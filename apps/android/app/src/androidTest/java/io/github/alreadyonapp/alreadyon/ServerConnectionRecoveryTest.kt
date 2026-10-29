package io.github.alreadyonapp.alreadyon

import android.app.Application
import android.content.Context
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
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
        require(origin == "https://127.0.0.1:19950")
        val application = FixtureApplication(InstrumentationRegistry.getInstrumentation().targetContext)
        val preferences = application.getSharedPreferences("connection", Context.MODE_PRIVATE)
        if (scenario == "login" || scenario == "cleanup") {
            preferences.edit().clear().commit()
            application.getSharedPreferences("encrypted_session", Context.MODE_PRIVATE).edit().clear().commit()
        }
        if (scenario == "cleanup") return
        val store = ViewModelStore()
        lateinit var model: PhoneModel
        compose.runOnUiThread {
            model = ViewModelProvider(store, ViewModelProvider.AndroidViewModelFactory(application))[PhoneModel::class.java]
            compose.activity.setContent {
                val state by model.state.collectAsState()
                PhoneTheme { ServerConnectScreen(state, model, Modifier.fillMaxSize()) }
            }
        }
        fun settled() {
            compose.waitForIdle()
            compose.waitUntil(30_000) { !model.state.value.busy }
        }
        fun signIn(certificate: String) {
            compose.runOnUiThread { model.signInWithPIN(origin, "fixture", "123456", certificate, "Recovery Fixture") }
            settled()
            compose.runOnIdle { assertTrue(model.state.value.error.orEmpty(), model.state.value.signedIn) }
        }
        fun showCredentials() {
            compose.onNodeWithText("Recovery Fixture (Recently connected)", substring = true).performClick()
            compose.waitUntil(20_000) { compose.onAllNodesWithText("Username").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Username").assertIsDisplayed()
            compose.onNodeWithText("TV PIN").assertIsDisplayed()
        }
        try {
            settled()
            when (scenario) {
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
                    compose.onNodeWithText("Recovery Fixture (Recently connected)", substring = true).assertIsDisplayed()
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
                    compose.runOnUiThread { model.reviewServerTrust() }
                    showCredentials()
                    val accept = compose.onNodeWithText("Accept New CA")
                    accept.assertIsDisplayed()
                    compose.onNodeWithText("Connect").assertDoesNotExist()
                    assertTrue(accept.fetchSemanticsNode().boundsInRoot.top > compose.onNodeWithText("TV PIN").fetchSemanticsNode().boundsInRoot.bottom)
                    accept.performClick()
                    settled()
                    compose.onNodeWithText("Accept New CA").assertDoesNotExist()
                    compose.onNodeWithText("Connect").assertIsDisplayed()
                    if (BuildConfig.IS_TV) compose.onNodeWithText("Connect").assertIsFocused()
                    val acceptedCa = requireNotNull(preferences.getString("trusted_ca", null))
                    assertFalse(sameServerTrust(previousCa, acceptedCa))
                    signIn(acceptedCa)
                }
                else -> error("Unknown recovery scenario")
            }
        } finally { compose.runOnUiThread { store.clear() } }
    }
}
