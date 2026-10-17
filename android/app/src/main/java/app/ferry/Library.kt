package app.ferry

import android.app.Application
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Environment
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.Call
import java.io.File
import java.util.UUID

sealed interface Status {
    data object Connecting : Status
    data object Connected : Status
    data class Offline(val reason: String) : Status
}

data class Upload(
    val id: String = UUID.randomUUID().toString(),
    val uri: Uri,
    val name: String,
    val size: Long,
    val progress: Float = 0f,
    val state: State = State.Waiting,
    val error: String? = null,
) {
    enum class State { Waiting, Sending, Done, Failed }
    val busy get() = state == State.Waiting || state == State.Sending
    val kind get() = FerryFile.kindOf(name)
}

/** Everything the app shows: the paired computers and the one shown, its files (kept live), what's being sent, and messages. */
class Library(app: Application) : AndroidViewModel(app) {
    private val context: Context get() = getApplication()

    private val _computers = MutableStateFlow(Pairing.computers(app))
    val computers: StateFlow<List<FerryComputer>> = _computers.asStateFlow()
    private val _computer = MutableStateFlow(Pairing.current(app))
    val computer: StateFlow<FerryComputer?> = _computer.asStateFlow()
    /** Pairing another computer, with the library still there to go back to. */
    private val _adding = MutableStateFlow(false)
    val adding: StateFlow<Boolean> = _adding.asStateFlow()
    /** Why the pair screen shows instead of the library ("This phone was removed from …"). */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()
    private val _files = MutableStateFlow<List<FerryFile>>(emptyList())
    val files: StateFlow<List<FerryFile>> = _files.asStateFlow()
    private val _loaded = MutableStateFlow(false)
    val loaded: StateFlow<Boolean> = _loaded.asStateFlow()
    private val _status = MutableStateFlow<Status>(Status.Connecting)
    val status: StateFlow<Status> = _status.asStateFlow()
    private val _uploads = MutableStateFlow<List<Upload>>(emptyList())
    val uploads: StateFlow<List<Upload>> = _uploads.asStateFlow()
    val toast = MutableStateFlow<String?>(null)

    var api: FerryApi? = null
        private set
    /** What the computer calls its bin ("Trash", "Recycle Bin"). */
    var trash = "Trash"
        private set
    private var live: Job? = null
    private val sentHere = mutableSetOf<String>()
    private val calls = mutableMapOf<String, Call>()

    init {
        start()
    }

    // MARK: Computers

    /** Shows a newly paired computer (re-pairing one replaces its old pairing). */
    fun pair(c: FerryComputer) {
        _computers.value = Pairing.add(context, c)
        show(c)
    }

    /** Shows another paired computer. */
    fun select(id: String) {
        val c = _computers.value.firstOrNull { it.id == id } ?: return
        Pairing.save(context, _computers.value, id)
        show(c)
    }

    /** This phone's token for a computer it may be pairing with again (found by address or id). */
    fun previousKey(host: String, port: Int, id: String? = null): String? =
        _computers.value.firstOrNull { (it.host == host && it.port == port) || it.id == id }?.token

    /** Unpairs a computer on this phone, and tells it; the next one (if any) shows instead. */
    fun forget(id: String) {
        _computers.value.firstOrNull { it.id == id }?.let { c -> viewModelScope.launch { FerryApi(c).unpairSelf() } }
        val list = _computers.value.filter { it.id != id }
        val next = _computer.value?.takeIf { it.id != id } ?: list.firstOrNull()
        Pairing.save(context, list, next?.id)
        _computers.value = list
        when {
            next == null -> { live?.cancel(); api = null; _computer.value = null; reset() }
            next.id != _computer.value?.id -> show(next)
        }
    }

    fun startAdding() { _adding.value = true }
    fun stopAdding() { _adding.value = false }

    private fun show(c: FerryComputer) {
        _adding.value = false
        _notice.value = null
        _computer.value = c
        reset()
        start()
    }

    private fun reset() {
        _files.value = emptyList()
        _loaded.value = false
        _status.value = Status.Connecting
        trash = "Trash"
    }

    // MARK: Connecting

