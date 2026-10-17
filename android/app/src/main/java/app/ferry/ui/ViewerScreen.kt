package app.ferry.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.MediaItem
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import app.ferry.FerryApi
import app.ferry.FerryFile
import app.ferry.Library
import app.ferry.Net
import app.ferry.dayLabel
import app.ferry.sizeLabel
import coil3.compose.SubcomposeAsyncImage
import java.text.DateFormat
import java.util.Date

/** One file at a time, full screen: swipe between them, pinch photos, play videos, open the rest. */
@Composable
fun ViewerScreen(lib: Library, start: String, onClose: () -> Unit, onDelete: (FerryFile) -> Unit) {
    val files by lib.files.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val initial = remember { files.indexOfFirst { it.name == start }.coerceAtLeast(0) }
    val pager = rememberPagerState(initialPage = initial) { files.size }
    var chrome by remember { mutableStateOf(true) }
    var zoomed by remember { mutableStateOf(false) }
    BackHandler(onBack = onClose)
    LaunchedEffect(files.size) { if (files.isEmpty()) onClose() }
    val file = files.getOrNull(pager.currentPage) ?: return

    Box(Modifier.fillMaxSize().background(Color.Black)) {
        HorizontalPager(state = pager, userScrollEnabled = !zoomed, key = { files.getOrNull(it)?.name ?: it }, modifier = Modifier.fillMaxSize()) { page ->
            val f = files.getOrNull(page) ?: return@HorizontalPager
            val api = lib.api ?: return@HorizontalPager
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                when (f.kind) {
                    "image" -> ZoomableImage(api, f, onZoom = { zoomed = it }, onTap = { chrome = !chrome })
                    "video" -> VideoPage(api, f, playing = pager.currentPage == page)
                    else -> DocumentPage(api, f) { lib.prepare(listOf(f)) { context.startActivity(lib.openIntent(it[0])) } }
                }
            }
        }
        if (chrome) {
            // The name and where it came from.
            Row(
                Modifier.fillMaxWidth().background(Brush.verticalGradient(listOf(Color.Black.copy(alpha = 0.55f), Color.Transparent)))
                    .statusBarsPadding().padding(horizontal = 12.dp, vertical = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RoundButton(Icons.Outlined.Close, "Close", onClick = onClose)
                Column(Modifier.weight(1f).padding(horizontal = 12.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(file.name, color = Color.White, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.MiddleEllipsis)
                    Text("From ${file.from} · ${sizeLabel(file.size)} · ${dayLabel(file.added)}, ${DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(file.added))}",
                        color = Color.White.copy(alpha = 0.6f), fontSize = 12.5.sp, maxLines = 1)
                }
                Spacer(Modifier.width(44.dp))
            }
            // What to do with it.
            Row(
                Modifier.align(Alignment.BottomCenter).fillMaxWidth()
                    .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.55f))))
                    .navigationBarsPadding().padding(bottom = 14.dp, top = 24.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterHorizontally), verticalAlignment = Alignment.CenterVertically,
            ) {
                if (file.isMedia) {
                    PillButton("Save", Icons.Outlined.Download) { lib.saveToGallery(listOf(file)) }
                } else {
                    PillButton("Open", Icons.Outlined.OpenInNew) { lib.prepare(listOf(file)) { context.startActivity(lib.openIntent(it[0])) } }
                }
                if (file.kind == "image") RoundButton(Icons.Outlined.ContentCopy, "Copy") { lib.copy(file) }
                RoundButton(Icons.Outlined.Share, "Share") { lib.prepare(listOf(file)) { context.startActivity(lib.shareIntent(it)) } }
                RoundButton(Icons.Outlined.Delete, "Delete", tint = Color(0xFFFF8A7F)) { onDelete(file) }
            }
        }
        Toast(lib, dark = true)
    }
}

