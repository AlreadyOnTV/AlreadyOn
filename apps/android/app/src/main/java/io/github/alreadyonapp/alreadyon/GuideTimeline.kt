package io.github.alreadyonapp.alreadyon

import android.os.SystemClock
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import java.time.Instant
import java.time.ZoneId

internal const val GUIDE_NAVIGATION_PAUSE_MS = 120_000L
internal const val GUIDE_REFRESH_INTERVAL_MS = 60_000L
internal const val GUIDE_SLOT_MINUTES = 15
internal const val GUIDE_HEADER_MINUTES = 30
internal const val GUIDE_SLOT_MS = GUIDE_SLOT_MINUTES * 60_000L

internal fun guideOrigin(now: Long, zone: ZoneId = ZoneId.systemDefault()): Long {
    val local = Instant.ofEpochMilli(now).atZone(zone)
    // Subtract elapsed local time to retain the offset during a repeated hour.
    return now - (local.minute % GUIDE_SLOT_MINUTES) * 60_000L - local.second * 1000L - local.nano / 1_000_000
}

internal fun guideShowsHeader(stamp: Long, zone: ZoneId = ZoneId.systemDefault()): Boolean =
    Instant.ofEpochMilli(stamp).atZone(zone).minute % GUIDE_HEADER_MINUTES == 0

internal fun guideNavigationRemaining(deadline: Long, elapsed: Long): Long = (deadline - elapsed).coerceAtLeast(0L)

internal fun GuideChannel.liveProgram(now: Long): GuideProgram? = programs.firstOrNull {
    Instant.parse(it.startsAt).toEpochMilli() <= now && now < Instant.parse(it.endsAt).toEpochMilli()
}

internal fun guideRefreshDelay(channels: List<GuideChannel>, now: Long): Long {
    val nextEnd = channels.mapNotNull { it.liveProgram(now) }
        .minOfOrNull { Instant.parse(it.endsAt).toEpochMilli() - now } ?: GUIDE_REFRESH_INTERVAL_MS
    return minOf(GUIDE_REFRESH_INTERVAL_MS - Math.floorMod(now, GUIDE_REFRESH_INTERVAL_MS), nextEnd).coerceAtLeast(1L)
}

// Owned by the shell so hidden guides keep advancing, including at program boundaries.
@Composable
internal fun rememberGuideTime(channels: List<GuideChannel>, navigationDeadline: Long = 0L): Long {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    val owner = LocalLifecycleOwner.current
    LaunchedEffect(owner, channels, navigationDeadline) {
        owner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (true) {
                val remaining = guideNavigationRemaining(navigationDeadline, SystemClock.elapsedRealtime())
                if (remaining > 0L) {
                    delay(remaining)
                    continue
                }
                now = System.currentTimeMillis()
                delay(guideRefreshDelay(channels, now))
            }
        }
    }
    return now
}

@Composable
internal fun Modifier.guideNavigationInput(onInteraction: () -> Unit, onExit: () -> Unit = {}): Modifier {
    val interaction by rememberUpdatedState(onInteraction)
    val exit by rememberUpdatedState(onExit)
    DisposableEffect(Unit) { onDispose { exit() } }
    return onPreviewKeyEvent { event ->
        if (event.type == KeyEventType.KeyDown && event.key in listOf(Key.DirectionUp, Key.DirectionDown, Key.DirectionLeft, Key.DirectionRight)) interaction()
        false
    }.pointerInput(Unit) {
        awaitPointerEventScope {
            while (true) {
                val event = awaitPointerEvent(PointerEventPass.Initial)
                if (event.type == PointerEventType.Scroll || event.changes.any { it.pressed || it.previousPressed }) interaction()
            }
        }
    }
}
