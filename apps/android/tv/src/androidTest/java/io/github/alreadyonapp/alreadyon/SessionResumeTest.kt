package io.github.alreadyonapp.alreadyon

import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test

/** Cached sessions and remote profile changes against a disposable server, never live revocation. */
class SessionResumeTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    @Test fun cachedReconnectProfileRefreshAndWebRevocation() {
        val args = InstrumentationRegistry.getArguments()
        val port = args.getString("serverPort")?.toInt() ?: error("Use the isolated test-tv.py fixture")
        val origin = "https://10.0.2.2:$port"
        compose.mainClock.advanceTimeBy(3000)
        val bootstrap = OkHttpClient.Builder().bootstrapTls(origin.toHttpUrl()).build()
        val certificate = bootstrap.newCall(Request.Builder().url("$origin/api/tv/trust").build()).execute().use {
            check(it.isSuccessful)
            JSONObject(it.body!!.string()).getString("certificate")
        }
        val model = ViewModelProvider(compose.activity)[PhoneModel::class.java]
        compose.runOnIdle { model.signInWithPIN(origin, "tv-fixture", "123456", certificate) }
        compose.waitUntil(15000) { model.state.value.signedIn && !model.state.value.busy }
        compose.runOnIdle { model.logout() }
        compose.waitUntil(15000) { !model.state.value.signedIn && !model.state.value.busy }
        assertTrue(SessionStore(compose.activity).read(origin).isNotEmpty())
        compose.runOnIdle { model.reconnect() }
        compose.waitUntil(10000) { !model.state.value.busy }
        assertFalse("Startup/reconnect stays on server selection after local sign-out", model.state.value.signedIn)
        assertFalse("A different origin cannot reuse the session", runBlocking { model.resumeSavedServer("https://10.0.2.3:$port") })
        compose.onNode(hasText("Recently connected", substring = true)).performClick()
        compose.waitUntil(15000) { model.state.value.signedIn && model.state.value.profiles.isNotEmpty() }
        val selected = model.state.value.profiles.first()
        compose.runOnIdle { model.selectProfile(selected, "") }
        compose.waitUntil(15000) { model.state.value.profile != null && !model.state.value.busy }

        // A separate in-memory WebUI session exercises server revocation without replacing native cookies.
        val jar = object : CookieJar {
            val cookies = mutableListOf<Cookie>()
            override fun saveFromResponse(url: HttpUrl, received: List<Cookie>) {
                received.forEach { cookie -> cookies.removeAll { it.name == cookie.name }; cookies.add(cookie) }
            }
            override fun loadForRequest(url: HttpUrl) = cookies.filter { it.matches(url) }
        }
        val web = OkHttpClient.Builder().serverTls(origin.toHttpUrl(), certificate).cookieJar(jar).build()
        fun request(path: String, body: JSONObject, method: String = "POST") {
            web.newCall(Request.Builder().url(origin + path).header("Origin", origin)
                .method(method, body.toString().toRequestBody("application/json".toMediaType())).build()).execute().use {
                assertTrue("Synthetic WebUI request $path: ${it.code}", it.isSuccessful)
            }
        }
        request("/api/auth/login", JSONObject().put("username", "tv-fixture").put("password", "synthetic-tv-fixture-pass"))
        request("/api/profiles/${selected.id}", JSONObject().put("name", "Remote Profile").put("icon", "owl").put("guideTimeFormat", "24h"), "PATCH")
        compose.runOnIdle { model.refreshProfiles() }
        compose.waitUntil(15000) { model.state.value.profile?.name == "Remote Profile" }
        assertEquals("owl", model.state.value.profile?.icon)
        assertEquals("24h", model.state.value.profile?.timeFormat)
        compose.onNodeWithText("Remote Profile").assertExists()

        val bitmap = android.graphics.Bitmap.createBitmap(8, 8, android.graphics.Bitmap.Config.ARGB_8888)
        bitmap.eraseColor(android.graphics.Color.BLUE)
        val bytes = java.io.ByteArrayOutputStream().also { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }.toByteArray()
        val uploadedIcon = web.newCall(Request.Builder().url("$origin/api/profiles/${selected.id}/icon").header("Origin", origin)
            .post(okhttp3.MultipartBody.Builder().setType(okhttp3.MultipartBody.FORM)
                .addFormDataPart("file", "avatar.png", bytes.toRequestBody("image/png".toMediaType())).build()).build()).execute().use {
                assertTrue(it.isSuccessful)
                JSONObject(it.body!!.string()).getString("icon")
            }
        compose.waitUntil(15000) { !model.state.value.busy }
        compose.runOnIdle { model.refreshProfiles() }
        compose.waitUntil(15000) { model.state.value.profile?.icon == uploadedIcon }
        assertNotNull("The refreshed uploaded avatar is readable by the other device", runBlocking { model.profileIcon(uploadedIcon) })

        compose.waitUntil(15000) { !model.state.value.busy }
        compose.runOnIdle { model.logout() }
        compose.waitUntil(15000) { !model.state.value.signedIn && !model.state.value.busy }
        request("/api/auth/sessions/revoke", JSONObject().put("scope", "others"))
        assertFalse("WebUI revocation rejects the cached native session", runBlocking { model.resumeSavedServer(origin) })
        assertTrue("Rejected cookies are removed", SessionStore(compose.activity).read(origin).isEmpty())
        compose.onNode(hasText("Recently connected", substring = true)).performClick()
        compose.waitUntil(15000) { compose.onAllNodesWithText("Username").fetchSemanticsNodes().isNotEmpty() }
        assertFalse(model.state.value.signedIn)
        compose.runOnIdle { model.signInWithPIN(origin, "tv-fixture", "123456", certificate) }
        compose.waitUntil(15000) { model.state.value.signedIn && !model.state.value.busy }
        compose.runOnIdle { model.logout() }
        compose.waitUntil(15000) { !model.state.value.signedIn && !model.state.value.busy }
        request("/api/auth/sessions/revoke", JSONObject().put("scope", "all"))
        assertFalse("Sign Out Everywhere also rejects cached native sessions", runBlocking { model.resumeSavedServer(origin) })
        assertTrue(SessionStore(compose.activity).read(origin).isEmpty())
    }
}
