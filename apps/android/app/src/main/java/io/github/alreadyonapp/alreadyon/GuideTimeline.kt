package io.github.alreadyonapp.alreadyon

import androidx.compose.runtime.*
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import java.time.Instant

internal fun GuideChannel.liveProgram(now: Long): GuideProgram? = programs.firstOrNull {
    Instant.parse(it.startsAt).toEpochMilli() <= now && now < Instant.parse(it.endsAt).toEpochMilli()
}

internal fun guideRefreshDelay(channels: List<GuideChannel>, now: Long): Long {
    val nextEnd = channels.mapNotNull { it.liveProgram(now) }
        .minOfOrNull { Instant.parse(it.endsAt).toEpochMilli() - now } ?: 30_000L
    return minOf(30_000L - Math.floorMod(now, 30_000L), nextEnd).coerceAtLeast(1L)
}

// Owned by the shell so hidden guides keep advancing, including at program boundaries.
@Composable
internal fun rememberGuideTime(channels: List<GuideChannel>): Long {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    val owner = LocalLifecycleOwner.current
    LaunchedEffect(owner, channels) {
        owner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (true) {
                now = System.currentTimeMillis()
                delay(guideRefreshDelay(channels, now))
            }
        }
    }
    return now
}
