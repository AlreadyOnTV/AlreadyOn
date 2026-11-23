// Track consecutive pressure samples for the shared phone and TV player.
package io.github.alreadyonapp.alreadyon

/** Called every five seconds for an active Auto transcode. */
internal class NativeAutoQuality {
    private val heights = listOf(240, 360, 480, 720, 1080, 2160)
    private var previousBuffer: Long? = null
    private var struggling = 0
    private var healthy = 0

    fun reset() {
        previousBuffer = null
        struggling = 0
        healthy = 0
    }

    fun observe(bufferedMs: Long, buffering: Boolean, height: Int, maximum: Int, serverPressure: Boolean = false): Int? {
        val prior = previousBuffer ?: bufferedMs
        previousBuffer = bufferedMs
        val draining = prior - bufferedMs > 500
        val underPressure = serverPressure && height > 360 || buffering || bufferedMs < 3000 || bufferedMs < 5000 && draining
        struggling = if (underPressure) struggling + 1 else 0
        healthy = if (!underPressure && !serverPressure && bufferedMs >= 4500 && !draining) healthy + 1 else 0
        if (struggling >= 2) {
            struggling = 0
            return heights.lastOrNull { it < height }
        }
        if (healthy >= 3) {
            healthy = 0
            return heights.firstOrNull { it > height && it <= maximum }
        }
        return null
    }
}
