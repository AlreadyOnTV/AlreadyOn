package io.github.alreadyonapp.alreadyon

import androidx.activity.compose.setContent
import android.app.Application
import android.content.Context
import android.os.SystemClock
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.InputMode
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.platform.LocalInputModeManager
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.time.Instant

// Pure synthetic guide UI; no server, account, media or stored preferences are changed.
@RunWith(AndroidJUnit4::class)
class GuideRestorationTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    @Test fun onlyHumanGuideInputStartsPauseAndLeavingClearsIt() {
        val application = object : Application() {
            init { attachBaseContext(InstrumentationRegistry.getInstrumentation().targetContext) }
            override fun getSharedPreferences(name: String, mode: Int) = super.getSharedPreferences("guide-105-$name", mode)
        }
        val store = ViewModelStore()
        lateinit var model: PhoneModel
        var visible by mutableStateOf(true)
        val start = System.currentTimeMillis()
        val programs = (0..20).map { index -> GuideProgram("Synthetic $index",
            Instant.ofEpochMilli(start - 60_000 + index * 300_000).toString(),
            Instant.ofEpochMilli(start - 60_000 + (index + 1) * 300_000).toString()) }
        val channels = (101L..120L).map { GuideChannel(it, it.toInt(), "Synthetic $it", programs) }
        val state = PhoneState(channels = channels, channel = channels.first())
        try {
            compose.runOnUiThread {
                model = ViewModelProvider(store, ViewModelProvider.AndroidViewModelFactory(application))[PhoneModel::class.java]
                compose.activity.setContent {
                    val deadline by model.guideNavigationDeadline.collectAsState()
                    val now = rememberGuideTime(channels, deadline)
                    val input = LocalInputModeManager.current
                    LaunchedEffect(Unit) { if (BuildConfig.IS_TV) input.requestInputMode(InputMode.Keyboard) }
                    PhoneTheme {
                        Surface(Modifier.fillMaxSize(), color = phonePalette.background, contentColor = phonePalette.text) {
                            if (visible) {
                                if (BuildConfig.IS_TV) TvGuide(state, phonePalette, Modifier.fillMaxSize(), now,
                                    onNavigation = model::noteGuideNavigation, onGuideExit = model::resetGuideNavigation, onTune = {})
                                else ChannelGuide(state, model, phonePalette, Modifier.fillMaxSize(), now, onTune = {})
                            } else Text("Guide closed")
                        }
                    }
                }
            }
            compose.waitForIdle()
            assertEquals(0L, model.guideNavigationDeadline.value)
            compose.onNodeWithTag("guide-rows").performScrollToIndex(10)
            compose.onNodeWithTag("guide-rows").performScrollToIndex(0)
            assertEquals("Programmatic scrolling is not human input", 0L, model.guideNavigationDeadline.value)
            if (BuildConfig.IS_TV) {
                compose.onNodeWithTag("guide-channel-101").performSemanticsAction(SemanticsActions.RequestFocus) { it() }
                compose.onNodeWithTag("guide-channel-101").performKeyInput { pressKey(Key.DirectionRight) }
            } else compose.onNodeWithTag("guide-rows").performTouchInput { swipeUp() }
            compose.waitUntil(5_000) { model.guideNavigationDeadline.value > 0L }
            val remaining = guideNavigationRemaining(model.guideNavigationDeadline.value, SystemClock.elapsedRealtime())
            assertTrue("Human navigation starts a two-minute pause", remaining in 110_000L..120_000L)
            compose.runOnIdle { visible = false }
            compose.waitUntil(5_000) { model.guideNavigationDeadline.value == 0L }
            compose.runOnIdle { visible = true }
            compose.waitForIdle()
            assertEquals("Re-entry does not restore an expired navigation pause", 0L, model.guideNavigationDeadline.value)
        } finally { compose.runOnUiThread { store.clear() } }
    }

    @Test fun startupWaitsForRestorationAndPlaybackWithoutComposingOnboarding() {
        var ready by mutableStateOf(false)
        var showContent by mutableStateOf(false)
        compose.runOnUiThread {
            compose.activity.setContent {
                StartupPreload(ready = ready, showContent = showContent) { Text("Restored guide") }
            }
        }
        compose.onNodeWithTag("startup-cover").assertExists()
        compose.onNodeWithText("Restored guide").assertDoesNotExist()
        compose.runOnIdle { showContent = true }
        compose.onNodeWithText("Restored guide").assertExists()
        compose.onNodeWithTag("startup-cover").assertExists()
        compose.runOnIdle { ready = true }
        compose.waitUntil(5_000) { compose.onAllNodesWithTag("startup-cover").fetchSemanticsNodes().isEmpty() }
        compose.runOnIdle { ready = false }
        compose.onNodeWithTag("startup-cover").assertDoesNotExist()
    }

    @Test fun fullscreenChannelChangesRestoreLiveAiringAtTopAcrossMidnight() {
        val start = Instant.parse("2026-10-24T23:29:59Z").toEpochMilli()
        val programs = (0..20).map { index ->
            GuideProgram("Synthetic $index",
                Instant.ofEpochMilli(start - 29 * 60_000 + index * 25 * 60_000).toString(),
                Instant.ofEpochMilli(start - 29 * 60_000 + (index + 1) * 25 * 60_000).toString())
        }
        val channels = (101L..120L).map { GuideChannel(it, it.toInt(), "Synthetic $it", programs) }
        var now by mutableLongStateOf(start)
        var visible by mutableStateOf(true)
        var state by mutableStateOf(PhoneState(channels = channels, channel = channels.first()))
        compose.runOnUiThread {
            val model = ViewModelProvider(compose.activity)[PhoneModel::class.java]
            compose.activity.setContent {
                PhoneTheme {
                    Box(Modifier.fillMaxSize()) {
                        if (visible) {
                            if (BuildConfig.IS_TV) TvGuide(state, phonePalette, Modifier.fillMaxSize(), now = now, onTune = {})
                            else ChannelGuide(state, model, phonePalette, Modifier.fillMaxSize(), now = now, onTune = {})
                        }
                    }
                }
            }
        }
        fun assertRestored(channel: Long) {
            compose.waitForIdle()
            compose.onNodeWithTag("guide-watched-airing", useUnmergedTree = true)
                .assertIsDisplayed().assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription,
                    channels.first { it.id == channel }.liveProgram(now)!!.startsAt))
            compose.onNodeWithTag("guide-timeline", useUnmergedTree = true)
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, guideHalfHour(now).toString()))
            val top = compose.onNodeWithTag("guide-rows").getUnclippedBoundsInRoot().top.value
            val row = compose.onNodeWithTag("guide-channel-$channel", useUnmergedTree = true).getUnclippedBoundsInRoot()
            assertEquals("Watched channel is the first visible row", top, row.top.value, 1f)
            if (BuildConfig.IS_TV) compose.onNodeWithTag("guide-channel-$channel").assertIsFocused()
        }
        assertRestored(101)
        compose.runOnIdle { visible = false }
        compose.runOnIdle {
            state = state.copy(channel = channels.first { it.id == 115L })
            now += 3_600_000L
            visible = true
        }
        assertRestored(115)
        val prior = state.channel!!.liveProgram(now)!!.startsAt
        compose.runOnIdle { now += 1_800_001L }
        compose.onNodeWithTag("guide-watched-airing", useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state.channel!!.liveProgram(now)!!.startsAt))
        assertNotEquals(prior, state.channel!!.liveProgram(now)!!.startsAt)
        for (channel in listOf(103L, 120L)) {
            compose.runOnIdle { visible = false }
            compose.runOnIdle { state = state.copy(channel = channels.first { it.id == channel }); visible = true }
            assertRestored(channel)
        }
        if (BuildConfig.IS_TV) {
            compose.onNodeWithTag("guide-channel-120").performKeyInput { pressKey(androidx.compose.ui.input.key.Key.DirectionRight) }
            compose.onNodeWithTag("guide-watched-airing", useUnmergedTree = true)
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state.channel!!.liveProgram(now)!!.startsAt))
        }
    }
}
