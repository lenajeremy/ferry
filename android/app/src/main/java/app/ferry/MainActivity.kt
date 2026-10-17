package app.ferry

import android.app.Application
import android.content.pm.ApplicationInfo
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import app.ferry.ui.FerryTheme
import app.ferry.ui.LibraryScreen
import app.ferry.ui.PairScreen
import coil3.ImageLoader
import coil3.PlatformContext
import coil3.SingletonImageLoader
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import coil3.request.crossfade
import kotlinx.coroutines.launch

/** Images load through each computer's own client: over its secure port, with this phone's token. */
class FerryApp : Application(), SingletonImageLoader.Factory {
    override fun newImageLoader(context: PlatformContext): ImageLoader =
        ImageLoader.Builder(context)
            .components { add(OkHttpNetworkFetcherFactory(callFactory = { Net.calls })) }
            .crossfade(true)
            .build()
}

class MainActivity : ComponentActivity() {
    private val lib: Library by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val debug = applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
        // Debug builds (the emulator has no camera): `--es ferryQR <the QR code's link>` pairs as scanning would;
        // `--es ferryUrl http://… --es ferryCode …` as typing the code would. `--es ferryOpen <name>` /
        // `--ei ferrySelect 3` open the viewer or Select mode.
        if (debug && savedInstanceState == null) {
            val qr = intent.getStringExtra("ferryQR")?.let { Pairing.parse(it) }
            val url = intent.getStringExtra("ferryUrl")
            val code = intent.getStringExtra("ferryCode")
            lifecycleScope.launch {
                runCatching {
                    when {
                        qr != null -> lib.pair(FerryApi.pair(this@MainActivity, qr))
                        url != null && code != null -> lib.pair(FerryApi.pair(this@MainActivity, url, code))
                    }
                }.onFailure {
                    android.util.Log.w("Ferry", "debug pairing: ${it.message}")
                    lib.toast.value = it.message
                }
            }
        }
        val open = if (debug) intent.getStringExtra("ferryOpen") else null
        val select = if (debug) intent.getIntExtra("ferrySelect", 0) else 0
        setContent {
            FerryTheme {
                val computer by lib.computer.collectAsStateWithLifecycle()
                val adding by lib.adding.collectAsStateWithLifecycle()
                if (computer == null || adding) PairScreen(lib) else LibraryScreen(lib, open, select)
            }
        }
    }

    override fun onResume() {
        super.onResume()
        if (lib.computer.value != null && lib.status.value != Status.Connected) lib.start()
    }
}
