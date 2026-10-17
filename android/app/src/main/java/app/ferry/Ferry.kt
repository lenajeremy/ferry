package app.ferry

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import android.provider.Settings
import android.util.Base64
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.withContext
import okhttp3.Call
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okio.BufferedSink
import okio.source
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.File
import java.io.IOException
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.TextStyle
import java.util.Locale
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

/**
 * A computer this phone is paired with. Pairing traded the code for a token of the phone's own,
 * and gave Ferry's certificate authority on that computer: everything after goes over its secure
 * port, trusting that authority alone.
 */
data class FerryComputer(
    val id: String,          // the computer's own id
    val name: String,
    val host: String,        // its .local name or address
    val port: Int,           // the page (plain HTTP: only the certificate is fetched there)
    val tlsPort: Int,        // the secure port; 0 for a computer without one
    val ca: String?,         // base64 DER of its certificate authority
    val token: String,
    val code: String? = null, // a pairing from before tokens, until it has been traded in
) {
    val secure get() = tlsPort > 0 && ca != null
    val base get() = if (secure) "https://$host:$tlsPort" else "http://$host:$port"

    fun toJson(): JSONObject = JSONObject().put("id", id).put("name", name).put("host", host).put("port", port)
        .put("tlsPort", tlsPort).put("ca", ca ?: JSONObject.NULL).put("token", token)

    companion object {
        fun from(o: JSONObject) = FerryComputer(
            o.getString("id"), o.getString("name"), o.getString("host"), o.getInt("port"), o.optInt("tlsPort"),
            o.optString("ca").takeIf { it.isNotEmpty() && it != "null" }, o.getString("token"),
        )
    }
}

/** One file in the computer's Ferry folder. */
data class FerryFile(
    val name: String,
    val size: Long,
    val added: Long,
    val kind: String,
    val from: String,
    val v: String,
) {
    val isMedia get() = kind == "image" || kind == "video"
    val ext get() = name.substringAfterLast('.', "").uppercase().take(4).ifEmpty { "FILE" }

    companion object {
        fun from(o: JSONObject) = FerryFile(
            o.getString("name"), o.getLong("size"), o.getLong("added"), o.getString("kind"), o.getString("from"), o.getString("v"),
        )

        fun list(a: JSONArray) = (0 until a.length()).map { from(a.getJSONObject(it)) }

        fun kindOf(name: String): String = when (name.substringAfterLast('.', "").lowercase()) {
            "jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "avif", "tif", "tiff", "dng", "bmp" -> "image"
            "mp4", "mov", "m4v", "webm", "3gp" -> "video"
            else -> "file"
        }
    }
}

open class FerryException(message: String) : IOException(message)

/** The computer turned this phone's token down: it was removed there (or the computer was reset). */
class RemovedException(name: String) : FerryException("This phone was removed from $name. Connect again.")

/** The code typed or scanned isn't the computer's (any more). */
class WrongCodeException(message: String) : FerryException(message)

/** "1 photo", "3 videos", "2 files" (mixed kinds are files). */
fun describe(kinds: List<String>): String {
    val one = mapOf("image" to "photo", "video" to "video")[kinds.firstOrNull()] ?: "file"
    val word = if (kinds.toSet().size == 1) one else "file"
    return "${kinds.size} $word${if (kinds.size == 1) "" else "s"}"
}

/** "Today", "Yesterday", "Monday 13 October". */
fun dayLabel(ms: Long, today: LocalDate = LocalDate.now()): String {
    val day = Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).toLocalDate()
    return when {
        !day.isBefore(today) -> "Today"
        day == today.minusDays(1) -> "Yesterday"
        else -> {
            val weekday = day.dayOfWeek.getDisplayName(TextStyle.FULL, Locale.getDefault())
            val rest = day.format(DateTimeFormatter.ofPattern(if (day.year == today.year) "d MMMM" else "d MMMM yyyy"))
            "$weekday $rest"
        }
    }
}

fun sizeLabel(bytes: Long): String = when {
    bytes >= 1_000_000_000 -> "%.1f GB".format(bytes / 1e9)
    bytes >= 1_000_000 -> "%.1f MB".format(bytes / 1e6)
    bytes >= 1_000 -> "${bytes / 1000} KB"
    else -> "$bytes bytes"
}

