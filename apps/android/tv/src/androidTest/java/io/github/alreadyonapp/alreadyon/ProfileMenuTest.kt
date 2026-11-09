package io.github.alreadyonapp.alreadyon

import androidx.compose.ui.test.*
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.UiDevice
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import kotlinx.coroutines.Dispatchers

/** Exercises the native menu order and profile navigation with an isolated host fixture. */
@RunWith(AndroidJUnit4::class)
class ProfileMenuTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>(effectContext = Dispatchers.Main.immediate)
    @Test fun profileMenuRemoteNavigationAndBack() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val portArgument = InstrumentationRegistry.getArguments().getString("serverPort")
        assumeTrue("Requires scripts/android/test-tv.py with a disposable server fixture", portArgument != null)
        val port = requireNotNull(portArgument).toInt()
        val device = UiDevice.getInstance(instrumentation)
        try {
            // Advance the static startup cover on Compose's test clock before sending input.
            compose.mainClock.advanceTimeBy(3000)
            compose.waitForIdle()
            val origin = "https://10.0.2.2:$port"
            // Trust bootstrapping reads only public metadata; credentials use the pinned instance certificate.
            val http = OkHttpClient.Builder().bootstrapTls(origin.toHttpUrl()).build()
            val certificate = http.newCall(Request.Builder().url("$origin/api/tv/trust").build()).execute().use {
                check(it.isSuccessful) { "Fixture trust request failed: ${it.code}" }
                JSONObject(it.body!!.string()).getString("certificate")
            }
            val model = ViewModelProvider(compose.activity)[PhoneModel::class.java]
            compose.runOnIdle { model.signInWithPIN(origin, "tv-fixture", "123456", certificate) }
            compose.waitUntil(15000) { compose.onAllNodes(hasText("Admin") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            compose.waitUntil(15000) { !model.state.value.busy }
            device.pressDPadCenter()
            device.waitForIdle()
            compose.waitUntil(15000) { compose.onAllNodesWithTag("guide-current-time").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Admin").performSemanticsAction(SemanticsActions.OnClick) { it() }
            compose.waitUntil(10000) { compose.onAllNodes(hasText("Switch Profile") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Profile").assertExists()
            compose.onNodeWithText("Sign Out").assertExists()
            compose.onNodeWithText("Exit App").assertExists()
            compose.onNodeWithText("Dashboard").assertDoesNotExist()
            compose.onNodeWithText("Settings").assertExists()
            compose.onNodeWithText("Server").assertDoesNotExist()
            device.pressDPadDown()
            compose.onNodeWithText("Profile").assertIsFocused()
            device.pressDPadCenter()
            compose.waitUntil(15000) { model.state.value.settings == "profile" }
            compose.onNodeWithText("Profile Settings").assertExists()
            device.pressBack()
            compose.waitUntil(15000) { compose.onAllNodesWithTag("guide-current-time").fetchSemanticsNodes().isNotEmpty() }
            compose.runOnIdle { assertNull(model.state.value.settings); assertNotNull(model.state.value.profile) }
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-guide.png"))
        } finally {
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-last-state.png"))
        }
    }
}
