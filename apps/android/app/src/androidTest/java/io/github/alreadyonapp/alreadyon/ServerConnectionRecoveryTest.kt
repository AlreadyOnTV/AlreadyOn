package io.github.alreadyonapp.alreadyon

import android.app.Application
import android.content.Context
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map

// Staged recovery checks use a disposable server and namespaced device preferences.
@RunWith(AndroidJUnit4::class)
class ServerConnectionRecoveryTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    private class FixtureApplication(context: Context) : Application() {
        init { attachBaseContext(context) }
        override fun getSharedPreferences(name: String, mode: Int) =
            super.getSharedPreferences("recovery-100-$name", mode)
    }

    @Test fun savedConnectionRecovery() {
        val arguments = InstrumentationRegistry.getArguments()
        val scenario = arguments.getString("recoveryScenario")
        assumeTrue("Requires an isolated staged server fixture", scenario != null)
        val origin = requireNotNull(arguments.getString("recoveryOrigin"))
        val serverName = arguments.getString("recoveryServerName") ?: "Recovery Fixture"
        val username = arguments.getString("recoveryUsername") ?: "fixture"
        require(origin == "https://127.0.0.1:19950")
        val application = FixtureApplication(InstrumentationRegistry.getInstrumentation().targetContext)
        val preferences = application.getSharedPreferences("connection", Context.MODE_PRIVATE)
        if (scenario in setOf("login", "form", "cleanup")) {
            preferences.edit().clear().commit()
            application.getSharedPreferences("encrypted_session", Context.MODE_PRIVATE).edit().clear().commit()
        }
        if (scenario == "cleanup") return
        val store = ViewModelStore()
        lateinit var model: PhoneModel
        var chooserVisible by mutableStateOf(true)
        val refreshStarts = AtomicInteger()
        compose.runOnUiThread {
            model = ViewModelProvider(store, ViewModelProvider.AndroidViewModelFactory(application))[PhoneModel::class.java]
            compose.activity.setContent {
                val state by model.state.collectAsState()
                if (scenario == "chooser") {
                    LaunchedEffect(model) {
                        model.state.map { it.busy }.distinctUntilChanged().collect { if (it) refreshStarts.incrementAndGet() }
                    }
                    PhoneTheme {
                        if (chooserVisible) ProfileScreen(state, model, Modifier.fillMaxSize()) else Text("Chooser closed")
                    }
                } else if (scenario in setOf("playback", "resumePlayback")) {
                    StartupPreload(ready = !state.restoringConnection && (state.channel == null || state.playbackReady || state.error != null),
                        showContent = !state.restoringConnection) { TvShell(state, model, rememberGuideTime(state.channels)) }
                } else PhoneTheme { ServerConnectScreen(state, model, Modifier.fillMaxSize()) }
            }
        }
        fun settled() {
            compose.waitForIdle()
            compose.waitUntil(30_000) { !model.state.value.busy }
        }
        fun signIn(certificate: String) {
            compose.runOnUiThread { model.signInWithPIN(origin, username, "123456", certificate, serverName) }
            settled()
            compose.runOnIdle { assertTrue(model.state.value.error.orEmpty(), model.state.value.signedIn) }
        }
        fun showCredentials() {
            if (!model.state.value.trustIssue) {
                compose.onNodeWithText("$serverName (Recently connected)", substring = true).performClick()
            }
            compose.waitUntil(20_000) { compose.onAllNodesWithText("Username").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Username").assertIsDisplayed()
            compose.onNodeWithText("TV Pairing Pin").assertIsDisplayed()
        }
        try {
            settled()
            when (scenario) {
                "chooser" -> {
                    assumeTrue(BuildConfig.IS_TV)
                    fun waitForFocus() {
                        compose.waitUntil(5_000) {
                            compose.onNodeWithText("Admin").fetchSemanticsNode().config[androidx.compose.ui.semantics.SemanticsProperties.Focused]
                        }
                    }
                    waitForFocus()
                    val initialRefreshes = refreshStarts.get()
                    val deadline = android.os.SystemClock.elapsedRealtime() + 6_500
                    compose.waitUntil(8_000) {
                        refreshStarts.get() != initialRefreshes || android.os.SystemClock.elapsedRealtime() >= deadline
                    }
                    assertEquals("Open chooser must not poll", initialRefreshes, refreshStarts.get())
                    compose.onNodeWithText("Admin").assertIsFocused()
                    compose.runOnIdle { chooserVisible = false }
                    compose.runOnIdle { chooserVisible = true }
                    compose.waitUntil(5_000) { refreshStarts.get() > initialRefreshes }
                    settled()
                    waitForFocus()
                    assertEquals("Reopening refreshes exactly once", initialRefreshes + 1, refreshStarts.get())
                }
                "playback", "resumePlayback" -> {
                    assumeTrue(BuildConfig.IS_TV)
                    compose.runOnIdle { assertTrue(model.state.value.error.orEmpty(), model.state.value.signedIn) }
                    if (scenario == "playback") {
                        compose.runOnUiThread { model.selectProfile(model.state.value.profiles.first(), "") }
                        settled()
                    }
                    compose.waitUntil(20_000) { model.state.value.playbackReady || model.state.value.error != null }
                    compose.runOnIdle {
                        assertTrue(model.state.value.error.orEmpty(), model.state.value.playbackReady)
                        assertNotNull(model.state.value.profile)
                        assertNotNull(model.state.value.channel)
                        assertNotNull(model.state.value.playback)
                    }
                    compose.waitUntil(5_000) { compose.onAllNodesWithTag("startup-cover").fetchSemanticsNodes().isEmpty() }
                    compose.onNodeWithText("Select Profile").assertDoesNotExist()
                    compose.onNodeWithText("Connect to your AlreadyOn server").assertDoesNotExist()
                    compose.onNodeWithText("Refresh").assertDoesNotExist()
                    val device = androidx.test.uiautomator.UiDevice.getInstance(InstrumentationRegistry.getInstrumentation())
                    device.takeScreenshot(java.io.File(application.getExternalFilesDir(null), "recovery-$scenario.png"))
                }
                "form" -> {
                    compose.onNodeWithText("Hostname / IP").performTextInput("127.0.0.1")
                    compose.onNodeWithText("Port").performTextClearance()
                    compose.onNodeWithText("Port").performTextInput("19950")
                    val checkServer = compose.onNodeWithText("Check Server").fetchSemanticsNode().boundsInRoot
                    val port = compose.onNodeWithText("Port").fetchSemanticsNode().boundsInRoot
                    if (!BuildConfig.IS_TV) {
                        val rescan = compose.onNodeWithText("Rescan").fetchSemanticsNode().boundsInRoot
                        assertEquals(checkServer.top, rescan.top, 1f)
                        assertTrue(checkServer.right < rescan.left)
                        assertTrue(checkServer.top > port.bottom)
                    }
                    compose.onNodeWithText("Check Server").performClick()
                    compose.waitUntil(20_000) { compose.onAllNodesWithText("Username").fetchSemanticsNodes().isNotEmpty() }
                    compose.onNodeWithText("TV Pairing Pin").assertIsDisplayed()
                    compose.onNodeWithText("Enter your 6–12 digit TV PIN").assertDoesNotExist()
                    val connect = compose.onNodeWithText("Connect").fetchSemanticsNode().boundsInRoot
                    val back = compose.onNodeWithText("Go Back").fetchSemanticsNode().boundsInRoot
                    val help = compose.onNodeWithText("Help").fetchSemanticsNode().boundsInRoot
                    assertEquals(connect.top, back.top, 1f)
                    assertEquals(back.top, help.top, 1f)
                    assertTrue(connect.right < back.left && back.right < help.left)
                    if (!BuildConfig.IS_TV) {
                        val pairingPin = compose.onNodeWithText("TV Pairing Pin").fetchSemanticsNode().boundsInRoot
                        assertEquals("Both phone action rows use the same field spacing", checkServer.top - port.bottom,
                            connect.top - pairingPin.bottom, 2f)
                    }
                    compose.onNodeWithText("Help").performClick()
                    compose.onNodeWithText("Set your username and TV Pairing Pin in your server’s WebUI under Settings.", substring = true).assertIsDisplayed()
                    compose.onNodeWithText("Close").performClick()
                    compose.onNodeWithText("Go Back").performClick()
                    compose.onNodeWithText("127.0.0.1").assertExists()
                    compose.onNodeWithText("19950").assertExists()
                }
                "login" -> {
                    val base = origin.toHttpUrl()
                    val http = OkHttpClient.Builder().bootstrapTls(base).callTimeout(10, TimeUnit.SECONDS).build()
                    val certificate = try {
                        http.newCall(Request.Builder().url(base.resolve("/api/tv/trust")!!).build()).execute().use {
                            assertTrue(it.isSuccessful)
                            JSONObject(it.body!!.string()).getString("certificate")
                        }
                    } finally { http.connectionPool.evictAll(); http.dispatcher.executorService.shutdown() }
                    signIn(certificate)
                    assertTrue(SessionStore(application).read(origin).isNotEmpty())
                }
                "resume" -> {
                    compose.runOnIdle { assertTrue(model.state.value.error.orEmpty(), model.state.value.signedIn) }
                    assertTrue(SessionStore(application).read(origin).isNotEmpty())
                }
                "outage" -> {
                    compose.runOnIdle {
                        assertFalse(model.state.value.signedIn)
                        assertFalse(model.state.value.trustIssue)
                        assertNotNull(model.state.value.error)
                    }
                    assertTrue(SessionStore(application).read(origin).isNotEmpty())
                    compose.onNodeWithText("$serverName (Recently connected)", substring = true).assertIsDisplayed()
                }
                "revoked", "relogin" -> {
                    compose.runOnIdle { assertFalse(model.state.value.signedIn); assertFalse(model.state.value.trustIssue) }
                    assertTrue(SessionStore(application).read(origin).isEmpty())
                    assertEquals(origin, preferences.getString("server_origin", null))
                    assertNotNull(preferences.getString("trusted_ca", null))
                    showCredentials()
                    compose.onNodeWithText("Accept New CA").assertDoesNotExist()
                    compose.onNodeWithText("Connect").assertIsDisplayed()
                    if (scenario == "relogin") signIn(requireNotNull(preferences.getString("trusted_ca", null)))
                }
                "replacement" -> {
                    compose.runOnIdle { assertFalse(model.state.value.signedIn); assertTrue(model.state.value.trustIssue) }
                    assertTrue(SessionStore(application).read(origin).isNotEmpty())
                    val previousCa = preferences.getString("trusted_ca", null)
                    showCredentials()
                    val accept = compose.onNodeWithText("Accept New CA")
                    accept.assertIsDisplayed()
                    compose.onNodeWithText("Connect").assertDoesNotExist()
                    compose.onNodeWithText("Review Server CA").assertDoesNotExist()
                    accept.performClick()
                    compose.runOnIdle {
                        assertEquals(previousCa, preferences.getString("trusted_ca", null))
                        assertFalse(model.state.value.signedIn)
                    }
                    assertTrue(SessionStore(application).read(origin).isNotEmpty())
                    assertTrue(accept.fetchSemanticsNode().boundsInRoot.top > compose.onNodeWithText("TV Pairing Pin").fetchSemanticsNode().boundsInRoot.bottom)
                    compose.onNodeWithText("Username").performTextInput(username)
                    compose.onNodeWithText("TV Pairing Pin").performTextInput("123456")
                    accept.performClick()
                    settled()
                    compose.onNodeWithText("Accept New CA").assertDoesNotExist()
                    compose.runOnIdle { assertTrue(model.state.value.error.orEmpty(), model.state.value.signedIn) }
                    val acceptedCa = requireNotNull(preferences.getString("trusted_ca", null))
                    assertFalse(sameServerTrust(previousCa, acceptedCa))
                }
                else -> error("Unknown recovery scenario")
            }
        } finally { compose.runOnUiThread { store.clear() } }
    }
}
