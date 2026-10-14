// Shared phone/TV navigation must wrap without retuning a lone playable channel.
package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class ChannelNavigationTest {
    private val program = GuideProgram("Synthetic", "2026-10-13T00:00:00Z", "2026-10-14T00:00:00Z")
    // Deliberately non-numeric order: follow the visible list, not a second sort.
    private val channels = listOf(GuideChannel(1, 120, "First", listOf(program)),
        GuideChannel(2, 110, "Empty", emptyList()), GuideChannel(3, 105, "Last", listOf(program)))
    @Test fun wrapsBothWaysInVisibleOrder() {
        assertEquals(1L, nextPlayableChannel(channels, 3, 1)?.id)
        assertEquals(3L, nextPlayableChannel(channels, 1, -1)?.id)
        assertEquals(3L, nextPlayableChannel(channels, 1, 1)?.id)
    }
    @Test fun emptyAndSingleListsNeverRetune() {
        assertNull(nextPlayableChannel(emptyList(), 1, 1))
        assertNull(nextPlayableChannel(listOf(channels[1]), 1, -1))
        assertNull(nextPlayableChannel(listOf(channels[0]), 1, 1))
        assertEquals(1L, nextPlayableChannel(channels, 99, 1)?.id)
        assertEquals(3L, nextPlayableChannel(channels, 99, -1)?.id)
    }
}
