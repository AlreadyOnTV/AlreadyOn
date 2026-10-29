// Exercise short release URLs while preserving signed historical package metadata.
package io.github.alreadyonapp.alreadyon

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.security.MessageDigest

/** Synthetic responses/APKs only; no production release or account state is modified. */
@RunWith(AndroidJUnit4::class)
class UpdateClientTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val bytes = "synthetic APK download".toByteArray()
    private fun hash(value: ByteArray) = MessageDigest.getInstance("SHA-256").digest(value).joinToString("") { "%02x".format(it) }
    private fun entry(tag: String, code: Int, draft: Boolean = false, preview: Boolean = true): JSONObject {
        val root = "https://github.com/$UPDATE_REPOSITORY/releases/download/$tag/"
        return JSONObject().put("tag_name", tag).put("draft", draft).put("prerelease", preview)
            .put("body", "Android versionCode: `$code`")
            .put("assets", JSONArray().put(JSONObject().put("name", "android-release.json").put("browser_download_url", root + "android-release.json"))
                .put(JSONObject().put("name", "alreadyon-tv.apk").put("browser_download_url", root + "alreadyon-tv.apk").put("size", bytes.size))
                .put(JSONObject().put("name", "alreadyon-android.apk").put("browser_download_url", root + "alreadyon-android.apk").put("size", bytes.size)))
    }
    private fun manifest(code: Int, name: String) = JSONObject().put("package", UPDATE_PACKAGE).put("version_code", code).put("version_name", name)
        .put("artifacts", JSONObject().put("tv", JSONObject().put("file", "alreadyon-tv.apk").put("sha256", hash(bytes)))
            .put("phone", JSONObject().put("file", "alreadyon-android.apk").put("sha256", hash(bytes))))
    private fun service(respond: (String) -> String) = GitHubUpdates(OkHttpClient.Builder().addInterceptor { chain ->
        Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
            .body(respond(chain.request().url.toString()).toResponseBody("application/json".toMediaType())).build()
    }.build())

    @Test fun paginatedReleasesChooseVersionCodeAndCorrectTarget() = runBlocking {
        val pageOne = JSONArray()
        repeat(29) { pageOne.put(entry("26.10.10-alpha1", 23, draft = true)) }
        pageOne.put(entry("26.10.12-alpha1", 24))
        val pageTwo = JSONArray().put(entry("26.10.11-alpha1", 25))
        val checker = service { url -> when {
            url.contains("page=1") -> pageOne.toString()
            url.contains("page=2") -> pageTwo.toString()
            url.contains("26.10.12") -> manifest(24, "26.10.12-alpha1").toString()
            else -> manifest(25, "26.10.11-alpha1").toString()
        } }
        val tv = checker.check("26.10.10-alpha1", 23, true, cycle = ReleaseCycle.NIGHTLY)!!
        assertEquals(25L, tv.code); assertTrue(tv.url.endsWith("alreadyon-tv.apk"))
        val phone = checker.check("26.10.10-alpha1", 23, false, cycle = ReleaseCycle.NIGHTLY)!!
        assertTrue(phone.url.endsWith("alreadyon-android.apk"))
        assertNull(checker.check("26.10.10-Stable", 23, true))
        assertEquals(25L, checker.check("26.10.10-Stable", 23, true, cycle = ReleaseCycle.NIGHTLY)!!.code)
        assertNull(checker.check("26.10.11-alpha1", 25, true, cycle = ReleaseCycle.NIGHTLY))
    }
    @Test fun selectedCycleControlsPhoneAndTvRegardlessOfStableCounterparts() = runBlocking {
        val checker = service { url -> when {
            url.contains("api.github.com") -> JSONArray().put(entry("26.10.10-Stable", 23, preview = false))
                .put(entry("26.10.11-Stable", 24, preview = false)).put(entry("26.10.12-rc1", 25))
                .put(entry("26.10.13-nightly", 26)).toString()
            url.contains("26.10.10") -> manifest(23, "26.10.10-alpha1").toString()
            url.contains("26.10.12") -> manifest(25, "26.10.12-rc1").toString()
            url.contains("26.10.13") -> manifest(26, "26.10.13-nightly").toString()
            else -> manifest(24, "26.10.11-alpha1").toString()
        } }
        for (tv in listOf(false, true)) {
            for (installed in listOf("26.10.10-alpha1", "26.10.10-Stable", "26.10.10-nightly")) {
                assertEquals(24L, checker.check(installed, 23, tv)!!.code)
                assertEquals(25L, checker.check(installed, 23, tv, cycle = ReleaseCycle.RELEASE_CANDIDATE)!!.code)
                val update = checker.check(installed, 23, tv, cycle = ReleaseCycle.NIGHTLY)!!
                assertEquals(26L, update.code)
                assertEquals("26.10.13-nightly", update.release)
                assertNull(checker.check(installed, 27, tv, cycle = ReleaseCycle.NIGHTLY))
            }
        }
    }
    @Test fun missingTargetMetadataIsSkipped() = runBlocking {
        val checker = service { url -> if (url.contains("api.github.com")) JSONArray().put(entry("26.10.11-alpha1", 25)).toString()
            else manifest(25, "26.10.11-alpha1").apply { remove("artifacts") }.toString() }
        assertNull(checker.check("26.10.10-alpha1", 23, true, cycle = ReleaseCycle.NIGHTLY))
    }
    @Test fun corruptAndTruncatedDownloadsAreRemoved() = runBlocking {
        val checker = service { String(bytes) }
        val folder = File(context.cacheDir, "synthetic-update-test").apply { mkdirs() }
        val file = File(folder, "update.apk")
        val update = AppUpdate("26.10.11-alpha1", "26.10.11-alpha1", 24, "tv",
            "https://github.com/$UPDATE_REPOSITORY/releases/download/26.10.11-alpha1/alreadyon-tv.apk", hash(bytes), bytes.size.toLong())
        try {
            checker.download(update, file) { }
            assertArrayEquals(bytes, file.readBytes())
            for (bad in listOf(update.copy(hash = "0".repeat(64)), update.copy(size = bytes.size + 1L))) {
                try { checker.download(bad, file) { }; fail("Corrupt download accepted") } catch (_: IllegalStateException) { }
                assertFalse(file.exists())
            }
        } finally { folder.deleteRecursively() }
    }
    @Test fun actualSignedApkChecksTargetVersionAndPackage() {
        val store = UpdateInstallStore(context)
        try {
            InstrumentationRegistry.getInstrumentation().context.assets.open(if (BuildConfig.IS_TV) "update-tv-test.apk" else "update-phone-test.apk").use { input -> store.apk.outputStream().use { input.copyTo(it) } }
            val update = AppUpdate("test", "26.10.11-alpha5", 24, if (BuildConfig.IS_TV) "tv" else "phone",
                "https://github.com/$UPDATE_REPOSITORY/releases/download/test/a.apk", "0".repeat(64), store.apk.length())
            verifyUpdateApk(context, store.apk, update)
            for (bad in listOf(update.copy(target = if (BuildConfig.IS_TV) "phone" else "tv"), update.copy(code = 23), update.copy(version = "wrong"))) {
                try { verifyUpdateApk(context, store.apk, bad); fail("Incorrect APK metadata accepted") } catch (_: IllegalStateException) { }
            }
        } finally { store.clear() }
    }
    @Test fun stageAndCancelPreserveUnrelatedAppSettings() = runBlocking {
        val store = UpdateInstallStore(context)
        val unrelated = context.getSharedPreferences("synthetic-update-settings", 0)
        unrelated.edit().putString("server", "https://synthetic.invalid").commit()
        try {
            InstrumentationRegistry.getInstrumentation().context.assets.open(if (BuildConfig.IS_TV) "update-tv-test.apk" else "update-phone-test.apk").use { input -> store.apk.outputStream().use { input.copyTo(it) } }
            val update = AppUpdate("test", "26.10.11-alpha5", 24, if (BuildConfig.IS_TV) "tv" else "phone", "", "", store.apk.length())
            val id = stageUpdate(context, update)
            assertEquals(id, store.session()!!.sessionId)
            assertFalse(store.completeIfInstalled())
            assertTrue(store.apk.exists())
            store.fail("Synthetic cancellation")
            assertNull(context.packageManager.packageInstaller.getSessionInfo(id))
            assertFalse(store.apk.exists())
            assertEquals("https://synthetic.invalid", unrelated.getString("server", null))
        } finally { store.clear(); unrelated.edit().clear().commit() }
    }

    @Test fun identicalPromotionsSelectLatestStageAndStopAfterAcceptance() = runBlocking {
        val checker = service { url -> when {
            url.contains("api.github.com") -> JSONArray().put(entry("26.10.11-alpha4", 24))
                .put(entry("26.10.11-RC9", 24)).put(entry("26.10.11-RC10", 24)).toString()
            else -> manifest(24, "26.10.11-alpha4").toString()
        } }
        for (tv in listOf(false, true)) {
            val update = checker.check("26.10.11-alpha4", 24, tv, installedHash = hash(bytes), cycle = ReleaseCycle.RELEASE_CANDIDATE)!!
            assertEquals("26.10.11-RC10", update.release)
            assertNull(checker.check("26.10.11-alpha4", 24, tv, update.release, hash(bytes), ReleaseCycle.RELEASE_CANDIDATE))
            assertNull(checker.check("26.10.11-alpha4", 24, tv, installedHash = "0".repeat(64), cycle = ReleaseCycle.RELEASE_CANDIDATE))
        }
        val stable = service { url -> if (url.contains("api.github.com"))
            JSONArray().put(entry("26.10.11-Stable", 24, preview = false)).toString()
            else manifest(24, "26.10.11-alpha4").toString() }
        assertEquals("26.10.11-Stable", stable.check("26.10.11-alpha4", 24, false,
            "26.10.11-RC10", hash(bytes))!!.release)
    }
    @Test fun acceptedReleaseSurvivesInstallerCleanupAndRejectsDifferentApks() {
        val prefs = context.getSharedPreferences("accepted-release", 0)
        val accepted = AcceptedRelease(context)
        val embedded = context.packageManager.getPackageInfo(context.packageName, 0).versionName.orEmpty()
        val hash = installedApkHash(context)
        val update = AppUpdate("26.10.11-RC1", embedded, installedVersionCode(context),
            if (BuildConfig.IS_TV) "tv" else "phone", "", hash, 1)
        try {
            assertEquals(embedded, accepted.current(hash))
            accepted.accept(update)
            UpdateInstallStore(context).clear()
            assertEquals("26.10.11-RC1", AcceptedRelease(context).current(hash))
            accepted.accept(update.copy(release = "26.10.11-Stable"))
            assertEquals("26.10.11-Stable", AcceptedRelease(context).current(hash))
            assertEquals(embedded, AcceptedRelease(context).current("different APK"))
            try { accepted.accept(update.copy(hash = "0".repeat(64))); fail("Different APK accepted") }
            catch (_: IllegalStateException) { }
            assertEquals("26.10.11-Stable", prefs.getString("release", null))
        } finally { prefs.edit().clear().commit() }
    }
}
