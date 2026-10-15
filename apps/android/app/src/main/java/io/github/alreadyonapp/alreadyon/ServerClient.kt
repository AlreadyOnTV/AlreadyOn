package io.github.alreadyonapp.alreadyon

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resumeWithException

/** API failure exposes only a safe summary, never a credential-bearing request URL. */
internal class ServerFailure(val status: Int, message: String, val sessionExpired: Boolean = false, val code: String = "") : IOException(message)

/** Exact-origin cookies persist encrypted until server expiry or rejection; local server detachment retains them. */
private class SessionCookies(private val origin: HttpUrl, private val store: SessionStore) : CookieJar {
    private fun accepted(cookie: Cookie) = cookie.secure && cookie.httpOnly && cookie.hostOnly &&
        cookie.path == "/" && cookie.matches(origin) && cookie.expiresAt > System.currentTimeMillis()
    private val stored = store.read(origin.toString().removeSuffix("/"))
        .mapNotNull { Cookie.parse(origin, it) }.filter(::accepted).toMutableList()
    private fun persist() = store.write(origin.toString().removeSuffix("/"), stored.map { it.toString() })
    @Synchronized override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        if (!sameOrigin(origin, url)) return
        cookies.filter { it.secure && it.httpOnly && it.hostOnly && it.path == "/" }.forEach { cookie ->
            stored.removeAll { it.name == cookie.name }
            if (accepted(cookie)) stored.add(cookie)
        }
        persist()
    }
    @Synchronized override fun loadForRequest(url: HttpUrl): List<Cookie> {
        if (stored.removeAll { it.expiresAt <= System.currentTimeMillis() }) persist()
        return if (sameOrigin(origin, url)) stored.filter { it.matches(url) } else emptyList()
    }
    @Synchronized fun clear() { stored.clear(); persist() }
    @Synchronized fun hasSession() = loadForRequest(origin).isNotEmpty()
}

/** Exact scheme/host/port comparison prevents cookies or scoped tokens crossing servers. */
internal fun sameOrigin(a: HttpUrl, b: HttpUrl): Boolean =
    a.scheme == b.scheme && a.host == b.host && a.port == b.port

/** HTTPS API client using server sessions and the existing same-origin write contract. */
internal class ServerClient(val origin: String, store: SessionStore, trustedCa: String? = null) {
    private val base = origin.toHttpUrl()
    private val cookies = SessionCookies(base, store)
    private val http = OkHttpClient.Builder().serverTls(base, trustedCa).cookieJar(cookies)
        .followRedirects(false).followSslRedirects(false)
        .connectTimeout(15, TimeUnit.SECONDS).readTimeout(90, TimeUnit.SECONDS)
        .callTimeout(120, TimeUnit.SECONDS).build()

    fun clearSession() = cookies.clear()
    fun hasSession() = cookies.hasSession()

