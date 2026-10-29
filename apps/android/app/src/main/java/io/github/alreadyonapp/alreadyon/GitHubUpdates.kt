package io.github.alreadyonapp.alreadyon

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.security.MessageDigest
import java.util.concurrent.TimeUnit

/** This client never inherits LAN certificate exceptions, cookies or server credentials. */
private fun updateHttpClient() = OkHttpClient.Builder().connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS).callTimeout(5, TimeUnit.MINUTES)
        .addNetworkInterceptor { chain ->
            val url = chain.request().url
            check(url.scheme == "https" && url.username.isEmpty() && url.password.isEmpty() &&
                url.port == 443 && url.host in setOf("api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com")) {
                "Unexpected update download destination."
            }
            chain.proceed(chain.request())
        }.build()

internal class GitHubUpdates(private val client: OkHttpClient = updateHttpClient()) {
    fun cancel() = client.dispatcher.cancelAll()

    private suspend fun json(url: String, limit: Long = 2 * 1024 * 1024): String = withContext(Dispatchers.IO) {
        currentCoroutineContext().ensureActive()
        val request = Request.Builder().url(url).header("Accept", "application/vnd.github+json")
            .header("User-Agent", "AlreadyOn-Android-Updater").build()
        client.newCall(request).execute().use { response ->
            check(response.isSuccessful) { "Update service is temporarily unavailable. Try again later." }
            val body = response.body ?: error("Empty update response.")
            check(body.contentLength() <= limit) { "Update response is too large." }
            body.byteStream().use { input ->
                val output = java.io.ByteArrayOutputStream()
                val buffer = ByteArray(8192)
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val read = input.read(buffer)
                    if (read < 0) break
                    check(output.size().toLong() + read <= limit) { "Update response is too large." }
                    output.write(buffer, 0, read)
                }
                output.toString("UTF-8")
            }
        }
    }

    suspend fun check(version: String, code: Long, tv: Boolean, release: String = version, installedHash: String? = null,
        cycle: ReleaseCycle = ReleaseCycle.STABLE): AppUpdate? {
        val releases = mutableListOf<JSONObject>()
        // Bound startup work while including prereleases and multiple pages of published history.
        for (page in 1..10) {
            val entries = JSONArray(json("https://api.github.com/repos/$UPDATE_REPOSITORY/releases?per_page=30&page=$page"))
            for (index in 0 until entries.length()) {
                val entry = entries.getJSONObject(index)
                if (acceptsUpdateRelease(version, entry.optString("tag_name"), entry.optBoolean("draft"), entry.optBoolean("prerelease"), cycle)) releases.add(entry)
            }
            if (entries.length() < 30) break
        }
        var newest: AppUpdate? = null
        val target = if (tv) "tv" else "phone"
        val assetName = if (tv) "alreadyon-tv.apk" else "alreadyon-android.apk"
        for (entry in releases.sortedByDescending { it.getString("tag_name").lowercase() }) {
            // The description is only a fetch optimization; manifest and APK verification are authoritative.
            val hint = Regex("Android versionCode: `(\\d+)`").find(entry.optString("body"))?.groupValues?.get(1)?.toLongOrNull()
            if (hint != null && hint < maxOf(code, newest?.code ?: 0)) continue
            val assets = entry.optJSONArray("assets") ?: continue
            fun asset(name: String): JSONObject? = (0 until assets.length()).map { assets.getJSONObject(it) }.singleOrNull { it.optString("name") == name }
            val descriptor = asset("android-release.json") ?: continue
            val apk = asset(assetName) ?: continue
            val manifestUrl = descriptor.optString("browser_download_url")
            if (!validUpdateAsset(manifestUrl)) continue
            val metadata = JSONObject(json(manifestUrl, 64 * 1024))
            if (metadata.optString("package") != UPDATE_PACKAGE) continue
            val artifact = metadata.optJSONObject("artifacts")?.optJSONObject(target) ?: continue
            if (artifact.optString("file") != assetName) continue
            val update = AppUpdate(entry.getString("tag_name"), metadata.getString("version_name"),
                metadata.getLong("version_code"), target, apk.getString("browser_download_url"),
                artifact.getString("sha256"), apk.getLong("size"))
            if ((update.code != code || update.version == version) &&
                eligibleUpdate(update, code, target, release, installedHash) &&
                (newest == null || update.code > newest.code ||
                    (update.code == newest.code && newerRelease(update.release, newest.release)))) newest = update
        }
        return newest
    }

    suspend fun download(update: AppUpdate, destination: File, progress: (Float) -> Unit) = withContext(Dispatchers.IO) {
        check(validUpdateAsset(update.url)) { "Unexpected update URL." }
        check(destination.parentFile!!.usableSpace > 2 * update.size + 16 * 1024 * 1024) { "Not enough storage for this update." }
        try {
            client.newCall(Request.Builder().url(update.url).header("User-Agent", "AlreadyOn-Android-Updater").build()).execute().use { response ->
                check(response.isSuccessful) { "Could not download the update." }
                val body = response.body ?: error("Empty APK download.")
                val digest = MessageDigest.getInstance("SHA-256")
                var downloaded = 0L
                body.byteStream().use { input -> destination.outputStream().use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        currentCoroutineContext().ensureActive()
                        val read = input.read(buffer)
                        if (read < 0) break
                        downloaded += read
                        check(downloaded <= update.size && downloaded <= MAX_UPDATE_BYTES) { "Unexpected APK size." }
                        digest.update(buffer, 0, read); output.write(buffer, 0, read)
                        progress(downloaded.toFloat() / update.size)
                    }
                } }
                check(downloaded == update.size && digest.digest().joinToString("") { "%02x".format(it) }.equals(update.hash, true)) {
                    "Update verification failed. Nothing was installed."
                }
            }
        } catch (error: Throwable) { destination.delete(); throw error }
    }
}
