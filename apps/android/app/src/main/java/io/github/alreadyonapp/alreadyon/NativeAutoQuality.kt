// Track consecutive pressure samples for the shared phone and TV player.
package io.github.alreadyonapp.alreadyon

import kotlin.math.abs
import kotlin.math.roundToInt

private val autoQualityHeights = listOf(240, 360, 480, 720, 1080, 2160)

// A wide film fills the 16:9 quality box horizontally before filling its height.
internal fun qualityRungHeight(width: Int, height: Int): Int {
    val fittedHeight = maxOf(height, (width * 9.0 / 16).roundToInt())
    return autoQualityHeights.firstOrNull { abs(it - fittedHeight) <= 2 } ?: fittedHeight
}

/** Called every five seconds for an active Auto transcode. */
internal class NativeAutoQuality {
    private var previousBuffer: Long? = null
    private var struggling = 0
    private var healthy = 0

    fun reset() {
        previousBuffer = null
        struggling = 0
        healthy = 0
    }

    fun observe(bufferedMs: Long, buffering: Boolean, height: Int, maximum: Int, serverPressure: Boolean = false, minimum: Int = 360, serverCeiling: Int = 0): Int? {
        val prior = previousBuffer ?: bufferedMs
        previousBuffer = bufferedMs
        val draining = prior - bufferedMs > 2000 // Ignore normal two-second HLS segment fluctuations.
        val ceiling = if (serverCeiling > 0) maxOf(minimum, serverCeiling) else Int.MAX_VALUE
        val constrained = if (serverCeiling > 0) height > ceiling else serverPressure
        val underPressure = constrained && height > minimum || buffering || bufferedMs < 3000 || bufferedMs < 5000 && draining
        struggling = if (underPressure) struggling + 1 else 0
        healthy = if (!underPressure && !constrained && bufferedMs >= 3000 && !draining) healthy + 1 else 0
        if (struggling >= 2) {
            struggling = 0
            return autoQualityHeights.lastOrNull { it < height && it >= minimum }
        }
        if (healthy >= 3) {
            healthy = 0
            return autoQualityHeights.firstOrNull { it > height && it <= minOf(maximum, ceiling) }
        }
        return null
    }
}