    /**
     * Keeps the list live: connects over the computer's secure port, follows updates, and retries after a
     * moment (looking the computer up on the Wi-Fi by its id now and then, in case its address changed).
     */
    fun start() {
        live?.cancel()
        var c = _computer.value ?: return
        live = viewModelScope.launch {
            var failures = 0
            while (isActive) {
                if (_status.value != Status.Connected) _status.value = Status.Connecting
                try {
                    if (c.token.isEmpty()) c = tradeIn(c) ?: return@launch
                    val a = FerryApi(c)
                    trash = a.info().optString("trash", "Trash")
                    api = a
                    a.events().collect { list ->
                        _status.value = Status.Connected
                        failures = 0
                        apply(list)
                    }
                    _status.value = Status.Offline("Lost the connection to ${c.name}")
                } catch (e: CancellationException) {
                    throw e
                } catch (e: RemovedException) {
                    removed(c, e.message!!)
                    return@launch
                } catch (e: Exception) {
                    _status.value = Status.Offline(e.message ?: "Can't reach ${c.name}")
                    if (c.id.isNotEmpty() && ++failures % 2 == 0) relocate(c)?.let { c = it }
                }
                delay(3000)
            }
        }
    }

    /** A pairing from before tokens: trades its code for this phone's own token (and the computer's certificate). */
    private suspend fun tradeIn(old: FerryComputer): FerryComputer? {
        val fresh = try {
            FerryApi.pair(context, "http://${old.host}:${old.port}", old.code ?: "")
        } catch (e: WrongCodeException) {
            Pairing.dropLegacy(context)
            removed(old, "Connect to ${old.name} again: its code has changed.")
            return null
        }
        Pairing.dropLegacy(context)
        _computers.value = Pairing.add(context, fresh)
        _computer.value = fresh
        return fresh
    }

    /** The computer turned this phone down: it's unpaired here too, and the pair screen says why. */
    private fun removed(c: FerryComputer, why: String) {
        val list = _computers.value.filter { it.id != c.id }
        Pairing.save(context, list, null)
        _computers.value = list
        api = null
        _computer.value = null
        reset()
        _notice.value = why
    }

    /** Looks for the computer on the Wi-Fi by its id; a new address is kept. */
    private suspend fun relocate(c: FerryComputer): FerryComputer? {
        val found = Discovery(context).find(c.id) ?: return null
        if (found.host == c.host && found.port == c.port) return null
        val moved = c.copy(host = found.host, port = found.port, tlsPort = if (found.tlsPort > 0) found.tlsPort else c.tlsPort)
        _computers.value = _computers.value.map { if (it.id == c.id) moved else it }
        Pairing.save(context, _computers.value, moved.id)
        if (_computer.value?.id == c.id) _computer.value = moved
        return moved
    }

    fun refresh() {
        viewModelScope.launch { runCatching { api?.files() }.getOrNull()?.let { apply(it) } }
    }

    private fun apply(list: List<FerryFile>) {
        if (_loaded.value) {
            val known = _files.value.map { it.name }.toSet()
            val arrived = list.filter { it.name !in known && it.name !in sentHere && it.from != "Android" }
            if (arrived.isNotEmpty()) toast.value = "${describe(arrived.map { it.kind })} from ${arrived[0].from}"
        }
        _files.value = list
        _loaded.value = true
    }

    // MARK: Sending

    val sending get() = _uploads.value.any { it.busy }

    fun send(uris: List<Uri>) {
        val resolver = context.contentResolver
        val added = uris.map { uri ->
            val (name, size) = FerryApi.describeUri(resolver, uri)
            Upload(uri = uri, name = name, size = size)
        }
        _uploads.update { current -> current.filter { it.busy || it.state == Upload.State.Failed } + added }
        pump()
    }

    private fun change(id: String, f: (Upload) -> Upload) = _uploads.update { list -> list.map { if (it.id == id) f(it) else it } }

    private fun pump() {
        val list = _uploads.value
        val free = 2 - list.count { it.state == Upload.State.Sending }
        list.filter { it.state == Upload.State.Waiting }.take(free.coerceAtLeast(0)).forEach { transfer(it) }
    }

