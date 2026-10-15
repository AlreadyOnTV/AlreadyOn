package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class PlayerMenuStatusTest {
    @Test fun activeTracksAndMissingMetadata() {
        val media = PhonePlayback("", "", 0, 1, mediaFileId = 9, capturedAt = 0, selectedAudioIndex = 2,
            audioTracks = listOf(PhoneTrack(2, "English", "eng")),
            subtitleTracks = listOf(PhoneTrack(4, "Spanish", "spa")))
        val state = PhoneState(playback = media, subtitleIndex = 4, subtitleFileId = 9)
        assertEquals("Quality (auto)", playerMenuLabel("Quality", state))
        assertEquals("Audio Tracks (eng)", playerMenuLabel("Audio Tracks", state))
        assertEquals("Subtitles (on)", playerMenuLabel("Subtitles", state))
        assertEquals("Subtitles (off)", playerMenuLabel("Subtitles", state.copy(subtitleFileId = 8)))
        assertEquals("Audio Tracks (unknown)", playerMenuLabel("Audio Tracks", state.copy(playback = media.copy(selectedAudioIndex = 3))))
        assertEquals("Quality (720p)", playerMenuLabel("Quality", state.copy(quality = 720)))
        assertEquals("unknown", playerLanguage("und"))
        assertEquals("unknown", playerLanguage("English"))
        assertEquals("en", playerLanguage("en-US"))
    }
    @Test fun halfHourBoundaryAndExtendedAbsence() {
        assertEquals(0L, guideHalfHour(1_799_999))
        assertEquals(1_800_000L, guideHalfHour(1_800_000))
        assertEquals(9_000_000L, guideHalfHour(10_799_999))
    }
}