@Composable
private fun ZoomableImage(api: FerryApi, f: FerryFile, onZoom: (Boolean) -> Unit, onTap: () -> Unit) {
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    val state = rememberTransformableState { zoom, pan, _ ->
        scale = (scale * zoom).coerceIn(1f, 5f)
        offset = if (scale > 1f) offset + pan * scale else Offset.Zero
        onZoom(scale > 1.01f)
    }
    SubcomposeAsyncImage(
        model = api.fileUrl(f), contentDescription = f.name, contentScale = ContentScale.Fit,
        loading = {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                SubcomposeAsyncImage(model = api.thumbUrl(f), contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
                CircularProgressIndicator(color = Color.White, modifier = Modifier.size(28.dp))
            }
        },
        // A phone can't read every format (RAW): the computer's large preview then.
        error = { SubcomposeAsyncImage(model = api.thumbUrl(f, large = true), contentDescription = f.name, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize()) },
        modifier = Modifier.fillMaxSize()
            .pointerInput(Unit) {
                detectTapGestures(
                    onTap = { onTap() },
                    onDoubleTap = {
                        scale = if (scale > 1f) 1f else 2.5f
                        offset = Offset.Zero
                        onZoom(scale > 1f)
                    },
                )
            }
            .transformable(state, canPan = { scale > 1f })
            .graphicsLayer(scaleX = scale, scaleY = scale, translationX = offset.x, translationY = offset.y),
    )
}

@Composable
private fun VideoPage(api: FerryApi, f: FerryFile, playing: Boolean) {
    val context = LocalContext.current
    val player = remember(f.v) {
        ExoPlayer.Builder(context).setMediaSourceFactory(DefaultMediaSourceFactory(OkHttpDataSource.Factory(Net.calls))).build().apply {
            setMediaItem(MediaItem.fromUri(api.fileUrl(f)))
            prepare()
        }
    }
    LaunchedEffect(playing) { if (!playing) player.pause() }
    DisposableEffect(player) { onDispose { player.release() } }
    // Above Ferry's own buttons, so the player's timeline isn't under them.
    AndroidView(
        factory = { PlayerView(it).apply { this.player = player; setShowNextButton(false); setShowPreviousButton(false) } },
        modifier = Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(top = 56.dp, bottom = 96.dp),
    )
}

@Composable
private fun DocumentPage(api: FerryApi, f: FerryFile, open: () -> Unit) {
    val c = LocalFerry.current
    Column(Modifier.fillMaxSize().padding(30.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
        if (f.kind == "pdf") {
            SubcomposeAsyncImage(model = api.thumbUrl(f, large = true), contentDescription = f.name, contentScale = ContentScale.Fit, modifier = Modifier.height(420.dp))
        } else {
            Text(f.ext, color = c.accent, fontSize = 17.sp, fontWeight = FontWeight.Bold, fontFamily = FontFamily.Monospace,
                modifier = Modifier.clip(RoundedCornerShape(10.dp)).background(c.accent.copy(alpha = 0.16f)).padding(horizontal = 14.dp, vertical = 10.dp))
        }
        Spacer(Modifier.height(14.dp))
        Text(f.name, color = Color.White, fontSize = 18.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
        Text(sizeLabel(f.size), color = Color.White.copy(alpha = 0.6f), fontSize = 15.sp)
        Spacer(Modifier.height(16.dp))
        PillButton("Open", Icons.Outlined.OpenInNew, onClick = open)
    }
}

@Composable
private fun PillButton(label: String, icon: ImageVector, onClick: () -> Unit) {
    Row(
        Modifier.height(48.dp).clip(RoundedCornerShape(50)).background(Color.White).clickable(onClick = onClick).padding(horizontal = 20.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = Color.Black, modifier = Modifier.size(19.dp))
        Spacer(Modifier.width(8.dp))
        Text(label, color = Color.Black, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun RoundButton(icon: ImageVector, label: String, tint: Color = Color.White, onClick: () -> Unit) {
    Box(
        Modifier.size(48.dp).clip(CircleShape).background(Color.White.copy(alpha = 0.15f)).clickable(onClickLabel = label, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Icon(icon, label, tint = tint, modifier = Modifier.size(21.dp))
    }
}