/** What the QR code on a computer's page says: where Ferry is, its code, and (newer computers) its certificate's fingerprint and secure port. */
data class QrLink(val host: String, val port: Int, val code: String, val fingerprint: String?, val tlsPort: Int)

/** The computers this phone is paired with, and which one it shows, in the app's own preferences. */
object Pairing {
    private const val PREFS = "pairing"

    private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun computers(context: Context): List<FerryComputer> {
        val list = runCatching {
            val a = JSONArray(prefs(context).getString("computers", "[]"))
            (0 until a.length()).map { FerryComputer.from(a.getJSONObject(it)) }
        }.getOrDefault(emptyList())
        Net.computers = list
        return list
    }

    /** The one shown: the chosen computer, else the first, else a pairing from before tokens (still to trade in). */
    fun current(context: Context): FerryComputer? {
        val list = computers(context)
        val id = prefs(context).getString("current", null)
        return list.firstOrNull { it.id == id } ?: list.firstOrNull() ?: legacy(context)
    }

    fun save(context: Context, list: List<FerryComputer>, current: String?) {
        prefs(context).edit().putString("computers", JSONArray(list.map { it.toJson() }).toString()).putString("current", current).apply()
        Net.computers = list
    }

    /** Adds a newly paired computer (in place of an older pairing with the same computer) and shows it. */
    fun add(context: Context, c: FerryComputer): List<FerryComputer> {
        val list = computers(context).filter { it.id != c.id } + c
        save(context, list, c.id)
        return list
    }

    /** A pairing kept by an older Ferry: the computer's address and its code. */
    private fun legacy(context: Context): FerryComputer? {
        val p = prefs(context)
        val url = p.getString("url", null)?.toHttpUrlOrNull() ?: return null
        val code = p.getString("code", null) ?: return null
        return FerryComputer("", p.getString("name", "your computer")!!, url.host, url.port, 0, null, "", code)
    }

    fun dropLegacy(context: Context) = prefs(context).edit().remove("url").remove("code").remove("name").remove("fallback").apply()

    /** Reads the QR code on Ferry's computer page: http://<computer>:7070/?k=<code>&f=<fingerprint>&s=<secure port>. */
    fun parse(text: String): QrLink? {
        val url = text.trim().toHttpUrlOrNull() ?: return null
        val code = url.queryParameter("k") ?: return null
        if (normalise(code).length < 6) return null
        val fp = url.queryParameter("f")?.takeIf { it.length >= 16 }
        val tls = url.queryParameter("s")?.toIntOrNull() ?: 0
        return QrLink(url.host, url.port, normalise(code), fp, if (fp != null) tls else 0)
    }

    fun normalise(code: String) = code.lowercase().filter { it.isLetterOrDigit() }

    /** How this phone introduces itself to a computer: its name in Settings, else its model. */
    fun deviceName(context: Context): String =
        Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME)?.takeIf { it.isNotBlank() } ?: Build.MODEL
}

/**
 * Ferry's server on a paired computer, over its secure port. Every request carries the phone's token
 * (added by that computer's own HTTP client, which images and videos use too); changes carry Ferry's header.
 */
class FerryApi(val computer: FerryComputer) {
    private val baseUrl: HttpUrl = computer.base.toHttpUrlOrNull() ?: throw FerryException("Not a valid address: ${computer.base}")
    private val client = Net.clientFor(computer)

    fun url(path: String, query: Map<String, String> = emptyMap()): String {
        val b = baseUrl.newBuilder().encodedPath(path)
        query.toSortedMap().forEach { (k, v) -> b.addQueryParameter(k, v) }
        return b.build().toString()
    }

    private fun named(prefix: String, f: FerryFile, query: Map<String, String> = emptyMap()): String {
        val b = baseUrl.newBuilder().encodedPath(prefix).addPathSegment(f.name)
        query.toSortedMap().forEach { (k, v) -> b.addQueryParameter(k, v) }
        return b.build().toString()
    }

    fun fileUrl(f: FerryFile) = named("/f/", f)
    fun thumbUrl(f: FerryFile, large: Boolean = false) = named("/thumb/", f, if (large) mapOf("s" to "large", "v" to f.v) else mapOf("v" to f.v))

    private fun request(url: String, method: String = "GET", body: RequestBody? = null): Request =
        Request.Builder().url(url).method(method, body).apply {
            if (method != "GET") header("X-Ferry", "1")
        }.build()