    private fun transfer(u: Upload) {
        val a = api
        if (a == null) {
            change(u.id) { it.copy(state = Upload.State.Failed, error = "Not connected to your computer") }
            return
        }
        change(u.id) { it.copy(state = Upload.State.Sending, progress = 0f) }
        viewModelScope.launch {
            try {
                val saved = a.upload(context.contentResolver, u.uri, u.name, u.size, call = { calls[u.id] = it }) { p ->
                    change(u.id) { it.copy(progress = p) }
                }
                sentHere += saved.name
                change(u.id) { it.copy(state = Upload.State.Done, progress = 1f) }
            } catch (e: CancellationException) {
                _uploads.update { list -> list.filter { it.id != u.id } }
            } catch (e: Exception) {
                change(u.id) { it.copy(state = Upload.State.Failed, error = e.message ?: "Didn't send") }
            } finally {
                calls.remove(u.id)
            }
            pump()
            tidy()
        }
    }

    fun retry(u: Upload) {
        change(u.id) { it.copy(state = Upload.State.Waiting, error = null) }
        pump()
    }

    fun cancel(u: Upload) {
        calls[u.id]?.cancel() ?: _uploads.update { list -> list.filter { it.id != u.id } }
    }

    fun clearUploads() = _uploads.update { list -> list.filter { it.busy } }

    private fun tidy() {
        if (sending || _uploads.value.any { it.state == Upload.State.Failed }) return
        viewModelScope.launch {
            delay(2200)
            if (!sending) _uploads.update { list -> list.filter { it.state != Upload.State.Done } }
        }
    }

    // MARK: Saving, copying, sharing, deleting

    /** Saves photos and videos to the gallery (Pictures/Ferry, Movies/Ferry), as they are. */
    fun saveToGallery(chosen: List<FerryFile>) {
        val media = chosen.filter { it.isMedia }
        val a = api ?: return
        if (media.isEmpty()) return
        viewModelScope.launch {
            var saved = 0
            for (f in media) {
                try {
                    val file = a.download(context, f)
                    withContext(Dispatchers.IO) { addToGallery(file, f.kind == "video") }
                    saved++
                } catch (e: Exception) {
                    toast.value = "Couldn't save ${f.name}"
                }
            }
            if (saved > 0) toast.value = "Saved ${describe(media.take(saved).map { it.kind })} to the gallery"
        }
    }

    private fun addToGallery(file: File, video: Boolean) {
        val resolver = context.contentResolver
        val collection = if (video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, file.name)
            put(MediaStore.MediaColumns.MIME_TYPE, mimeOf(file.name))
            put(MediaStore.MediaColumns.RELATIVE_PATH, (if (video) Environment.DIRECTORY_MOVIES else Environment.DIRECTORY_PICTURES) + "/Ferry")
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }
        val uri = resolver.insert(collection, values) ?: throw FerryException("The gallery said no")
        resolver.openOutputStream(uri)?.use { out -> file.inputStream().use { it.copyTo(out) } }
        values.clear()
        values.put(MediaStore.MediaColumns.IS_PENDING, 0)
        resolver.update(uri, values, null, null)
    }

    /** Puts a photo on the clipboard, to paste into any app. */
    fun copy(f: FerryFile) {
        val a = api ?: return
        viewModelScope.launch {
            try {
                val file = a.download(context, f)
                val uri = shareUri(file)
                val clipboard = context.getSystemService(ClipboardManager::class.java)
                clipboard.setPrimaryClip(ClipData.newUri(context.contentResolver, f.name, uri))
                // Android 13 and later show their own preview of what was copied.
                if (android.os.Build.VERSION.SDK_INT < android.os.Build.VERSION_CODES.TIRAMISU) toast.value = "Copied. Paste it anywhere"
            } catch (e: Exception) {
                toast.value = "Couldn't copy ${f.name}"
            }
        }
    }

    /** Fetches files and hands them to [then] as shareable content URIs. */
    fun prepare(chosen: List<FerryFile>, then: (List<Uri>) -> Unit) {
        val a = api ?: return
        viewModelScope.launch {
            val uris = chosen.mapNotNull { f -> runCatching { shareUri(a.download(context, f)) }.getOrNull() }
            if (uris.size < chosen.size) toast.value = "Couldn't get ${chosen.size - uris.size} from your computer"
            if (uris.isNotEmpty()) then(uris)
        }
    }

    fun shareIntent(uris: List<Uri>): Intent {
        val send = if (uris.size == 1) {
            Intent(Intent.ACTION_SEND).putExtra(Intent.EXTRA_STREAM, uris[0])
        } else {
            Intent(Intent.ACTION_SEND_MULTIPLE).putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(uris))
        }
        send.type = uris.map { context.contentResolver.getType(it) ?: "*/*" }.distinct().singleOrNull() ?: "*/*"
        send.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        return Intent.createChooser(send, null)
    }

    fun openIntent(uri: Uri): Intent = Intent(Intent.ACTION_VIEW).setDataAndType(uri, context.contentResolver.getType(uri))
        .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)

    private fun shareUri(file: File): Uri = FileProvider.getUriForFile(context, "${context.packageName}.files", file)

    fun delete(chosen: List<FerryFile>) {
        val a = api ?: return
        viewModelScope.launch {
            var failed = 0
            for (f in chosen) runCatching { a.delete(f) }.onFailure { failed++ }
            toast.value = if (failed > 0) "Couldn't delete $failed" else "Moved ${describe(chosen.map { it.kind })} to the $trash on ${_computer.value?.name ?: "your computer"}"
            refresh()
        }
    }

    override fun onCleared() {
        live?.cancel()
    }
}

