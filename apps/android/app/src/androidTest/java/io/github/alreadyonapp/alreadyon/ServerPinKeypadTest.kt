// Measure real Compose geometry and verify compact-keypad remote and edit behavior.
package io.github.alreadyonapp.alreadyon

import androidx.activity.compose.setContent
import androidx.compose.runtime.*
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@OptIn(ExperimentalTestApi::class)
@RunWith(AndroidJUnit4::class)
class ServerPinKeypadTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    @Test fun measuredSizeAndRemoteEditing() {
        var scale by mutableStateOf(1f)
        var pin by mutableStateOf("")
        var done = false
        val first = FocusRequester()
        compose.runOnUiThread { compose.activity.setContent {
            PhoneTheme { ServerPinKeypad(pin, first, { pin = it }, { done = true }, scale) }
        } }
        val original = compose.onNodeWithTag("server-pin-keypad").fetchSemanticsNode().boundsInRoot
        compose.runOnIdle { scale = 0.7f }
        val compact = compose.onNodeWithTag("server-pin-keypad").fetchSemanticsNode().boundsInRoot
        assertEquals(original.width * 0.7f, compact.width, 1f)
        assertEquals(original.height * 0.7f, compact.height, 1f)
        compose.runOnIdle { first.requestFocus() }
        compose.onNodeWithText("1").performKeyInput { pressKey(Key.DirectionRight); pressKey(Key.Enter) }
        compose.runOnIdle { assertEquals("2", pin) }
        compose.onNodeWithText("1").performClick()
        compose.onNodeWithText("Delete").performClick()
        compose.runOnIdle { assertEquals("2", pin) }
        compose.onNodeWithText("Clear").performClick()
        compose.runOnIdle { assertEquals("", pin) }
        repeat(13) { compose.onNodeWithText("1").performClick() }
        compose.runOnIdle { assertEquals(12, pin.length) }
        compose.onNodeWithText("✓ Done").performClick()
        compose.runOnIdle { assertTrue(done) }
    }
}
