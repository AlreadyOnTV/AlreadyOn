// Exercise short release URLs while preserving signed historical package metadata.
package io.github.alreadyonapp.alreadyon

import androidx.activity.compose.setContent
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class UpdateDialogTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    @Test fun updateDialogShowsVersionsAndBothActions() {
        var accepted = false; var cancelled = false
        compose.runOnUiThread { compose.activity.setContent { PhoneTheme { UpdateDialog(AppUpdateState(AppUpdate("26.10.11-alpha1", "26.10.11-alpha1", 24,
            if (BuildConfig.IS_TV) "tv" else "phone", "", "", 1)),
            { accepted = true }, { cancelled = true }, {}, {}) } } }
        compose.onNodeWithText("Update Available").assertIsDisplayed()
        compose.onNodeWithText("Current version: ${BuildConfig.VERSION_NAME}").assertIsDisplayed()
        compose.onNodeWithText("Available version: 26.10.11-alpha1").assertIsDisplayed()
        if (BuildConfig.IS_TV) compose.waitUntil(timeoutMillis = 5_000) {
            compose.onNodeWithText("Update").fetchSemanticsNode().config[SemanticsProperties.Focused]
        }
        val instrumentation = androidx.test.platform.app.InstrumentationRegistry.getInstrumentation()
        androidx.test.uiautomator.UiDevice.getInstance(instrumentation).takeScreenshot(java.io.File(instrumentation.targetContext.cacheDir, "update-dialog.png"))
        if (BuildConfig.IS_TV) {
            compose.waitForIdle()
            androidx.test.uiautomator.UiDevice.getInstance(androidx.test.platform.app.InstrumentationRegistry.getInstrumentation()).pressDPadCenter()
        } else compose.onNodeWithText("Update").performClick()
        compose.waitUntil(timeoutMillis = 5_000) { accepted }
        compose.runOnIdle { assertTrue(accepted) }
        compose.onNodeWithText("Cancel").performClick()
        compose.runOnIdle { assertTrue(cancelled) }
    }
    @Test fun deniedInstallPermissionOffersSettingsAndCancel() {
        var opened = false; var cancelled = false
        compose.runOnUiThread { compose.activity.setContent { PhoneTheme {
            UpdateDialog(AppUpdateState(AppUpdate("test", "test-24", 24, "phone", "", "", 1), phase = UpdatePhase.PERMISSION),
                {}, { cancelled = true }, { opened = true }, {})
        } } }
        compose.onNodeWithText("Open Settings").assertIsDisplayed().performClick()
        compose.runOnIdle { assertTrue(opened) }
        compose.onNodeWithText("Cancel").performClick()
        compose.runOnIdle { assertTrue(cancelled) }
    }
    @Test fun downloadingShowsProgressAndCanBeCancelled() {
        var cancelled = false
        compose.runOnUiThread { compose.activity.setContent { PhoneTheme {
            UpdateDialog(AppUpdateState(AppUpdate("test", "test-24", 24, "phone", "", "", 1), phase = UpdatePhase.DOWNLOADING, progress = .5f),
                {}, { cancelled = true }, {}, {})
        } } }
        compose.onNodeWithText("Downloading… 50%").assertIsDisplayed()
        compose.onNodeWithText("Cancel").performClick()
        compose.runOnIdle { assertTrue(cancelled) }
    }

}
