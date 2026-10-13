package io.github.alreadyonapp.alreadyon

import androidx.compose.ui.test.*
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.UiDevice
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Exercises Dashboard with a real account session and isolated host fixture, without onboarding dependencies. */
@RunWith(AndroidJUnit4::class)
class DashboardTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    @Test fun dashboardDataRemoteNavigationAndBack() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val port = InstrumentationRegistry.getArguments().getString("serverPort")?.toInt()
            ?: error("Use scripts/android/test-tv.py with a disposable server fixture")
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
            compose.waitUntil(15000) { compose.onAllNodesWithText("Admin").fetchSemanticsNodes().isNotEmpty() }
            compose.waitUntil(15000) { !model.state.value.busy }
            compose.runOnIdle { model.selectProfile(model.state.value.profiles.first { it.name == "Admin" }, "") }
            compose.waitUntil(15000) { compose.onAllNodesWithText("Got it").fetchSemanticsNodes().isNotEmpty() }
            compose.runOnIdle { model.finishTvTutorial() }
            compose.waitUntil(15000) { compose.onAllNodesWithText("CHANNEL").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Admin").performSemanticsAction(SemanticsActions.OnClick) { it() }
            compose.waitUntil(10000) { compose.onAllNodes(hasText("Dashboard") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Switch Profile").assertExists()
            compose.onNodeWithText("Profile Settings").assertExists()
            device.pressDPadCenter()
            compose.waitUntil(15000) { compose.onAllNodesWithText("No active streaming sessions.").fetchSemanticsNodes().isNotEmpty() }
            compose.waitUntil(15000) { compose.onAllNodesWithText("AlreadyOn TV fixture").fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("AlreadyOn TV fixture").assertExists()
            compose.onNodeWithText(origin).assertExists()
            compose.onNodeWithText("Server Version").assertExists()
            compose.onNodeWithText("App Version").assertExists()
            compose.onNodeWithText(BuildConfig.VERSION_NAME).assertExists()
            compose.onNodeWithText("Back to Guide").assertIsFocused()
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-dashboard.png"))
            device.pressDPadDown()
            compose.waitForIdle()
            assertTrue("Dashboard sections receive remote focus", compose.onAllNodes(isFocused() and (hasText("Server Details") or hasAnyDescendant(hasText("Server Details")))).fetchSemanticsNodes().isNotEmpty())
            repeat(5) { device.pressDPadDown(); compose.waitForIdle() }
            compose.onNodeWithText("CPU").assertExists()
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-dashboard-metrics.png"))
            device.pressDPadCenter()
            compose.runOnIdle { assertEquals("dashboard", model.state.value.settings) }
            compose.onAllNodes(isDialog()).assertCountEquals(0)
            device.pressBack()
            compose.waitUntil(15000) { compose.onAllNodesWithText("CHANNEL").fetchSemanticsNodes().isNotEmpty() }
            compose.runOnIdle { assertNull(model.state.value.settings); assertNotNull(model.state.value.profile) }
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-guide.png"))
        } finally {
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-last-state.png"))
        }
    }
}
