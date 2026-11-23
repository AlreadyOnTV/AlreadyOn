// Verify the native Auto timing with synthetic playback samples.
package io.github.alreadyonapp.alreadyon

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class NativeAutoQualityTest {
    @Test fun wideOutputUsesItsFittedRung() {
        val auto = NativeAutoQuality()
        val rung = qualityRungHeight(1280, 532)
        assertEquals(720, rung)
        assertEquals(1080, qualityRungHeight(1920, 800))
        repeat(2) { assertNull(auto.observe(4000, false, rung, 1080)) }
        assertEquals(1080, auto.observe(4000, false, rung, 1080))
    }

    @Test fun normalSegmentVariationAllowsAnUpgrade() {
        val auto = NativeAutoQuality()
        assertNull(auto.observe(4900, false, 720, 1080))
        assertNull(auto.observe(3200, false, 720, 1080))
        assertEquals(1080, auto.observe(4100, false, 720, 1080))
    }

    @Test fun bufferedSamplesStepDownFasterThanTheyStepUp() {
        val auto = NativeAutoQuality()
        assertNull(auto.observe(2000, false, 360, 2160))
        assertEquals(240, auto.observe(2000, false, 360, 2160))
        auto.reset()
        repeat(2) { assertNull(auto.observe(5000, false, 360, 2160)) }
        assertEquals(480, auto.observe(5000, false, 360, 2160))
        repeat(3) { assertNull(auto.observe(5000, false, 360, 360)) }
    }
}
