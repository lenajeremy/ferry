package app.ferry.ui

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Computer
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.PhotoLibrary
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.SwapVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.ferry.FerryApi
import app.ferry.FerryComputer
import app.ferry.FerryFile
import app.ferry.Library
import app.ferry.Status
import app.ferry.Upload
import app.ferry.dayLabel
import app.ferry.describe
import app.ferry.sizeLabel
import coil3.compose.SubcomposeAsyncImage
import kotlinx.coroutines.delay

/** The computer's Ferry folder: send to it, see what's there, save, copy or share what you want. */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun LibraryScreen(lib: Library, openFirst: String? = null, selectFirst: Int = 0) {
    val c = LocalFerry.current
    val context = LocalContext.current
    val computer by lib.computer.collectAsStateWithLifecycle()
    val computers by lib.computers.collectAsStateWithLifecycle()
    val files by lib.files.collectAsStateWithLifecycle()
    val loaded by lib.loaded.collectAsStateWithLifecycle()
    val status by lib.status.collectAsStateWithLifecycle()
    val uploads by lib.uploads.collectAsStateWithLifecycle()
    var viewing by remember { mutableStateOf<String?>(null) }
    var selecting by remember { mutableStateOf(false) }
    var selected by remember { mutableStateOf(setOf<String>()) }
    var confirmDelete by remember { mutableStateOf<List<FerryFile>?>(null) }
    var forgetting by remember { mutableStateOf<FerryComputer?>(null) }
    var sheet by remember { mutableStateOf(false) }
    var refreshing by remember { mutableStateOf(false) }

    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(100)) { if (it.isNotEmpty()) lib.send(it) }
    val documents = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { if (it.isNotEmpty()) lib.send(it) }

    LaunchedEffect(loaded) {
        if (!loaded) return@LaunchedEffect
        openFirst?.let { name -> if (files.any { it.name == name }) viewing = name }
        if (selectFirst > 0) {
            selecting = true
            selected = files.filter { it.isMedia }.take(selectFirst).map { it.name }.toSet()
        }
    }
    BackHandler(selecting) { selecting = false; selected = emptySet() }

    val name = computer?.name ?: "your computer"
    val statusLine = when (val s = status) {
        Status.Connecting -> "Connecting to $name…"
        Status.Connected -> "Connected to $name"
        is Status.Offline -> s.reason
    }
    val chosen = files.filter { it.name in selected }

    Box(Modifier.fillMaxSize().background(c.bg)) {
        Column(Modifier.fillMaxSize()) {
            // The bar: the computers (switch, add, forget), Select.
            Row(Modifier.fillMaxWidth().statusBarsPadding().padding(horizontal = 8.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { sheet = true }) {
                    Icon(Icons.Outlined.Computer, "Your computers", tint = if (status == Status.Connected) c.ink else c.ink3)
                }
                Spacer(Modifier.weight(1f))
                if (files.isNotEmpty() || selecting) {
                    TextButton(onClick = { selecting = !selecting; selected = emptySet() }) {
                        Text(if (selecting) "Done" else "Select", color = c.accent, fontSize = 17.sp, fontWeight = FontWeight.SemiBold)
                    }
                }
            }
            PullToRefreshBox(isRefreshing = refreshing, onRefresh = { refreshing = true; lib.refresh() }, modifier = Modifier.weight(1f)) {
                LaunchedEffect(refreshing) { if (refreshing) { delay(700); refreshing = false } }
                LazyVerticalGrid(
                    columns = GridCells.Fixed(3),
                    contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 140.dp),
                    horizontalArrangement = Arrangement.spacedBy(3.dp),
                    verticalArrangement = Arrangement.spacedBy(3.dp),
                    modifier = Modifier.fillMaxSize(),
                ) {
                    item(span = { GridItemSpan(maxLineSpan) }) {
                        Column(Modifier.padding(top = 4.dp, bottom = 12.dp)) {
                            Text(if (selecting) (if (selected.isEmpty()) "Select files" else "${selected.size} selected") else "Ferry",
                                fontSize = 34.sp, fontWeight = FontWeight.Bold, color = c.ink)
                            if (!selecting) {
                                Row(Modifier.clip(RoundedCornerShape(8.dp)).clickable { sheet = true }.padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                                    if (status == Status.Connected && computer?.secure == true) {
                                        Icon(Icons.Outlined.Lock, "Encrypted", tint = c.ink2, modifier = Modifier.size(13.dp))
                                        Spacer(Modifier.width(4.dp))
                                    }
                                    Text(statusLine, fontSize = 14.sp, color = c.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                                    Icon(Icons.Outlined.ExpandMore, null, tint = c.ink3, modifier = Modifier.size(18.dp))
                                }
                            }
                            if (!selecting) {
                                Row(Modifier.padding(top = 18.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                    InkButton("Photos", Icons.Outlined.PhotoLibrary, Modifier.weight(1f)) {
                                        photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
                                    }
                                    SoftButton("Files", Icons.Outlined.Folder, Modifier.width(140.dp)) { documents.launch(arrayOf("*/*")) }
                                }
                            }
                        }
                    }
                    if (loaded && files.isEmpty()) {
                        item(span = { GridItemSpan(maxLineSpan) }) { Empty() }
                    }
                    if (!loaded) {
                        item(span = { GridItemSpan(maxLineSpan) }) {
                            Column(Modifier.padding(top = 70.dp, start = 30.dp, end = 30.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                                if (status is Status.Offline) {
                                    Text((status as Status.Offline).reason, color = c.ink2, textAlign = TextAlign.Center)
                                    TextButton(onClick = { lib.start() }) { Text("Try again", color = c.accent, fontWeight = FontWeight.SemiBold) }
                                } else {
                                    CircularProgressIndicator(color = c.ink3)
                                }
                            }
                        }
                    }
                    files.groupBy { dayLabel(it.added) }.forEach { (label, dayFiles) ->
                        item(span = { GridItemSpan(maxLineSpan) }, key = "day-$label") {
                            Text(label, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, color = c.ink2, modifier = Modifier.padding(start = 2.dp, top = 10.dp, bottom = 5.dp))
                        }
                        items(dayFiles, key = { it.name }) { f ->
                            val isSelected = f.name in selected
                            Box(
                                Modifier.aspectRatio(1f).clip(RoundedCornerShape(10.dp)).combinedClickable(
                                    onClick = {
                                        if (selecting) selected = if (isSelected) selected - f.name else selected + f.name
                                        else viewing = f.name
                                    },
                                    onLongClick = { selecting = true; selected = selected + f.name },
                                ),
                            ) {
                                Tile(f, lib.api)
                                if (selecting) {
                                    if (isSelected) Box(Modifier.fillMaxSize().background(Color.White.copy(alpha = 0.18f)).border(3.dp, c.accent, RoundedCornerShape(10.dp)))
                                    Icon(
                                        if (isSelected) Icons.Filled.CheckCircle else Icons.Outlined.Close, null,
                                        tint = if (isSelected) c.accent else Color.Transparent,
                                        modifier = Modifier.align(Alignment.TopEnd).padding(6.dp).size(24.dp).clip(CircleShape)
                                            .background(if (isSelected) Color.White else Color.Black.copy(alpha = 0.25f))
                                            .border(2.dp, Color.White, CircleShape),
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }

        // What's being sent, or what to do with the selection.
        Box(Modifier.align(Alignment.BottomCenter).navigationBarsPadding()) {
            if (selecting) {
                SelectBar(
                    chosen,
                    selectAll = { selected = files.map { it.name }.toSet() },
                    save = { lib.saveToGallery(chosen); selecting = false; selected = emptySet() },
                    share = { lib.prepare(chosen) { context.startActivity(lib.shareIntent(it)) } },
                    delete = { confirmDelete = chosen },
                )
            } else if (uploads.isNotEmpty()) {
                UploadTray(uploads, lib)
            }
        }
        Toast(lib)

        viewing?.let { start ->
            ViewerScreen(lib, start, onClose = { viewing = null }, onDelete = { confirmDelete = listOf(it) })
        }
    }

    confirmDelete?.let { list ->
        AlertDialog(
            onDismissRequest = { confirmDelete = null },
            containerColor = c.bg,
            title = { Text(if (list.size == 1) "Delete ${list[0].name}?" else "Delete ${list.size} files?", color = c.ink) },
            text = { Text("They'll go to the ${lib.trash} on $name.", color = c.ink2) },
            confirmButton = {
                TextButton(onClick = { lib.delete(list); confirmDelete = null; selecting = false; selected = emptySet() }) {
                    Text("Move to the ${lib.trash}", color = c.danger)
                }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = null }) { Text("Cancel", color = c.ink2) } },
        )
    }
    if (sheet) {
        ModalBottomSheet(onDismissRequest = { sheet = false }, containerColor = c.bg) {
            ComputersSheet(
                computers, computer?.id, status,
                select = { sheet = false; lib.select(it.id) },
                add = { sheet = false; lib.startAdding() },
                forget = { forgetting = it },
            )
        }
    }
    forgetting?.let { f ->
        AlertDialog(
            onDismissRequest = { forgetting = null },
            containerColor = c.bg,
            title = { Text("Forget ${f.name}?", color = c.ink) },
            text = { Text("This phone stops connecting to it. To connect again, scan its code.", color = c.ink2) },
            confirmButton = { TextButton(onClick = { forgetting = null; lib.forget(f.id); if (computers.size <= 1) sheet = false }) { Text("Forget", color = c.danger) } },
            dismissButton = { TextButton(onClick = { forgetting = null }) { Text("Cancel", color = c.ink2) } },
        )
    }
}

/** The paired computers: the one showing (ticked), the others to switch to, adding another, forgetting one. */
@Composable
private fun ComputersSheet(
    computers: List<FerryComputer>, current: String?, status: Status,
    select: (FerryComputer) -> Unit, add: () -> Unit, forget: (FerryComputer) -> Unit,
) {
    val c = LocalFerry.current
    Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(start = 20.dp, end = 20.dp, bottom = 20.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        Text("Your computers", fontSize = 22.sp, fontWeight = FontWeight.Bold, color = c.ink)
        Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(c.surface)) {
            computers.forEachIndexed { i, computer ->
                if (i > 0) androidx.compose.material3.HorizontalDivider(Modifier.padding(start = 60.dp), color = c.ink.copy(alpha = 0.08f))
                val showing = computer.id == current
                Row(
                    Modifier.fillMaxWidth().clickable { select(computer) }.padding(start = 16.dp, end = 6.dp, top = 10.dp, bottom = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(Icons.Outlined.Computer, null, tint = c.ink, modifier = Modifier.width(32.dp))
                    Spacer(Modifier.width(14.dp))
                    Column(Modifier.weight(1f)) {
                        Text(computer.name, fontSize = 17.sp, fontWeight = FontWeight.Medium, color = c.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            if (computer.secure) {
                                Icon(Icons.Outlined.Lock, null, tint = c.ink3, modifier = Modifier.size(12.dp))
                                Spacer(Modifier.width(4.dp))
                            }
                            Text(when {
                                showing && status == Status.Connected -> "Connected"
                                showing -> "Showing now"
                                else -> "Paired"
                            } + if (computer.secure) " · Encrypted" else "", fontSize = 13.5.sp, color = c.ink2)
                        }
                    }
                    if (showing) Icon(Icons.Filled.CheckCircle, "Showing", tint = c.accent, modifier = Modifier.padding(end = 6.dp).size(22.dp))
                    TextButton(onClick = { forget(computer) }) { Text("Forget", color = c.ink3, fontSize = 14.sp) }
                }
            }
        }
        SoftButton("Add a computer", Icons.Outlined.Add) { add() }
    }
}

@Composable
private fun Empty() {
    val c = LocalFerry.current
    Column(Modifier.fillMaxWidth().padding(top = 60.dp, start = 30.dp, end = 30.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(58.dp).clip(RoundedCornerShape(16.dp)).background(c.surface), contentAlignment = Alignment.Center) {
            Icon(Icons.Outlined.SwapVert, null, tint = c.ink3, modifier = Modifier.size(28.dp))
        }
        Spacer(Modifier.height(14.dp))
        Text("Nothing here yet", fontSize = 19.sp, fontWeight = FontWeight.SemiBold, color = c.ink)
        Spacer(Modifier.height(6.dp))
        Text("Send something from this phone, or drop files on Ferry's page on your computer.", fontSize = 15.sp, color = c.ink2, textAlign = TextAlign.Center)
    }
}

/** A square in the grid: the computer's thumbnail, or a card naming the file. */
@Composable
fun Tile(f: FerryFile, api: FerryApi?) {
    val c = LocalFerry.current
    Box(Modifier.fillMaxSize().background(c.surface2)) {
        if (api != null && (f.kind == "image" || f.kind == "video" || f.kind == "pdf")) {
            SubcomposeAsyncImage(
                model = api.thumbUrl(f), contentDescription = f.name, contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(), error = { DocFace(f) },
            )
        } else {
            DocFace(f)
        }
        if (f.kind == "video") {
            Box(Modifier.align(Alignment.BottomStart).padding(6.dp).size(22.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.5f)), contentAlignment = Alignment.Center) {
                Icon(Icons.Filled.PlayArrow, null, tint = Color.White, modifier = Modifier.size(14.dp))
            }
        }
    }
}

@Composable
fun DocFace(f: FerryFile) {
    val c = LocalFerry.current
    Column(Modifier.fillMaxSize().background(c.surface).border(1.dp, c.ink.copy(alpha = 0.07f), RoundedCornerShape(10.dp)).padding(10.dp)) {
        Text(f.ext, fontSize = 11.sp, fontWeight = FontWeight.Bold, fontFamily = FontFamily.Monospace, color = c.accent,
            modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(c.accent.copy(alpha = 0.13f)).padding(horizontal = 7.dp, vertical = 4.dp))
        Spacer(Modifier.weight(1f))
        Text(f.name, fontSize = 12.5.sp, color = c.ink, maxLines = 3, overflow = TextOverflow.Ellipsis, lineHeight = 15.sp)
        Text(sizeLabel(f.size), fontSize = 11.5.sp, color = c.ink3)
    }
}

@Composable
private fun SelectBar(chosen: List<FerryFile>, selectAll: () -> Unit, save: () -> Unit, share: () -> Unit, delete: () -> Unit) {
    val c = LocalFerry.current
    Row(
        Modifier.fillMaxWidth().background(c.bg.copy(alpha = 0.96f)).padding(horizontal = 16.dp, vertical = 10.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        if (chosen.isEmpty()) {
            SoftButton("Select all", null, Modifier.weight(1f), onClick = selectAll)
        } else {
            if (chosen.any { it.isMedia }) InkButton("Save", Icons.Outlined.Download, Modifier.weight(1f), onClick = save)
            SoftButton("Share", Icons.Outlined.Share, Modifier.weight(1f), onClick = share)
            SoftButton("", Icons.Outlined.Delete, Modifier.width(64.dp), tint = c.danger, onClick = delete)
        }
    }
}

/** What's being sent: one line while it goes, each file when there are several. */
@Composable
private fun UploadTray(ups: List<Upload>, lib: Library) {
    val c = LocalFerry.current
    val total = ups.sumOf { it.size.coerceAtLeast(1) }.toFloat()
    val sent = ups.sumOf { (it.size.coerceAtLeast(1) * (if (it.state == Upload.State.Done) 1f else it.progress)).toDouble() }.toFloat()
    val busy = ups.any { it.busy }
    val failed = ups.count { it.state == Upload.State.Failed }
    val what = describe(ups.map { it.kind })
    Column(
        Modifier.padding(12.dp).fillMaxWidth().shadow(18.dp, RoundedCornerShape(20.dp), ambientColor = Color.Black.copy(alpha = 0.25f))
            .clip(RoundedCornerShape(20.dp)).background(c.surface).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(if (busy) "Sending $what" else if (failed == 0) "Sent $what" else "$failed didn't send",
                fontSize = 16.sp, fontWeight = FontWeight.SemiBold, color = c.ink, modifier = Modifier.weight(1f))
            if (busy) Text("${(sent / total * 100).toInt()}%", fontSize = 14.sp, color = c.ink2)
            else if (failed > 0) TextButton(onClick = { lib.clearUploads() }) { Text("Clear", color = c.accent) }
        }
        LinearProgressIndicator(progress = { (sent / total).coerceIn(0f, 1f) }, modifier = Modifier.fillMaxWidth(),
            color = if (!busy && failed == 0) c.ok else c.accent, trackColor = c.surface2)
        if (ups.size > 1 || failed > 0) {
            LazyColumn(Modifier.heightIn(max = 150.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                items(ups, key = { it.id }) { u ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(u.name, fontSize = 14.sp, color = c.ink, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                        when (u.state) {
                            Upload.State.Waiting -> Text("Waiting", fontSize = 13.sp, color = c.ink3)
                            Upload.State.Sending -> Text("${(u.progress * 100).toInt()}%", fontSize = 13.sp, color = c.ink3)
                            Upload.State.Done -> Text("✓", fontSize = 13.sp, color = c.ok, fontWeight = FontWeight.Bold)
                            Upload.State.Failed -> {
                                Text(u.error ?: "Failed", fontSize = 13.sp, color = c.danger, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.width(140.dp))
                                IconButton(onClick = { lib.retry(u) }, modifier = Modifier.size(30.dp)) { Icon(Icons.Outlined.Refresh, "Try again", tint = c.ink2) }
                            }
                        }
                        if (u.busy) IconButton(onClick = { lib.cancel(u) }, modifier = Modifier.size(30.dp)) { Icon(Icons.Outlined.Close, "Cancel", tint = c.ink3) }
                    }
                }
            }
        }
    }
}

/** A short message at the top ("3 photos from Mac"), gone after a moment. */
@Composable
fun Toast(lib: Library, dark: Boolean = false) {
    val c = LocalFerry.current
    val message by lib.toast.collectAsStateWithLifecycle()
    LaunchedEffect(message) {
        if (message != null) { delay(2600); lib.toast.value = null }
    }
    Box(Modifier.fillMaxWidth().statusBarsPadding().padding(top = 8.dp), contentAlignment = Alignment.TopCenter) {
        AnimatedVisibility(message != null, enter = fadeIn() + slideInVertically { -it }, exit = fadeOut() + slideOutVertically { -it }) {
            Text(message ?: "", fontSize = 15.sp, fontWeight = FontWeight.SemiBold, color = if (dark) Color.Black else c.bg,
                modifier = Modifier.clip(RoundedCornerShape(50)).background(if (dark) Color.White else c.ink).clickable { lib.toast.value = null }
                    .padding(horizontal = 18.dp, vertical = 12.dp))
        }
    }
}

@Composable
fun InkButton(label: String, icon: ImageVector? = null, modifier: Modifier = Modifier.fillMaxWidth(), enabled: Boolean = true, onClick: () -> Unit) {
    val c = LocalFerry.current
    Row(
        modifier.height(54.dp).clip(RoundedCornerShape(16.dp)).background(if (enabled) c.ink else c.ink.copy(alpha = 0.4f)).clickable(enabled = enabled, onClick = onClick),
        horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) Icon(icon, null, tint = c.bg, modifier = Modifier.size(20.dp))
        if (icon != null && label.isNotEmpty()) Spacer(Modifier.width(8.dp))
        if (label.isNotEmpty()) Text(label, color = c.bg, fontSize = 17.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
fun SoftButton(label: String, icon: ImageVector? = null, modifier: Modifier = Modifier.fillMaxWidth(), tint: Color? = null, onClick: () -> Unit) {
    val c = LocalFerry.current
    Row(
        modifier.height(54.dp).clip(RoundedCornerShape(16.dp)).background(c.surface).border(1.dp, c.ink.copy(alpha = 0.08f), RoundedCornerShape(16.dp)).clickable(onClick = onClick),
        horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) Icon(icon, label.ifEmpty { "Delete" }, tint = tint ?: c.ink, modifier = Modifier.size(20.dp))
        if (icon != null && label.isNotEmpty()) Spacer(Modifier.width(8.dp))
        if (label.isNotEmpty()) Text(label, color = tint ?: c.ink, fontSize = 17.sp, fontWeight = FontWeight.SemiBold)
    }
}