    suspend fun request(path: String, method: String = "GET", body: JSONObject? = null, upload: ByteArray? = null): String =
        withContext(Dispatchers.IO) {
            require((path.startsWith("/api/") && !path.startsWith("//")) || path == "/healthz")
            val url = base.resolve(path) ?: throw IOException("Invalid server route.")
            require(sameOrigin(base, url))
            val payload = if (method == "GET") null else
                if (upload != null) okhttp3.MultipartBody.Builder().setType(okhttp3.MultipartBody.FORM)
                    .addFormDataPart("file", "avatar.png", upload.toRequestBody("image/png".toMediaType())).build()
                else (body ?: JSONObject()).toString().toRequestBody("application/json".toMediaType())
            val call = http.newCall(Request.Builder().url(url).method(method, payload)
                .header("Origin", origin).header("Accept", "application/json")
                .header("User-Agent", "AlreadyOn-Android/${if (BuildConfig.IS_TV) "TV" else "Mobile"}").build())
            val response = suspendCancellableCoroutine<Response> { continuation ->
                continuation.invokeOnCancellation { call.cancel() }
                call.enqueue(object : Callback {
                    override fun onFailure(call: Call, e: IOException) {
                        if (continuation.isActive) continuation.resumeWithException(e)
                    }
                    override fun onResponse(call: Call, response: Response) {
                        continuation.resume(response) { _, value, _ -> value.close() }
                    }
                })
            }
            response.use {
                // Guides are bounded to 12 hours; reject oversized responses before allocating JSON.
                val source = it.body?.source()
                if (source?.request(8L * 1024 * 1024 + 1) == true) {
                    throw IOException("Server response is too large.")
                }
                val text = source?.readUtf8().orEmpty()
                if (!it.isSuccessful) {
                    val code = runCatching { JSONObject(text).optString("code") }.getOrDefault("")
                    val message = when (code) {
                        "password_change_required" -> "Change your password before continuing."
                        "password_locked" -> "This account’s username and password are locked."
                        "username_taken" -> "That username is already in use."
                        "stream_limit" -> "All simultaneous streams for this account are in use. Stop another stream and try again."
                        "stream_capacity" -> "Server playback capacity is unavailable. Try again shortly."
                        else -> when (it.code) {
                        401 -> if (path == "/api/auth/tv-login") "Username or TV PIN was not accepted." else "Sign-in or profile PIN was not accepted."
                        403 -> "The server refused this request."
                        409 -> "Server setup or schedule changed. Try again."
                        422 -> "This channel cannot play with the phone’s supported formats."
                        429 -> "Too many attempts. Retry after ${it.header("Retry-After") ?: "a few"} seconds."
                        503 -> "Server playback capacity is unavailable. Try again shortly."
                        else -> "Server request failed (${it.code})."
                    }
                    }
                    val expired = it.code == 401 && path != "/api/auth/login" && path != "/api/auth/tv-login" && !path.endsWith("/verify-pin")
                    if (expired) clearSession()
                    throw ServerFailure(it.code, message, sessionExpired = expired, code = code)
                }
                text
            }
        }

    suspend fun profiles(): List<ViewerProfile> {
        val data = JSONArray(request("/api/profiles"))
        return List(data.length()) { index ->
            val p = data.getJSONObject(index)
            ViewerProfile(p.getLong("id"), p.getString("name"), p.getString("audienceTier"),
                p.optBoolean("hasPin"), p.optString("audioLanguage", "en").takeUnless { it == "null" } ?: "en",
                p.optString("icon").takeUnless { it.isBlank() || it == "null" }, p.optString("role", "viewer"),
                p.optLong("lastWatchedChannelId", -1), p.optString("guideTimeFormat", "12h").takeUnless { it == "null" } ?: "12h",
                p.optBoolean("subtitlesEnabled"), p.optString("subtitleLanguage", "en").takeUnless { it == "null" } ?: "en", p.optString("subtitleMode").takeUnless { it.isBlank() || it == "null" } ?: if (p.optBoolean("subtitlesEnabled")) "always" else "off")
        }
    }

