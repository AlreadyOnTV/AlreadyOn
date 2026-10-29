package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test
import java.time.Instant

// Synthetic UTC schedules exercise midnight and non-slot program boundaries.
class GuideTimelineTest {
    private fun stamp(value: String) = Instant.parse(value).toEpochMilli()
    private val before = GuideProgram("Before midnight", "2026-10-24T23:20:00Z", "2026-10-25T00:05:00Z")
    private val after = GuideProgram("After midnight", before.endsAt, "2026-10-25T01:20:00Z")
    private val channel = GuideChannel(115, 115, "Synthetic", listOf(before, after))

    @Test fun airingAdvancesAtProgramBoundaryNotSlotBoundary() {
        assertEquals(before, channel.liveProgram(stamp("2026-10-25T00:00:00Z")))
        assertEquals(before, channel.liveProgram(stamp("2026-10-25T00:04:59.999Z")))
        assertEquals(after, channel.liveProgram(stamp(after.startsAt)))
        assertNull(channel.liveProgram(stamp(after.endsAt)))
    }

    @Test fun slotsCatchUpAcrossHourAndMidnightWithoutChangingSchedule() {
        val start = stamp("2026-10-24T23:29:59.999Z")
        assertEquals(stamp("2026-10-24T23:00:00Z"), guideHalfHour(start))
        val returned = start + 3_600_001L
        assertEquals(stamp("2026-10-25T00:30:00Z"), guideHalfHour(returned))
        assertEquals(after, channel.liveProgram(returned))
        assertEquals("2026-10-24T23:20:00Z", before.startsAt)
    }

    @Test fun timerWakesAtExactSlotAndProgramBoundaries() {
        assertEquals(1L, guideRefreshDelay(listOf(channel), stamp("2026-10-24T23:29:59.999Z")))
        assertEquals(1L, guideRefreshDelay(listOf(channel), stamp("2026-10-25T00:04:59.999Z")))
        assertEquals(60_000L, guideRefreshDelay(listOf(channel), stamp(after.startsAt)))
        assertEquals(60_000L, guideRefreshDelay(emptyList(), stamp(after.endsAt)))
        val short = channel.copy(programs = listOf(before.copy(endsAt = "2026-10-24T23:20:08Z")))
        assertEquals(8_000L, guideRefreshDelay(listOf(short), stamp(before.startsAt)))
    }

    @Test fun navigationUsesTwoMinutesFromLatestMonotonicInput() {
        val first = 5_000L + GUIDE_NAVIGATION_PAUSE_MS
        assertEquals(120_000L, guideNavigationRemaining(first, 5_000L))
        assertEquals(1L, guideNavigationRemaining(first, 124_999L))
        assertEquals(0L, guideNavigationRemaining(first, 125_000L))
        val repeated = 124_999L + GUIDE_NAVIGATION_PAUSE_MS
        assertEquals(119_999L, guideNavigationRemaining(repeated, 125_000L))
        assertEquals(0L, guideNavigationRemaining(repeated, 245_000L))
        assertEquals(0L, guideNavigationRemaining(0, 5_000L))
    }
}
