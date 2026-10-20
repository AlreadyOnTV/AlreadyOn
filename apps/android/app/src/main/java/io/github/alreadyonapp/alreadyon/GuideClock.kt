// Format device-local guide time independently of schedule refreshes.
package io.github.alreadyonapp.alreadyon

import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

internal fun guideClockText(now: Long, timeFormat: String?, zone: ZoneId = ZoneId.systemDefault()): String =
    DateTimeFormatter.ofPattern(when (timeFormat) {
        "24h" -> "HH:mm"
        "12h-padded" -> "hh:mma"
        else -> "h:mma"
    }, Locale.US).withZone(zone).format(Instant.ofEpochMilli(now)).lowercase(Locale.US)

/** The clock owns its minute timer; resuming refreshes it without waiting for guide polling. */
@Composable
internal fun GuideClock(timeFormat: String?, palette: PhonePalette, modifier: Modifier = Modifier) {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    val owner = LocalLifecycleOwner.current
    LaunchedEffect(owner) {
        owner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (true) {
                now = System.currentTimeMillis()
                delay(60_000L - Math.floorMod(now, 60_000L))
            }
        }
    }
    val label = guideClockText(now, timeFormat)
    Text(label, modifier.testTag("guide-current-time").semantics { contentDescription = "Current time: $label" },
        color = palette.heading, maxLines = 1, softWrap = false)
}