    suspend fun guide(profile: ViewerProfile): List<GuideChannel> = withContext(Dispatchers.Default) {
        val tier = when (profile.tier) { "young_child", "teen", "adult" -> profile.tier; else -> "young_child" }
        val data = JSONArray(request("/api/guide?hours=12&audienceTier=$tier"))
        List(data.length()) { index ->
            val c = data.getJSONObject(index)
            fun readPrograms(key: String): List<GuideProgram> {
                val programs = c.optJSONArray(key) ?: JSONArray()
                val visible = List(programs.length()) { programs.getJSONObject(it) }
                    .filter { !BuildConfig.IS_TV || it.optString("kind") != "commercial" }.map { p ->
                        GuideProgram(guideTitle(if (p.optString("mediaType") == "movie") p.getString("title") else p.optString("seriesTitle").takeUnless { it.isBlank() || it == "null" } ?: p.getString("title")), p.getString("startsAt"), p.getString("endsAt"),
                            episodeTitle = guideTitle(p.optString("title")), seasonNumber = p.optInt("seasonNumber"), episodeNumber = p.optInt("episodeNumber"),
                            overview = p.optString("overview"), year = p.optInt("year"), rating = p.optString("contentRating"), airDate = p.optString("airDate"),
                            mediaFileId = p.optLong("mediaFileId", -1), sourceOffset = p.optDouble("sourceOffsetSeconds"))
                    }
                // An interrupted show is one guide stop; a fresh airing of the same file remains separate.
                val cells = if (!BuildConfig.IS_TV) visible else buildList {
                    visible.forEach { program ->
                        val previous = lastOrNull()
                        if (previous != null && program.mediaFileId >= 0 && previous.mediaFileId == program.mediaFileId && program.sourceOffset > 0) {
                            set(lastIndex, previous.copy(endsAt = program.endsAt))
                        } else add(program)
                    }
                }
                return cells
            }
            GuideChannel(c.getLong("channelId"), c.getInt("number"), c.getString("name"), readPrograms("programs"), readPrograms("previousPrograms"))
        }.sortedBy { it.number }
    }

    /** Uploaded avatars use only the server's image route, never third-party credential-bearing URLs. */
    suspend fun profileIcon(path: String): android.graphics.Bitmap? = withContext(Dispatchers.IO) {
        val url = base.resolve(path) ?: return@withContext null
        if (!path.startsWith("/profile-icons/") || !sameOrigin(base, url) || url.query != null || url.fragment != null) return@withContext null
        http.newCall(Request.Builder().url(url).build()).execute().use { response ->
            if (!response.isSuccessful) return@withContext null
            val source = response.body?.source() ?: return@withContext null
            if (source.request(1024L * 1024 + 1)) return@withContext null
            val bytes = source.readByteArray()
            val bounds = android.graphics.BitmapFactory.Options().apply { inJustDecodeBounds = true }
            android.graphics.BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
            if (bounds.outWidth !in 1..2048 || bounds.outHeight !in 1..2048) return@withContext null
            val options = android.graphics.BitmapFactory.Options().apply { inSampleSize = (maxOf(bounds.outWidth, bounds.outHeight) / 128).coerceAtLeast(1) }
            android.graphics.BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options)
        }
    }

    /** Validate even server-supplied URLs before giving any token to the media transport. */
    fun mediaURL(raw: String, channel: Long): String {
        val url = raw.toHttpUrl()
        if (!sameOrigin(base, url) || url.username.isNotEmpty() || url.password.isNotEmpty() ||
            !url.encodedPath.startsWith("/media/$channel/")) {
            throw IOException("The server returned an unexpected media address.")
        }
        return url.toString()
    }
}

internal data class ViewerProfile(val id: Long, val name: String, val tier: String, val hasPin: Boolean, val language: String, val icon: String?, val role: String = "viewer",
    val lastChannel: Long = -1, val timeFormat: String = "12h", val subtitles: Boolean = false, val subtitleLanguage: String = "en", val subtitleMode: String = if (subtitles) "always" else "off")
internal data class GuideProgram(val title: String, val startsAt: String, val endsAt: String,
    val episodeTitle: String = "", val seasonNumber: Int = 0, val episodeNumber: Int = 0,
    val overview: String = "", val year: Int = 0, val rating: String = "", val airDate: String = "",
    val mediaFileId: Long = -1, val sourceOffset: Double = 0.0)
internal data class GuideChannel(val id: Long, val number: Int, val name: String, val programs: List<GuideProgram>, val previousPrograms: List<GuideProgram> = emptyList())

/** Match web display cleanup without changing server metadata or source filenames. */
private fun guideTitle(title: String) = title.replace(Regex("\\[[^\\]]*\\]"), "")
    .replace(Regex("\\(\\d{4}\\)"), "").replace(Regex("\\s+"), " ").trim()
