package app.ferry.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Computer
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.ferry.Discovery
import app.ferry.FerryApi
import app.ferry.Library
import app.ferry.Pairing
import app.ferry.R
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.launch

/** Pairing a computer: find it, then scan its code (or type it). Also where the other paired computers are, to switch to. */
@Composable
fun PairScreen(lib: Library) {
    val c = LocalFerry.current
    val context = LocalContext.current
    val discovery = remember { Discovery(context) }
    val found by discovery.found.collectAsStateWithLifecycle()
    val computers by lib.computers.collectAsStateWithLifecycle()
    val current by lib.computer.collectAsStateWithLifecycle()
    val notice by lib.notice.collectAsStateWithLifecycle()
    var typing by remember { mutableStateOf<Discovery.Found?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val adding = current != null
    val paired = computers.map { it.id }.toSet()

    DisposableEffect(Unit) {
        discovery.start()
        onDispose { discovery.stop() }
    }
    BackHandler(adding) { lib.stopAdding() }

    val scanner = rememberLauncherForActivityResult(ScanContract()) { result ->
        val text = result.contents ?: return@rememberLauncherForActivityResult
        val link = Pairing.parse(text)
        if (link == null) {
            problem = "That's not Ferry's code"
            return@rememberLauncherForActivityResult
        }
        busy = true
        scope.launch {
            try {
                lib.pair(FerryApi.pair(context, link, lib.previousKey(link.host, link.port)))
            } catch (e: Exception) {
                problem = e.message
            }
            busy = false
        }
    }

    Box(Modifier.fillMaxSize().background(c.bg)) {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(26.dp),
        ) {
            Column(Modifier.padding(top = if (adding) 8.dp else 36.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                if (adding) {
                    Box(Modifier.size(40.dp).clip(CircleShape).background(c.surface).clickable { lib.stopAdding() }, contentAlignment = Alignment.Center) {
                        Icon(Icons.Outlined.Close, "Close", tint = c.ink, modifier = Modifier.size(20.dp))
                    }
                } else {
                    Image(painterResource(R.drawable.logo), contentDescription = null, modifier = Modifier.size(64.dp).clip(RoundedCornerShape(15.dp)))
                }
                Text(if (adding) "Add a computer" else "Connect to your computer", fontSize = 32.sp, fontWeight = FontWeight.Bold, color = c.ink, lineHeight = 38.sp)
                Text("Open Ferry on your Mac or PC, then scan the code on it. Photos, videos and files go both ways after that.",
                    fontSize = 17.sp, color = c.ink2, lineHeight = 24.sp)
                notice?.let {
                    Text(it, fontSize = 15.sp, color = c.danger, lineHeight = 21.sp,
                        modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(c.danger.copy(alpha = 0.09f)).padding(14.dp))
                }
            }
            InkButton(if (busy) "Connecting…" else "Scan the code", icon = Icons.Outlined.QrCodeScanner, enabled = !busy) {
                problem = null
                scanner.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("Point the camera at the code on your computer's screen")
                    .setBeepEnabled(false).setOrientationLocked(false))
            }
            problem?.let { Text(it, color = c.danger, fontSize = 15.sp) }

            // Computers already paired (other than the one being shown), to switch to.
            val others = computers.filter { it.id != current?.id }
            if (others.isNotEmpty()) {
                Section("YOUR COMPUTERS") {
                    others.forEachIndexed { i, computer ->
                        if (i > 0) Divider()
                        Choice(Icons.Outlined.Computer, computer.name, if (computer.secure) "Paired · Encrypted" else "Paired") { lib.select(computer.id) }
                    }
                }
            }

            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Section("ON THIS WI-FI") {
                    if (found.isEmpty()) {
                        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = c.ink3)
                            Text("Looking for Ferry on this Wi-Fi…", color = c.ink2, fontSize = 16.sp)
                        }
                    }
                    found.forEachIndexed { i, computer ->
                        if (i > 0) Divider()
                        val known = computer.id.isNotEmpty() && computer.id in paired
                        val showing = known && computer.id == current?.id
                        Choice(Icons.Outlined.Computer, computer.name, when {
                            showing -> "Paired · Showing now"
                            known -> "Paired · Tap to switch"
                            else -> "Type its code"
                        }) {
                            when {
                                showing -> lib.stopAdding()
                                known -> lib.select(computer.id)
                                else -> typing = computer
                            }
                        }
                    }
                }
                Text("Not showing up? Check Ferry is running on your computer and that both are on the same Wi-Fi. Scanning the code always works.",
                    fontSize = 13.sp, color = c.ink3, modifier = Modifier.padding(horizontal = 4.dp))
            }
            Row(Modifier.padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Icon(Icons.Outlined.Lock, null, tint = c.ink3, modifier = Modifier.size(15.dp))
                Text("Scanning the code lets Ferry check it's your computer before sending anything. Everything after is encrypted.", fontSize = 13.sp, color = c.ink3)
            }
            Spacer(Modifier.height(30.dp))
        }
        Toast(lib)
    }

    typing?.let { computer ->
        CodeDialog(computer, onDismiss = { typing = null }) { code ->
            lib.pair(FerryApi.pair(context, computer.host, computer.port, code, computer.tlsPort, computer.fingerprint,
                lib.previousKey(computer.host, computer.port, computer.id)))
            typing = null
        }
    }
}

