package dev.flowdrop.android.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

/**
 * Colour tokens mirror the web palette (`web/src/styles.css`): one accent blue,
 * a near-white page background and the card / divider greys for light mode;
 * Tokyo Night style warm grey-blue layers for dark mode instead of pure black.
 */
private val LightColors = lightColorScheme(
    primary = Color(0xFF2563EB),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFEAF1FE),
    onPrimaryContainer = Color(0xFF173A8A),
    secondary = Color(0xFF565E71),
    background = Color(0xFFF6F7FB),
    onBackground = Color(0xFF1C1C21),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF1C1C21),
    surfaceVariant = Color(0xFFF1F2F6),
    onSurfaceVariant = Color(0xFF4B4B53),
    outline = Color(0xFFD0D2DA),
    outlineVariant = Color(0xFFE3E3E8),
    error = Color(0xFFBA1A1A),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFF7AA2F7),
    onPrimary = Color(0xFF14142A),
    primaryContainer = Color(0xFF22304D),
    onPrimaryContainer = Color(0xFFDDE7FF),
    secondary = Color(0xFFB9B9C1),
    background = Color(0xFF1A1A1E),
    onBackground = Color(0xFFEDEDF1),
    surface = Color(0xFF232327),
    onSurface = Color(0xFFEDEDF1),
    surfaceVariant = Color(0xFF2B2B31),
    onSurfaceVariant = Color(0xFFB9B9C1),
    outline = Color(0xFF45454E),
    outlineVariant = Color(0xFF333339),
    error = Color(0xFFFFB4AB),
)

private val AppTypography = Typography(
    titleLarge = TextStyle(fontSize = 22.sp, fontWeight = FontWeight.SemiBold),
    titleMedium = TextStyle(fontSize = 17.sp, fontWeight = FontWeight.SemiBold),
    bodyLarge = TextStyle(fontSize = 16.sp, lineHeight = 24.sp),
    bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 20.sp),
    labelMedium = TextStyle(fontSize = 12.sp, fontWeight = FontWeight.Medium),
)

@Composable
fun FlowDropTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors,
        typography = AppTypography,
        content = content,
    )
}
