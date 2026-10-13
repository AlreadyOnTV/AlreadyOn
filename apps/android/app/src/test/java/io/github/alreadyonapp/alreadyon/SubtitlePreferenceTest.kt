package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class SubtitlePreferenceTest {
    private val profile = ViewerProfile(1, "Viewer", "adult", false, "en", null, subtitleMode = "other_audio")
    private val subtitles = listOf(PhoneTrack(1, "English forced", "eng", true),
        PhoneTrack(2, "English", "en"), PhoneTrack(3, "Spanish", "spa"))
    private fun media(language: String, tracks: List<PhoneTrack> = subtitles) =
        PhonePlayback("https://example.invalid", "Title", 0, 1, capturedAt = 0,
            selectedAudioIndex = 5, audioTracks = listOf(PhoneTrack(5, "Audio", language)), subtitleTracks = tracks)

    @Test fun otherAudioUsesActiveAndPreferredLanguagesAndChosenCaptions() {
        for (language in listOf("ja", "jpn", "ES", "es-MX")) assertEquals(2, profileSubtitle(media(language), profile))
        assertEquals(3, profileSubtitle(media("eng"), profile.copy(language = "ja", subtitleLanguage = "es")))
        assertNull(profileSubtitle(media("jpn"), profile.copy(language = "ja")))
        assertNull(profileSubtitle(media("fre"), profile.copy(language = "fr")))
    }
    @Test fun matchingUnknownAndDisabledAudioDoNotEnableCaptions() {
        for (language in listOf("en", "eng", "EN-us", "und", "")) assertNull(profileSubtitle(media(language), profile))
        assertNull(profileSubtitle(media("jpn"), profile.copy(subtitleMode = "off")))
    }
    @Test fun missingTracksAndAlwaysOnAreHandled() {
        assertNull(profileSubtitle(media("jpn", emptyList()), profile))
        assertNull(profileSubtitle(media("jpn", listOf(subtitles[2])), profile))
        assertEquals(3, profileSubtitle(media("eng"), profile.copy(subtitleMode = "always", subtitleLanguage = "es")))
    }
    @Test fun activeAudioChangesReevaluateCaptionChoice() {
        assertEquals(listOf(null, 2, null), listOf("eng", "jpn", "eng").map { profileSubtitle(media(it), profile) })
    }
}
