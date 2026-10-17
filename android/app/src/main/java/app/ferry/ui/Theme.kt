package app.ferry.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color

/** Ferry's colours: warm paper and ink, with a life-ring orange. The same as its web page and iPhone app. */
@Immutable
data class FerryColors(
    val bg: Color, val surface: Color, val surface2: Color,
    val ink: Color, val ink2: Color, val ink3: Color,
    val accent: Color, val ok: Color, val danger: Color,
)

val LightFerry = FerryColors(
    bg = Color(0xFFF3F1EC), surface = Color(0xFFFFFFFF), surface2 = Color(0xFFE9E6DF),
    ink = Color(0xFF16171A), ink2 = Color(0xFF5E6168), ink3 = Color(0xFF95979D),
    accent = Color(0xFFEE5229), ok = Color(0xFF1E9A54), danger = Color(0xFFD93A2B),
)

val DarkFerry = FerryColors(
    bg = Color(0xFF111214), surface = Color(0xFF1B1C1F), surface2 = Color(0xFF26272B),
    ink = Color(0xFFF3F2EF), ink2 = Color(0xFFA3A6AD), ink3 = Color(0xFF6D7077),
    accent = Color(0xFFFF6B45), ok = Color(0xFF3DD68C), danger = Color(0xFFFF5F52),
)

val LocalFerry = staticCompositionLocalOf { LightFerry }

@Composable
fun FerryTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val c = if (dark) DarkFerry else LightFerry
    val scheme = if (dark) {
        darkColorScheme(primary = c.accent, background = c.bg, surface = c.surface, onBackground = c.ink, onSurface = c.ink,
            surfaceVariant = c.surface2, onSurfaceVariant = c.ink2, error = c.danger)
    } else {
        lightColorScheme(primary = c.accent, background = c.bg, surface = c.surface, onBackground = c.ink, onSurface = c.ink,
            surfaceVariant = c.surface2, onSurfaceVariant = c.ink2, error = c.danger)
    }
    androidx.compose.runtime.CompositionLocalProvider(LocalFerry provides c) {
        MaterialTheme(colorScheme = scheme, content = content)
    }
}
