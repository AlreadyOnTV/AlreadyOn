// Exercise the real installed IME without Espresso's hidden input-manager APIs.
package io.github.alreadyonapp.alreadyon

import android.provider.Settings
import androidx.activity.compose.setContent
import androidx.compose.runtime.*
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ServerPinFieldTest {
    @Test fun numericEditingVisibilityAndDone() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val device = UiDevice.getInstance(instrumentation)
        var pin by mutableStateOf("")
        var visible by mutableStateOf(false)
        var done = false
        val focus = FocusRequester()
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent {
                PhoneTheme { ServerPinField(pin, { pin = it }, visible, { visible = !visible }, true, { done = true }, Modifier.padding(top = 80.dp).focusRequester(focus)) }
            } }
            if (BuildConfig.IS_TV) {
                instrumentation.runOnMainSync { focus.requestFocus() }
                assertTrue("TV keypad opens", device.wait(Until.hasObject(By.text("Delete")), 5000))
                device.pressDPadCenter() // Initial focus must be 1, without navigation.
                device.pressDPadRight(); device.pressDPadCenter() // 2
                device.pressDPadDown(); device.pressDPadCenter() // 5
                device.waitForIdle()
                instrumentation.runOnMainSync { assertEquals("125", pin) }
                repeat(3) { device.pressDPadCenter() }
                device.findObject(By.text("Done")).click()
                device.waitForIdle()
                instrumentation.runOnMainSync { assertEquals("125555", pin); assertTrue(done) }
                return
            }
            val field = device.wait(Until.findObject(By.clazz("android.widget.EditText")), 10000)
            assertNotNull("Editable PIN field", field)
            field.click()
            val imePackage = Settings.Secure.getString(instrumentation.targetContext.contentResolver,
                Settings.Secure.DEFAULT_INPUT_METHOD).substringBefore("/")
            assertTrue("The installed device keyboard opens for PIN entry",
                device.wait(Until.hasObject(By.pkg(imePackage)), 10000))
            field.text = "12a345678901234"
            device.waitForIdle()
            instrumentation.runOnMainSync { assertEquals("123456789012", pin) }
            device.findObject(By.desc("Show PIN")).click()
            assertTrue(device.wait(Until.hasObject(By.text("123456789012")), 5000))
            device.findObject(By.desc("Hide PIN")).click()
            device.findObject(By.clazz("android.widget.EditText")).text = "654321"
            device.waitForIdle()
            instrumentation.runOnMainSync { assertEquals("654321", pin) }
            // Invoke the installed keyboard's Done action through its accessibility node.
            val doneKey = device.wait(Until.findObject(By.pkg(imePackage).descContains("Done")), 5000)
            assertNotNull("Numeric keyboard Done key", doneKey)
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "server-pin-keyboard.png"))
            doneKey.click()
            device.waitForIdle()
            instrumentation.runOnMainSync { assertTrue(done) }
        }
    }
}
