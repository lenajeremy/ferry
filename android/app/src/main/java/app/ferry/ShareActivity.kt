package app.ferry

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
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
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowUpward
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.PriorityHigh
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.IntentCompat
import app.ferry.ui.FerryTheme
import app.ferry.ui.LocalFerry
import kotlinx.coroutines.delay

/** "Ferry" in the share sheet: sends what's shared straight to the paired computer. */
class ShareActivity : ComponentActivity() {
    private sealed interface Phase {
        data object Sending : Phase
        data object Done : Phase
        data class Failed(val reason: String) : Phase
    }

    private var phase by mutableStateOf<Phase>(Phase.Sending)
    private var progress by mutableFloatStateOf(0f)
    private var sent by mutableIntStateOf(0)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val uris = shared(intent)
        val computer = Pairing.current(this)
        setContent {
            FerryTheme {
                val c = LocalFerry.current
                LaunchedEffect(Unit) {
                    if (computer == null) { phase = Phase.Failed("Open Ferry first and connect it to your computer."); return@LaunchedEffect }
                    if (computer.token.isEmpty()) { phase = Phase.Failed("Open Ferry once to finish connecting to ${computer.name}."); return@LaunchedEffect }
                    if (uris.isEmpty()) { phase = Phase.Failed("There's nothing in that to send."); return@LaunchedEffect }
                    try {
                        // The computer Ferry is showing, over its secure port.
                        val api = FerryApi(computer).also { it.info() }
                        uris.forEachIndexed { i, uri ->
                            val (name, size) = FerryApi.describeUri(contentResolver, uri)
                            api.upload(contentResolver, uri, name, size) { p -> progress = (i + p) / uris.size }
                            sent = i + 1
                        }
                        progress = 1f
                        phase = Phase.Done
                        delay(1200)
                        finish()
                    } catch (e: Exception) {
                        phase = Phase.Failed(e.message ?: "Couldn't send")
                    }
                }
                val tint = when (phase) { is Phase.Failed -> c.danger; Phase.Done -> c.ok; Phase.Sending -> c.accent }
                Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.3f)).clickable { if (phase != Phase.Sending) finish() }, contentAlignment = Alignment.BottomCenter) {
                    Column(
                        Modifier.navigationBarsPadding().padding(14.dp).fillMaxWidth().clip(RoundedCornerShape(24.dp)).background(c.surface).padding(18.dp),
                        verticalArrangement = Arrangement.spacedBy(14.dp),
                    ) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(Modifier.size(40.dp).clip(CircleShape).background(tint.copy(alpha = 0.14f)), contentAlignment = Alignment.Center) {
                                Icon(when (phase) { is Phase.Failed -> Icons.Outlined.PriorityHigh; Phase.Done -> Icons.Outlined.Check; Phase.Sending -> Icons.Outlined.ArrowUpward }, null, tint = tint)
                            }
                            Spacer(Modifier.width(12.dp))
                            Column {
                                val name = computer?.name ?: "your computer"
                                Text(when (val p = phase) {
                                    Phase.Sending -> if (uris.size > 1) "Sending ${minOf(sent + 1, uris.size)} of ${uris.size}" else "Sending…"
                                    Phase.Done -> "Sent to $name"
                                    is Phase.Failed -> "Couldn't send"
                                }, fontSize = 17.sp, fontWeight = FontWeight.SemiBold, color = c.ink)
                                Text(when (val p = phase) {
                                    Phase.Sending -> "To $name, with Ferry"
                                    Phase.Done -> if (uris.size == 1) "It's in the Ferry folder" else "They're in the Ferry folder"
                                    is Phase.Failed -> p.reason
                                }, fontSize = 14.sp, color = c.ink2, maxLines = 2)
                            }
                        }
                        if (phase == Phase.Sending) LinearProgressIndicator(progress = { progress }, Modifier.fillMaxWidth(), color = c.accent, trackColor = c.surface2)
                        Box(Modifier.fillMaxWidth().height(46.dp).clip(RoundedCornerShape(14.dp)).background(c.surface2).clickable { finish() }, contentAlignment = Alignment.Center) {
                            Text(if (phase == Phase.Sending) "Cancel" else "Done", fontSize = 16.sp, fontWeight = FontWeight.SemiBold, color = c.ink)
                        }
                    }
                }
            }
        }
    }

    private fun shared(intent: Intent): List<Uri> = when (intent.action) {
        Intent.ACTION_SEND -> listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))
        Intent.ACTION_SEND_MULTIPLE -> IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
        else -> emptyList()
    }
}