    private fun send(r: Request, c: OkHttpClient = client): String {
        val response = try {
            c.newCall(r).execute()
        } catch (e: IOException) {
            throw unreachable(e)
        }
        response.use {
            val text = it.body?.string().orEmpty()
            if (it.code == 401) throw RemovedException(computer.name)
            if (!it.isSuccessful) throw FerryException(text.trim().ifEmpty { "Your computer said no (${it.code})." })
            return text
        }
    }

    suspend fun info(): JSONObject = withContext(Dispatchers.IO) {
        JSONObject(send(request(url("/api/info", mapOf("device" to "Android")))))
    }

    suspend fun files(): List<FerryFile> = withContext(Dispatchers.IO) { FerryFile.list(JSONArray(send(request(url("/api/files"))))) }

    suspend fun delete(f: FerryFile) = withContext(Dispatchers.IO) { send(request(named("/api/files/", f), "DELETE")) }

    /** Tells the computer this phone forgot it, so it drops the phone from its list too. */
    suspend fun unpairSelf() = withContext(Dispatchers.IO) { runCatching { send(request("${computer.base}/api/devices/self", "DELETE")) } }

    /** Sends a file to the computer, streamed from its content URI, reporting progress (0…1). */
    suspend fun upload(resolver: ContentResolver, uri: Uri, name: String, size: Long, call: (Call) -> Unit = {}, progress: (Float) -> Unit): FerryFile =
        withContext(Dispatchers.IO) {
            val body = object : RequestBody() {
                override fun contentType(): MediaType = "application/octet-stream".toMediaType()
                override fun contentLength(): Long = if (size > 0) size else -1
                override fun writeTo(sink: BufferedSink) {
                    val input = resolver.openInputStream(uri) ?: throw FerryException("Couldn't read $name")
                    input.source().use { source ->
                        var sent = 0L
                        while (true) {
                            val n = source.read(sink.buffer, 256 * 1024)
                            if (n < 0) break
                            sink.emit()
                            sent += n
                            if (size > 0) progress((sent.toFloat() / size).coerceAtMost(1f))
                        }
                    }
                }
            }
            val r = request(url("/api/upload", mapOf("name" to name, "device" to "Android")), "PUT", body)
            val c = client.newBuilder().writeTimeout(30, TimeUnit.MINUTES).readTimeout(30, TimeUnit.MINUTES).build().newCall(r)
            call(c)
            val response = try {
                c.execute()
            } catch (e: IOException) {
                if (c.isCanceled()) throw kotlinx.coroutines.CancellationException("cancelled")
                throw FerryException("Lost the connection to your computer")
            }
            response.use {
                val text = it.body?.string().orEmpty()
                if (it.code == 401) throw RemovedException(computer.name)
                if (!it.isSuccessful) throw FerryException(text.trim().ifEmpty { "Your computer said no (${it.code})." })
                FerryFile.from(JSONObject(text))
            }
        }

    /** Fetches a file into the app's cache under its own name (for the gallery, sharing, copying). */
    suspend fun download(context: Context, f: FerryFile): File = withContext(Dispatchers.IO) {
        val dir = File(context.cacheDir, "ferry/${f.v}").apply { mkdirs() }
        val dest = File(dir, f.name)
        if (dest.exists() && dest.length() == f.size) return@withContext dest
        val response = try {
            client.newCall(request(fileUrl(f))).execute()
        } catch (e: IOException) {
            throw unreachable(e)
        }
        response.use {
            if (it.code == 401) throw RemovedException(computer.name)
            if (!it.isSuccessful) throw FerryException("Couldn't get ${f.name} (${it.code})")
            val tmp = File(dir, ".${f.name}.part")
            tmp.outputStream().use { out -> it.body!!.byteStream().copyTo(out) }
            tmp.renameTo(dest)
        }
        dest
    }

