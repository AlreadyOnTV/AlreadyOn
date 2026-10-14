package io.github.alreadyonapp.alreadyon

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.UiDevice
import org.junit.Assert.*
import androidx.lifecycle.ViewModelProvider
import org.junit.Rule
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.*
import androidx.compose.ui.semantics.SemanticsActions
import org.junit.Test
import org.junit.runner.RunWith
import java.net.ServerSocket
import java.net.Socket
import kotlin.concurrent.thread

/** Real TV discovery/login against a marked, disposable host server and synthetic media.
 * The TCP bridge places that host fixture on the emulator's own virtual LAN; no
 * production discovery hooks, saved credentials or live databases are involved.
 */
@RunWith(AndroidJUnit4::class)
class TvNavigationTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    @Test fun pairingGuideAndRemoteControls() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val port = InstrumentationRegistry.getArguments().getString("serverPort")?.toInt()
            ?: error("Supply serverPort for an isolated synthetic server; see docs/GOOGLE_TV.md")
        val device = UiDevice.getInstance(instrumentation)
        val bridge = ServerSocket(0)
        var running = true
        val peers = java.util.Collections.synchronizedList(mutableListOf<Socket>())
        thread(isDaemon = true) {
            while (running) {
                val incoming = runCatching { bridge.accept() }.getOrNull() ?: break
                thread(isDaemon = true) {
                    val outgoing = Socket("10.0.2.2", port)
                    peers.add(incoming); peers.add(outgoing)
                    thread(isDaemon = true) { runCatching { incoming.getInputStream().copyTo(outgoing.getOutputStream()) }; runCatching { outgoing.shutdownOutput() } }
                    runCatching { outgoing.getInputStream().copyTo(incoming.getOutputStream()) }
                    runCatching { incoming.close() }; runCatching { outgoing.close() }
                }
            }
        }
        val manual = InstrumentationRegistry.getArguments().getString("entryMode") == "manual"
        try {
            // Advance the static startup cover on Compose's test clock before sending input.
            compose.mainClock.advanceTimeBy(3000)
            compose.waitForIdle()
            compose.onAllNodes(hasClickAction() and (hasContentDescription("AlreadyOn") or hasAnyDescendant(hasContentDescription("AlreadyOn")))).assertCountEquals(0)
            fun exists(text: String) = compose.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty()
            fun click(text: String) {
                compose.waitUntil(60000) { exists(text) }
                val node = compose.onAllNodesWithText(text).onFirst()
                // The centered keypad fits in its dialog and has no scroll container.
                if (!exists("✓ Done")) node.performScrollTo()
                node.performClick()
            }
            if (manual) {
                val addressField = hasSetTextAction() and hasText("Hostname / IP")
                compose.onNode(addressField).assertExists()
                compose.onNodeWithText("https://").assertExists()
                compose.onNodeWithText("Searching for local servers…", substring = true).assertExists()
                compose.waitUntil(15000) {
                    compose.onAllNodes(hasText("10.0.2.2:19900", substring = true)).fetchSemanticsNodes().isNotEmpty()
                }
                compose.onNode(addressField).assertExists()
                compose.onNodeWithText("Searching for local servers…", substring = true).assertExists()
                compose.onNode(addressField).performTextReplacement("127.0.0.1")
                compose.onNode(hasSetTextAction() and hasText("Port")).assertTextContains("19900")
                compose.onNode(hasSetTextAction() and hasText("Port")).performTextReplacement(bridge.localPort.toString())
                compose.onNodeWithText("Searching for local servers…", substring = true).assertExists()
                device.pressBack() // Dismiss the TV keyboard.
                click("Check Server")
            } else {
                compose.waitUntil(60000) { compose.onAllNodes(hasText("10.0.2.2:19900", substring = true)).fetchSemanticsNodes().isNotEmpty() }
                val server = hasText("10.0.2.2:19900", substring = true)
                // Establish a known start below the discovered server, then navigate with the remote.
                compose.onNodeWithText("Rescan").performSemanticsAction(SemanticsActions.RequestFocus) { it() }
                repeat(8) {
                    if (compose.onAllNodes(server and isFocused()).fetchSemanticsNodes().isEmpty()) {
                        device.pressDPadUp()
                        compose.waitForIdle()
                    }
                }
                compose.onAllNodes(server).onFirst().assertIsFocused()
                device.pressDPadCenter()
            }
            compose.waitUntil(60000) { exists("Username") }
            compose.onNode(hasSetTextAction() and hasText("Username")).assertIsFocused()
            assertFalse("Server fingerprint confirmation is skipped", exists("Confirm Server"))
            compose.onNode(hasSetTextAction() and hasText("Username")).performTextInput("tv-fixture")
            compose.onNode(hasSetTextAction() and hasText("Username")).performImeAction()
            compose.waitUntil(10000) { exists("✓ Done") }
            compose.waitUntil(10000) { compose.onAllNodes(hasText("1") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            // Synthetic account PIN set by the isolated test fixture, never a live account.
            "123456".forEach { click(it.toString()) }
            click("✓ Done")
            compose.waitUntil(10000) { !exists("✓ Done") }
            click("Connect")
            compose.waitUntil(10000) { compose.onAllNodes(hasText("Admin") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            device.pressDPadCenter()
            compose.waitUntil(10000) { exists("Your remote, at a glance") }
            compose.onNodeWithText("Got it").assertIsFocused()
            device.pressDPadCenter()
            compose.waitUntil(10000) { exists("CHANNEL") }
            assertTrue("Compact guide on first entry", exists("CHANNEL"))
            assertTrue("Guide channel 110", exists("110"))
            compose.waitUntil(10000) { compose.onAllNodes(hasContentDescription("Channel 110: TV fixture 110") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            compose.onNodeWithText("Admin").performClick()
            compose.onNodeWithText("Switch Profile").assertExists()
            compose.onNodeWithText("Sign Out").assertExists()
            compose.onNodeWithText("Profile").assertExists()
            compose.onNodeWithText("Dashboard").assertDoesNotExist()
            device.pressBack()
            val model = ViewModelProvider(compose.activity)[PhoneModel::class.java]
            compose.runOnIdle { model.setQuality(720); model.setSubtitle(null) }
            fun controlFocused(label: String) = compose.onAllNodes(
                (hasContentDescription(label) or hasAnyDescendant(hasContentDescription(label))) and isFocused()).fetchSemanticsNodes().isNotEmpty()
            val muted = false
            click("110")
            assertTrue("Fullscreen hides guide", !exists("CHANNEL"))
            compose.waitUntil(60000) { !exists("Preparing channel…") }
            compose.waitUntil(10000) { controlFocused("Pause") }
            device.pressDPadDown()
            device.pressDPadUp()
            compose.waitForIdle()
            assertTrue("Vertical arrows retain transport focus", controlFocused("Pause"))
            // Reveal the hidden transport controls using only the remote.
            device.pressDPadDown()
            compose.waitUntil(10000) { compose.onAllNodes(hasContentDescription("Mute") or hasContentDescription("Unmute")).fetchSemanticsNodes().isNotEmpty() }
            device.pressDPadRight()
            compose.waitForIdle()
            compose.runOnIdle { assertEquals("Overlay navigation must not tune", 110, model.state.value.channel?.number) }
            device.pressBack() // Hide controls before channel navigation.
            device.pressDPadRight()
            compose.waitUntil(60000) { model.state.value.channel?.number == 111 && model.state.value.playback != null }
            compose.waitUntil(10000) { controlFocused("Pause") }
            device.pressBack() // A newly tuned channel starts with the overlay focused.
            device.pressDPadLeft()
            compose.waitUntil(60000) { model.state.value.channel?.number == 110 && model.state.value.playback != null }
            compose.runOnIdle {
                assertEquals("Quality survives tuning", 720, model.state.value.quality)
                assertEquals("Mute survives tuning", muted, model.state.value.muted)
                assertNull("Subtitles remain off", model.state.value.subtitleIndex)
            }
            compose.waitUntil(10000) { controlFocused("Pause") }
            device.pressDPadRight(); device.pressDPadRight(); device.pressDPadCenter()
            click("Quality")
            compose.waitUntil(10000) { compose.onAllNodes(hasText("✓ 720p") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            device.pressBack() // Submenu to root menu.
            click("Audio Tracks")
            compose.waitUntil(10000) { compose.onAllNodes(hasText("✓ ", substring = true) and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            device.pressBack()
            click("Subtitles")
            compose.waitUntil(10000) { compose.onAllNodes(hasText("✓ Off") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            device.pressBack(); device.pressBack(); device.pressBack(); device.pressBack()
            compose.waitUntil(10000) { exists("CHANNEL") }
            device.pressDPadDown()
            device.pressBack() // Restore the current preview row.
            compose.waitUntil(10000) { compose.onAllNodes(hasContentDescription("Channel 110: TV fixture 110") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            device.pressBack() // Focus the mini-player.
            compose.waitUntil(10000) { controlFocused("Pause") }
            device.pressDPadUp()
            compose.waitUntil(10000) { compose.onAllNodes(hasText("Switch Profile") and isFocused()).fetchSemanticsNodes().isNotEmpty() }
            device.pressBack()
            compose.waitUntil(10000) { controlFocused("Pause") }
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-guide.png"))
            device.pressBack() // Exit from profile/player without signing out.
            compose.waitUntil(10000) { compose.activity.isFinishing }
            return
        } finally {
            device.takeScreenshot(java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "tv-last-state.png"))
            running = false; bridge.close()
            peers.toList().forEach { runCatching { it.close() } }
        }
    }
}
