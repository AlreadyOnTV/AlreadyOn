package io.github.alreadyonapp.alreadyon

import androidx.compose.runtime.*
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.ui.Alignment
import android.util.TypedValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException

// Share preset IDs, emoji and background colors with web/src/users/avatars.tsx.
internal val avatarPresets = mapOf(
    "fox" to ("🦊" to 0xffe07a3e), "cat" to ("🐱" to 0xffe07ab0), "dog" to ("🐶" to 0xffd9a441),
    "lion" to ("🦁" to 0xffe0b93e), "panda" to ("🐼" to 0xff7a8a99), "koala" to ("🐨" to 0xff8f8f8f),
    "owl" to ("🦉" to 0xff8a6a4a), "frog" to ("🐸" to 0xff5fa35f), "monkey" to ("🐵" to 0xffb08a5a), "rabbit" to ("🐰" to 0xffa08ad0))

/** Centered avatar/name group supports web presets, uploaded icons and an initial fallback. */
@Composable
internal fun ProfileName(profile: ViewerProfile, model: PhoneModel, avatarSize: androidx.compose.ui.unit.Dp = 32.dp, showName: Boolean = true) {
    val bitmap by produceState<android.graphics.Bitmap?>(null, profile.id, profile.icon) {
        if (profile.icon?.startsWith("/profile-icons/") == true) {
            try { value = model.profileIcon(profile.icon) }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { value = null } // An unavailable avatar never blocks choosing a profile.
        }
    }
    val preset = avatarPresets[profile.icon]
    // The owner requested physical points, distinct from Android scalable pixels.
    val extraPixels = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_PT, 2f, LocalContext.current.resources.displayMetrics)
    val extraSize = with(LocalDensity.current) { extraPixels.toSp() }
    val nameSize = (MaterialTheme.typography.labelLarge.fontSize.value + extraSize.value).sp
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Box(Modifier.size(avatarSize).clip(CircleShape).background(preset?.let { Color(it.second) } ?: MaterialTheme.colorScheme.surfaceVariant),
            contentAlignment = Alignment.Center) {
            bitmap?.let { Image(it.asImageBitmap(), contentDescription = null, modifier = Modifier.fillMaxSize(), contentScale = ContentScale.Crop) }
                ?: Text(preset?.first ?: profile.name.trim().firstOrNull()?.uppercase().orEmpty().ifBlank { "?" },
                    fontSize = (avatarSize.value * 0.5625f).sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (showName) Text(profile.name, maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis, style = MaterialTheme.typography.labelLarge.copy(fontSize = nameSize, fontWeight = FontWeight.Bold))
    }
}
