package dev.droidline.agent.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

// Same palette as droidline.dev: paper and green-black ink, mint surfaces.
private val Light = lightColorScheme(
    primary = Color(0xFF15211C),
    onPrimary = Color(0xFFF5F1E8),
    secondary = Color(0xFF1F6B4D),
    onSecondary = Color.White,
    tertiary = Color(0xFFB83A17),
    background = Color(0xFFF5F1E8),
    onBackground = Color(0xFF15211C),
    surface = Color(0xFFFBF9F4),
    surfaceVariant = Color(0xFFEAE4D7),
    onSurface = Color(0xFF15211C),
    onSurfaceVariant = Color(0xFF4F5C55),
    outline = Color(0xFF74807A),
    error = Color(0xFFB3261E),
)

private val Dark = darkColorScheme(
    primary = Color(0xFFEDE9DF),
    onPrimary = Color(0xFF0E1613),
    secondary = Color(0xFF7FD3AA),
    onSecondary = Color(0xFF0E1613),
    tertiary = Color(0xFFFF8A6B),
    background = Color(0xFF0E1613),
    onBackground = Color(0xFFEDE9DF),
    surface = Color(0xFF17221E),
    surfaceVariant = Color(0xFF1C3329),
    onSurface = Color(0xFFEDE9DF),
    onSurfaceVariant = Color(0xFFA7B2AC),
    outline = Color(0xFF6E7B74),
    error = Color(0xFFF2B8B5),
)

/** Dynamic color stays off so the app looks the same on every phone. */
@Composable
fun DroidlineTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (isSystemInDarkTheme()) Dark else Light, content = content)
}
