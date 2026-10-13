package io.github.alreadyonapp.alreadyon

import org.json.JSONArray
import org.json.JSONObject
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.delay
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.util.concurrent.TimeUnit

// Source-file track indices are scoped to the current tune, never to another scheduled file.
internal data class PhoneTrack(val index: Int, val label: String, val language: String = "und", val forced: Boolean = false)
// Compare ISO-639 two/three-letter tags; stable sorting preserves source order within each language.
internal fun List<PhoneTrack>.preferredFirst(preference: String?): List<PhoneTrack> {
    fun language(code: String): String {
        val tag = code.trim().lowercase(java.util.Locale.ROOT).replace('_', '-').substringBefore('-')
        return runCatching { java.util.Locale.forLanguageTag(tag).isO3Language }.getOrDefault(tag)
    }
    val preferred = language(preference?.takeUnless { it.isBlank() || it == "und" } ?: "en")
    return sortedBy { track -> when (language(track.language)) { preferred -> 0; "eng" -> 1; else -> 2 } }
}
internal fun phoneTracks(data: JSONArray?): List<PhoneTrack> = if (data == null) emptyList() else List(data.length()) {
    val track = data.getJSONObject(it)
    PhoneTrack(track.getInt("index"), listOf(track.optString("language", "und"), track.optString("title"), track.optString("codec")).filter { value -> value.isNotBlank() }.joinToString(" · "), track.optString("language", "und"), track.optBoolean("forced"))
}
internal data class PhoneCue(val start: Double, val end: Double, val text: String)

/** Subtitle extraction uses only the current scoped token and origin; no management cookies or redirects. */
internal suspend fun subtitleCues(playback: PhonePlayback, channel: Long, index: Int): List<PhoneCue> = withContext(Dispatchers.IO) {
    val base = playback.url.toHttpUrl()
    require(base.encodedPath.startsWith("/media/$channel/"))
    val url = base.newBuilder().encodedPath("/media/$channel/subtitles/$index").query(null)
        .addQueryParameter("token", base.queryParameter("token") ?: throw IOException("Missing media token")).build()
    val http = OkHttpClient.Builder().serverTls(base, playback.trustedCa).followRedirects(false).followSslRedirects(false).callTimeout(10, TimeUnit.SECONDS).build()
    repeat(90) {
        val cues = http.newCall(Request.Builder().url(url).build()).execute().use { response ->
            if (response.code == 202) return@use null
            if (!response.isSuccessful) throw IOException("Subtitles unavailable")
            val source = response.body?.source() ?: throw IOException("Subtitles unavailable")
            if (source.request(8L * 1024 * 1024 + 1)) throw IOException("Subtitles too large")
            val array = JSONObject(source.readUtf8()).getJSONArray("cues")
            List(array.length()) { i -> val cue = array.getJSONObject(i); PhoneCue(cue.getDouble("s"), cue.getDouble("e"), cue.getString("t")) }
        }
        if (cues != null) return@withContext cues
        delay(2000)
    }
    throw IOException("Subtitles preparation timed out")
}

/** Automatic captions use actual audio metadata and never guess an unknown language. */
internal fun profileSubtitle(media: PhonePlayback, profile: ViewerProfile): Int? {
    fun language(code: String): String {
        val tag = code.trim().lowercase(java.util.Locale.ROOT).replace('_', '-').substringBefore('-')
        val canonical = mapOf("fre" to "fra", "ger" to "deu", "chi" to "zho", "dut" to "nld", "cze" to "ces", "gre" to "ell")[tag] ?: tag
        return runCatching { java.util.Locale.forLanguageTag(canonical).isO3Language }.getOrDefault(canonical)
    }
    val audio = media.audioTracks.find { it.index == media.selectedAudioIndex }?.language?.let(::language)
    val enabled = profile.subtitleMode == "always" || (profile.subtitleMode == "other_audio" &&
        !audio.isNullOrBlank() && audio != "und" && audio != language(profile.language))
    if (!enabled) return null
    val matching = media.subtitleTracks.filter { language(it.language) == language(profile.subtitleLanguage) }
    return (matching.firstOrNull { !it.forced } ?: matching.firstOrNull())?.index
}