@Composable
private fun Section(title: String, content: @Composable () -> Unit) {
    val c = LocalFerry.current
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text(title, fontSize = 12.sp, fontWeight = FontWeight.SemiBold, letterSpacing = 1.sp, color = c.ink3, modifier = Modifier.padding(start = 4.dp))
        Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(c.surface)) { content() }
    }
}

@Composable
private fun Divider() {
    val c = LocalFerry.current
    HorizontalDivider(Modifier.padding(start = 60.dp), color = c.ink.copy(alpha = 0.08f))
}

@Composable
private fun Choice(icon: ImageVector, title: String, subtitle: String, onClick: () -> Unit) {
    val c = LocalFerry.current
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 13.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = c.ink, modifier = Modifier.width(32.dp))
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(title, fontSize = 17.sp, fontWeight = FontWeight.Medium, color = c.ink)
            Text(subtitle, fontSize = 14.sp, color = c.ink2)
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null, tint = c.ink3)
    }
}

@Composable
private fun CodeDialog(computer: Discovery.Found, onDismiss: () -> Unit, connect: suspend (String) -> Unit) {
    val c = LocalFerry.current
    var code by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val go = {
        busy = true
        problem = null
        scope.launch {
            try { connect(code) } catch (e: Exception) { problem = e.message }
            busy = false
        }
        Unit
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = c.bg,
        title = { Text(computer.name, color = c.ink) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
                Text("Type the code under the QR code on Ferry's page.", color = c.ink2)
                OutlinedTextField(
                    value = code, onValueChange = { code = it.take(12) }, singleLine = true, placeholder = { Text("xxxx xxxx", modifier = Modifier.fillMaxWidth(), textAlign = TextAlign.Center) },
                    textStyle = TextStyle(fontSize = 26.sp, fontWeight = FontWeight.SemiBold, fontFamily = FontFamily.Monospace, textAlign = TextAlign.Center, color = c.ink),
                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.None, autoCorrectEnabled = false, imeAction = ImeAction.Go),
                    keyboardActions = KeyboardActions(onGo = { if (Pairing.normalise(code).length >= 8) go() }),
                    modifier = Modifier.fillMaxWidth(),
                )
                problem?.let { Text(it, color = c.danger, fontSize = 15.sp) }
            }
        },
        confirmButton = {
            TextButton(onClick = go, enabled = Pairing.normalise(code).length >= 8 && !busy) { Text(if (busy) "Connecting…" else "Connect", color = c.accent) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel", color = c.ink2) } },
    )
}
