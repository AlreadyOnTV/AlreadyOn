package io.github.alreadyonapp.alreadyon

import java.util.Locale

/** Only detected language codes belong in status rows; names and und are unknown. */
internal fun playerLanguage(code: String?): String {
    val language = code.orEmpty().trim().lowercase(Locale.ROOT).replace('_', '-').substringBefore('-')
    return language.takeIf { it.matches(Regex("[a-z]{2,3}")) && it !in listOf("und", "zxx", "mul") } ?: "unknown"
}

internal fun guideHalfHour(now: Long): Long = Math.floorDiv(now, 1_800_000L) * 1_800_000L

internal fun playerMenuLabel(label: String, state: PhoneState): String {
    val playback = state.playback
    val status = when (label) {
        "Quality" -> if (state.quality == 0) "auto" else if (state.quality == 2160) "4K" else "${state.quality}p"
        "Audio Tracks" -> playerLanguage(playback?.audioTracks?.find { it.index == playback.selectedAudioIndex }?.language)
        "Subtitles" -> if (state.subtitleIndex != null && state.subtitleFileId == playback?.mediaFileId &&
            playback?.subtitleTracks?.any { it.index == state.subtitleIndex } == true) "on" else "off"
        else -> return label
    }
    return "$label ($status)"
}
