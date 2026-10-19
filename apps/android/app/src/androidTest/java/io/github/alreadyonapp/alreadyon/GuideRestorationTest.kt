package io.github.alreadyonapp.alreadyon

import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
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