    /** The computer's live list: a fresh copy of every file each time anything changes. */
    fun events(): Flow<List<FerryFile>> = callbackFlow {
        val call = client.newBuilder().readTimeout(0, TimeUnit.SECONDS).build().newCall(request(url("/api/events", mapOf("device" to "Android"))))
        val response = try {
            call.execute()
        } catch (e: IOException) {
            close(unreachable(e))
            return@callbackFlow
        }
        if (response.code == 401) {
            response.close()
            close(RemovedException(computer.name))
            return@callbackFlow
        }
        Thread {
            try {
                response.use {
                    val source = it.body!!.source()
                    var event = ""
                    var data = ""
                    while (!source.exhausted()) {
                        val line = source.readUtf8Line() ?: break
                        when {
                            line.startsWith("event:") -> event = line.removePrefix("event:").trim()
                            line.startsWith("data:") -> data = line.removePrefix("data:").trim()
                            line.isEmpty() -> {
                                if (event == "files") trySend(FerryFile.list(JSONArray(data)))
                                if (event == "hello") trySend(FerryFile.list(JSONObject(data).getJSONArray("files")))
                                event = ""; data = ""
                            }
                        }
                    }
                }
                close()
            } catch (e: Exception) {
                close(if (call.isCanceled()) null else FerryException("Lost the connection to your computer"))
            }
        }.start()
        awaitClose { call.cancel() }
    }.flowOn(Dispatchers.IO)

    companion object {
        private fun unreachable(e: IOException): FerryException =
            if (e is javax.net.ssl.SSLException) FerryException("This isn't the computer this phone paired with. Connect again if it was reset.")
            else FerryException("Can't reach your computer. Is Ferry running there, and is this phone on the same Wi-Fi?")

        /** Base64url (no padding) of the certificate's SHA-256, first 22 characters, as in the QR code and on Bonjour. */
        fun fingerprint(der: ByteArray): String =
            Base64.encodeToString(MessageDigest.getInstance("SHA-256").digest(der), Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP).take(22)

        /** Ferry's certificate authority on a computer, from its page (public; checked against a fingerprint before it's trusted). */
        private fun fetchAuthority(host: String, port: Int): ByteArray? {
            val r = Request.Builder().url("http://$host:$port/ferry.cer").build()
            val response = try {
                Net.plain.newCall(r).execute()
            } catch (e: IOException) {
                throw FerryException("Can't reach that computer. Is it on the same Wi-Fi as this phone?")
            }
            return response.use { if (it.isSuccessful) it.body?.bytes() else null }
        }

        /**
         * Pairs with a computer: trades the code for this phone's own token, over the secure port when the
         * computer has one. [fingerprint] (from the QR code, or Bonjour) must match the certificate the
         * computer hands over before the code is sent; a computer without a secure port pairs as before.
         * [previous] is this phone's old token there, when pairing again, so the computer replaces its entry.
         */
        suspend fun pair(context: Context, host: String, port: Int, code: String, tlsPort: Int, fingerprint: String?, previous: String? = null): FerryComputer =
            withContext(Dispatchers.IO) {
                var ca: ByteArray? = null
                var base = "http://$host:$port"
                var client = Net.plain
                if (tlsPort > 0) {
                    ca = fetchAuthority(host, port) ?: throw FerryException("That computer didn't hand over its certificate. Is Ferry up to date there?")
                    if (fingerprint != null && fingerprint(ca) != fingerprint) {
                        throw FerryException("This computer isn't the one showing that code. Scan it again from your computer's screen.")
                    }
                    base = "https://$host:$tlsPort"
                    client = Net.pinned(ca)
                }
                val body = JSONObject().put("code", Pairing.normalise(code)).put("name", Pairing.deviceName(context)).put("kind", "Android")
                if (previous != null) body.put("previous", previous)
                val r = Request.Builder().url("$base/api/pair").post(body.toString().toRequestBody("application/json".toMediaType())).build()
                val response = try {
                    client.newCall(r).execute()
                } catch (e: IOException) {
                    throw unreachable(e)
                }
                val reply = response.use {
                    val text = it.body?.string().orEmpty()
                    if (it.code == 401) throw WrongCodeException(runCatching { JSONObject(text).getString("error") }.getOrDefault("That code doesn't match. Check the one on your computer."))
                    if (it.code == 404 || it.code == 405) throw FerryException("Ferry on that computer needs updating first.")
                    if (!it.isSuccessful) throw FerryException("Your computer said no (${it.code}).")
                    JSONObject(text)
                }
                val sentCA = reply.optString("ca").takeIf { it.isNotEmpty() }
                // Paired over the secure port: the authority in the reply must be the one already checked.
                if (ca != null && sentCA != null && !Base64.decode(sentCA, Base64.DEFAULT).contentEquals(ca)) {
                    throw FerryException("This computer isn't the one showing that code. Scan it again from your computer's screen.")
                }
                FerryComputer(
                    id = reply.optString("id").ifEmpty { "$host:$port" }, name = reply.optString("name", "Computer"),
                    host = host, port = port, tlsPort = reply.optInt("tlsPort"), ca = sentCA, token = reply.getString("token"),
                )
            }

        /** Pairs from a scanned QR code. */
        suspend fun pair(context: Context, link: QrLink, previous: String? = null) =
            pair(context, link.host, link.port, link.code, link.tlsPort, link.fingerprint, previous)

        /**
         * Pairs with a computer known only by its address (a debug launch): its secure port is the next one, as
         * Ferry sets it up, when it hands over a certificate; otherwise the page's own port.
         */
        suspend fun pair(context: Context, url: String, code: String): FerryComputer {
            val u = url.toHttpUrlOrNull() ?: throw FerryException("Not a valid address: $url")
            val hasCA = withContext(Dispatchers.IO) { runCatching { fetchAuthority(u.host, u.port) }.getOrNull() != null }
            return pair(context, u.host, u.port, code, if (hasCA) u.port + 1 else 0, null)
        }

        /** A content URI's file name and size, as the picker or the sharing app gives them. */
        fun describeUri(resolver: ContentResolver, uri: Uri): Pair<String, Long> {
            var name: String? = null
            var size = -1L
            resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
                if (c.moveToFirst()) {
                    name = c.getString(0)
                    if (!c.isNull(1)) size = c.getLong(1)
                }
            }
            val type = resolver.getType(uri).orEmpty()
            val ext = android.webkit.MimeTypeMap.getSingleton().getExtensionFromMimeType(type) ?: ""
            val generic = (if (type.startsWith("video/")) "video" else "image") + if (ext.isEmpty()) "" else ".$ext"
            // Android's photo picker hides real names ("18.jpg"): send those as generic photos and
            // videos, which the computer names by the time they arrive, as it does an iPhone's.
            val shown = name ?: uri.lastPathSegment?.substringAfterLast('/')
            val hidden = shown == null || Regex("""\d+(\.\w+)?""").matches(shown)
            return (if (hidden) generic else shown!!) to size
        }
    }
}

