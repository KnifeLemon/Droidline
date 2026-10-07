package dev.droidline.agent.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

// Same palette as droidline.dev. Tangerine itself is too light for text on white, so the primary
// color is its deeper text shade, which the app uses for selected tabs and "On" states.
private val Light = lightColorScheme(
    primary = Color(0xFFB53A07),
    onPrimary = Color.White,
    secondary = Color(0xFF2E3340),
    onSecondary = Color.White,
    tertiary = Color(0xFFFF6B21),
    onTertiary = Color(0xFF1A1004),
    background = Color.White,
    onBackground = Color(0xFF10131A),
    surface = Color(0xFFF3F4F7),
    surfaceVariant = Color(0xFFE9EBF0),
    onSurface = Color(0xFF10131A),
    onSurfaceVariant = Color(0xFF5D6475),
    outline = Color(0xFF8A90A0),
    error = Color(0xFFC42B2B),
)

private val Dark = darkColorScheme(
    primary = Color(0xFFFF9A5E),
    onPrimary = Color(0xFF1A1004),
    secondary = Color(0xFFD5D9E2),
    onSecondary = Color(0xFF1C202B),
    tertiary = Color(0xFFFF6B21),
    onTertiary = Color(0xFF1A1004),
    background = Color(0xFF1C202B),
    onBackground = Color(0xFFF3F4F7),
    surface = Color(0xFF242937),
    surfaceVariant = Color(0xFF161922),
    onSurface = Color(0xFFF3F4F7),
    onSurfaceVariant = Color(0xFF9AA1B2),
    outline = Color(0xFF6B7286),
    error = Color(0xFFFF6B6B),
)

/** Dynamic color stays off so the app looks the same on every phone. */
@Composable
fun DroidlineTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (isSystemInDarkTheme()) Dark else Light, content = content)
}
