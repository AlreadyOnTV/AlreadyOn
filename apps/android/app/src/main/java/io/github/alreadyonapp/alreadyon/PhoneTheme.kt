package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.text.TextStyle

// Native Modern Minimal Dark values match the sole web palette.
internal data class PhonePalette(val background: Color, val surface: Color,
    val alternate: Color, val text: Color, val muted: Color, val heading: Color,
    val accent: Color, val accentText: Color, val link: Color, val border: Color,
    val now: Color, val nowText: Color, val radius: Int)

// Modern Minimal Dark is fixed across all accounts and profiles.
internal val phonePalette = PhonePalette(Color(0xff12141a), Color(0xff1a1d26), Color(0xff21252f), Color(0xffdfe3ea), Color(0xff8b93a3), Color(0xffffffff), Color(0xff3a6ae0), Color(0xffffffff), Color(0xff5b8cff), Color(0xff2b303c), Color(0xff33406b), Color(0xffffffff), 10)

@Composable
internal fun PhoneTheme(content: @Composable () -> Unit) {
    val p = phonePalette
    val colors = darkColorScheme(primary = p.accent, onPrimary = p.accentText,
        primaryContainer = p.accent, onPrimaryContainer = p.accentText,
        secondary = p.heading, background = p.background, onBackground = p.text,
        surface = p.surface, onSurface = p.text, surfaceVariant = p.alternate,
        onSurfaceVariant = p.muted, outline = p.border)
    val typography = Typography().let { base ->
        val family = FontFamily.SansSerif
        // Android uses scalable pixels: reduce every role while preserving system font scaling.
        fun smaller(style: TextStyle, reduction: Int = 2) = style.copy(
            fontSize = (style.fontSize.value - reduction).sp,
            lineHeight = (style.lineHeight.value - reduction).sp,
            fontFamily = family)
        base.copy(
            displayLarge = smaller(base.displayLarge), displayMedium = smaller(base.displayMedium), displaySmall = smaller(base.displaySmall),
            headlineLarge = smaller(base.headlineLarge), headlineMedium = smaller(base.headlineMedium, 4).copy(color = p.heading), headlineSmall = smaller(base.headlineSmall),
            titleLarge = smaller(base.titleLarge).copy(color = p.heading), titleMedium = smaller(base.titleMedium).copy(color = p.heading), titleSmall = smaller(base.titleSmall),
            bodyLarge = smaller(base.bodyLarge), bodyMedium = smaller(base.bodyMedium), bodySmall = smaller(base.bodySmall),
            labelLarge = smaller(base.labelLarge), labelMedium = smaller(base.labelMedium), labelSmall = smaller(base.labelSmall))
    }
    MaterialTheme(colorScheme = colors, typography = typography,
        shapes = Shapes(small = RoundedCornerShape(p.radius.dp), medium = RoundedCornerShape(p.radius.dp), large = RoundedCornerShape(p.radius.dp)),
        content = content)
}
