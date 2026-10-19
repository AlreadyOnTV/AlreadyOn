package io.github.alreadyonapp.alreadyon

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class GuideClockTest {
    @Test fun formatsRespectProfileAndLocalMidnight() {
        val zone = ZoneId.of("America/New_York")
        val before = Instant.parse("2026-10-20T03:59:59Z").toEpochMilli()
        val midnight = before + 1000
        assertEquals("11:59pm", guideClockText(before, "12h", zone))
        assertEquals("12:00am", guideClockText(midnight, "12h", zone))
        assertEquals("00:00", guideClockText(midnight, "24h", zone))
        val morning = Instant.parse("2026-10-20T13:05:00Z").toEpochMilli()
        assertEquals("09:05am", guideClockText(morning, "12h-padded", zone))
        assertEquals("9:05am", guideClockText(morning, null, zone))
    }
}