/**
 * HTTP clients. Each paired computer gets its own: it trusts only that computer's certificate authority
 * and signs in with this phone's token. Images (Coil) and videos (Media3) go through [calls], which
 * picks the client by the address being fetched.
 */
object Net {
    /** The paired computers, kept in step with [Pairing]. */
    @Volatile var computers: List<FerryComputer> = emptyList()

    /** For a computer's certificate and the pairing of computers without a secure port. */
    val plain: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    private val clients = mutableMapOf<String, OkHttpClient>()

    /** Trusts [caDER] alone, and checks the computer's name or address against its certificate as usual. */
    fun pinned(caDER: ByteArray, base: OkHttpClient = plain): OkHttpClient {
        val ca = CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(caDER)) as X509Certificate
        val keys = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null, null); setCertificateEntry("ferry", ca) }
        val trust = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(keys) }
            .trustManagers.filterIsInstance<X509TrustManager>().first()
        val tls = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), null) }
        return base.newBuilder().sslSocketFactory(tls.socketFactory, trust).build()
    }

    /** The client for one computer: pinned to its authority (when it has a secure port), with its token. */
    fun clientFor(c: FerryComputer): OkHttpClient = synchronized(clients) {
        clients.getOrPut("${c.id}|${c.token}|${c.ca?.hashCode()}") {
            val auth = Interceptor { chain ->
                val r = chain.request()
                chain.proceed(if (c.token.isNotEmpty() && r.header("Authorization") == null) r.newBuilder().header("Authorization", "Ferry ${c.token}").build() else r)
            }
            val signed = plain.newBuilder().addInterceptor(auth).build()
            c.ca?.let { pinned(Base64.decode(it, Base64.DEFAULT), signed) } ?: signed
        }
    }

    /** Requests for images and videos: each goes through the client of the computer it's addressed to. */
    val calls = Call.Factory { request ->
        val u = request.url
        val c = computers.firstOrNull { it.host == u.host && (if (it.secure) it.tlsPort else it.port) == u.port }
        (c?.let { clientFor(it) } ?: plain).newCall(request)
    }
}