fun mimeOf(name: String): String =
    MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase()) ?: when (FerryFile.kindOf(name)) {
        "image" -> "image/*"
        "video" -> "video/*"
        else -> "application/octet-stream"
    }

/** Computers running Ferry on this Wi-Fi, found with Android's network service discovery (_ferry._tcp). */
class Discovery(context: Context) {
    /** One computer as it announces itself: its id, secure port and certificate fingerprint come from Bonjour's TXT record. */
    data class Found(val name: String, val host: String, val port: Int, val id: String, val tlsPort: Int, val fingerprint: String?)

    private val nsd = context.getSystemService(NsdManager::class.java)
    val found = MutableStateFlow<List<Found>>(emptyList())
    private val waiting = ArrayDeque<NsdServiceInfo>()
    private var resolving = false
    private var listener: NsdManager.DiscoveryListener? = null

    fun start() {
        if (listener != null) return
        val l = object : NsdManager.DiscoveryListener {
            override fun onServiceFound(info: NsdServiceInfo) = synchronized(this@Discovery) { waiting.add(info); next() }
            override fun onServiceLost(info: NsdServiceInfo) {
                found.update { list -> list.filter { it.name != info.serviceName } }
            }
            override fun onDiscoveryStarted(serviceType: String) {}
            override fun onDiscoveryStopped(serviceType: String) {}
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {}
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
        }
        listener = l
        nsd.discoverServices("_ferry._tcp", NsdManager.PROTOCOL_DNS_SD, l)
    }

    fun stop() {
        listener?.let { runCatching { nsd.stopServiceDiscovery(it) } }
        listener = null
    }

    /** Looks for one computer by its id for a few seconds. */
    suspend fun find(id: String, timeoutMs: Long = 5000): Found? {
        start()
        return try {
            withTimeoutOrNull(timeoutMs) { found.first { list -> list.any { it.id == id } }.first { it.id == id } }
        } finally {
            stop()
        }
    }

    // One resolve at a time (older Android allows no more).
    @Suppress("DEPRECATION")
    private fun next() {
        if (resolving) return
        val info = waiting.removeFirstOrNull() ?: return
        resolving = true
        nsd.resolveService(info, object : NsdManager.ResolveListener {
            override fun onServiceResolved(s: NsdServiceInfo) {
                val host = s.host?.hostAddress
                if (host != null && !host.contains(':')) {
                    val txt = s.attributes.mapValues { (_, v) -> v?.let { String(it) }.orEmpty() }
                    val one = Found(s.serviceName, host, s.port, txt["id"].orEmpty(), txt["tls"]?.toIntOrNull() ?: 0, txt["fp"]?.takeIf { it.isNotEmpty() })
                    found.update { list -> (list.filter { it.name != s.serviceName } + one).sortedBy { it.name } }
                }
                synchronized(this@Discovery) { resolving = false; next() }
            }
            override fun onResolveFailed(s: NsdServiceInfo, errorCode: Int) {
                synchronized(this@Discovery) { resolving = false; next() }
            }
        })
    }
}
