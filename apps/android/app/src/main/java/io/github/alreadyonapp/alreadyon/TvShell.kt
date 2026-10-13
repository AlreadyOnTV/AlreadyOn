package io.github.alreadyonapp.alreadyon

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalContext
import android.app.Activity
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.contentDescription

/** TV navigation is fixed-position presentation; tuning never rotates or remounts the shell. */
@Composable
internal fun TvShell(state: PhoneState, model: PhoneModel) {
    var fullscreen by rememberSaveable { mutableStateOf(false) }
    var controlsSignal by remember { mutableIntStateOf(0) }
    var controlsVisible by remember { mutableStateOf(false) }
    var region by remember { mutableStateOf("guide") }
    var guideOnCurrent by remember { mutableStateOf(true) }
    var guideReset by remember { mutableIntStateOf(0) }
    var profileOpenSignal by remember { mutableIntStateOf(0) }
    val selection = remember { mutableStateOf<Pair<GuideChannel, GuideProgram?>?>(null) }
    val scope = rememberCoroutineScope()
    val profileFocus = remember { FocusRequester() }
    val activity = LocalContext.current as? Activity
    fun returnToGuide() { region = "guide"; guideOnCurrent = true; guideReset++ }
    fun guideBack() {
        if (region == "guide") {
            if (!guideOnCurrent) { guideOnCurrent = true; guideReset++ }
            else if (state.channel != null) { region = "player"; controlsSignal++ } else activity?.finish()
        } else activity?.finish()
    }
    val focus = remember { FocusRequester() }
    val manager = LocalFocusManager.current
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, model) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) model.suspendWatching()
            if (event == Lifecycle.Event.ON_START) model.resumeWatching()
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
    LaunchedEffect(state.profile == null) { if (state.profile == null) fullscreen = false }
    LaunchedEffect(fullscreen, controlsVisible) { if (fullscreen && !controlsVisible) focus.requestFocus() }
    val currentSettings by rememberUpdatedState(state.settings)
    LaunchedEffect(state.profile?.id) {
        while (state.profile != null) { delay(30_000); if (currentSettings == null) model.refreshGuide() }
    }
    BackHandler(enabled = fullscreen || state.settings != null || state.profile != null) {
        when {
            state.tvTutorial -> model.finishTvTutorial()
            fullscreen -> { fullscreen = false; returnToGuide(); manager.clearFocus() }
            state.settings != null -> model.closeSettings()
            else -> guideBack()
        }
    }
    PhoneTheme {
        val palette = phonePalette
        Surface(Modifier.fillMaxSize(), color = palette.background, contentColor = palette.text) {
        Column(Modifier.fillMaxSize().padding(horizontal = if (fullscreen) 0.dp else 16.dp, vertical = if (fullscreen) 0.dp else 16.dp)
            .padding(top = if (fullscreen) 0.dp else 12.dp)
            .onPreviewKeyEvent { event ->
                if (state.tvTutorial) {
                    if (event.key == Key.Back) {
                        if (event.type == KeyEventType.KeyUp) model.finishTvTutorial()
                        return@onPreviewKeyEvent true
                    }
                    return@onPreviewKeyEvent false
                }
                if (!fullscreen) {
                    if (state.profile == null || state.settings != null) return@onPreviewKeyEvent false
                    when (event.key) {
                        Key.Back -> {
                            if (event.type == KeyEventType.KeyUp) guideBack()
                            return@onPreviewKeyEvent true
                        }
                        Key.DirectionUp, Key.DirectionDown -> if (region == "player") {
                            if (event.type == KeyEventType.KeyDown) {
                                if (event.key == Key.DirectionUp) { region = "profile"; profileFocus.requestFocus(); profileOpenSignal++ }
                                else returnToGuide()
                            }
                            return@onPreviewKeyEvent true
                        }
                        else -> Unit
                    }
                    return@onPreviewKeyEvent false
                }
                when (event.key) {
                    Key.Back -> {
                        if (controlsVisible) false else {
                            if (event.type == KeyEventType.KeyUp) { fullscreen = false; returnToGuide(); manager.clearFocus() }
                            true
                        }
                    }
                    Key.DirectionLeft, Key.DirectionRight -> {
                        if (controlsVisible || state.playback == null) return@onPreviewKeyEvent false
                        if (event.type == KeyEventType.KeyUp) model.stepChannel(if (event.key == Key.DirectionRight) 1 else -1)
                        true
                    }
                    Key.DirectionUp, Key.DirectionDown -> {
                        if (event.type == KeyEventType.KeyDown) {
                            if (!controlsVisible) { controlsVisible = true; controlsSignal++ }
                        }
                        true
                    }
                    Key.DirectionCenter, Key.Enter, Key.NumPadEnter -> {
                        if (controlsVisible) false else {
                            if (event.type == KeyEventType.KeyDown) { controlsVisible = true; controlsSignal++ }
                            true
                        }
                    }
                    else -> false
                }
            // Only fullscreen needs a focus target to receive remote keys with hidden controls.
            // In the guide and sign-in screens, focus belongs to the actionable children.
            }.then(if (fullscreen) Modifier.focusRequester(focus).focusable() else Modifier),
            verticalArrangement = Arrangement.spacedBy(if (fullscreen) 0.dp else 8.dp)) {
            if (!fullscreen && (state.profile == null || state.tvTutorial)) Row(Modifier.fillMaxWidth()) { PhoneBrand() }
            if (!fullscreen) {
                if (state.signedIn) state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                // Routine background refreshes must not animate or shift the TV layout.
            }
            ServerTrustAlert(state, model)
            when {
                !state.signedIn -> ServerConnectScreen(state, model, Modifier.weight(1f))
                state.mustChangePassword -> AccountScreen(state, model, forced = true)
                state.settings == "account" -> AccountScreen(state, model)
                state.settings == "profile" -> ProfileSettingsScreen(state, model)
                state.settings == "dashboard" -> DashboardScreen(state, model, Modifier.weight(1f))
                state.profile == null -> ProfileScreen(state, model, Modifier.weight(1f))
                state.tvTutorial -> TvNavigationTutorial(Modifier.weight(1f), model::finishTvTutorial)
                else -> {
                    if (state.channel != null) {
                        // One player call site keeps the stream/settings alive when returning to the guide.
                        Row(if (fullscreen) Modifier.fillMaxWidth().weight(1f) else Modifier.fillMaxWidth().height(162.dp),
                            horizontalArrangement = Arrangement.spacedBy(if (fullscreen) 0.dp else 20.dp)) {
                            PhonePlayer(state, model,
                                if (fullscreen) Modifier.fillMaxSize() else Modifier.width(288.dp).fillMaxHeight(),
                                fullscreen = fullscreen, tv = true, tvActive = fullscreen || region == "player", controlsSignal = controlsSignal,
                                onControlsVisibility = { controlsVisible = it }, onFullscreen = { if (!fullscreen) model.unmute(); fullscreen = !fullscreen; if (fullscreen) controlsSignal++ else returnToGuide() },
                                onExitFullscreen = { fullscreen = false; returnToGuide() })
                            if (!fullscreen) Box(Modifier.weight(1f).fillMaxHeight()) {
                                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                                    ProfileMenu(state, model, Modifier.focusRequester(profileFocus).onFocusChanged { if (it.isFocused) region = "profile" },
                                        openSignal = profileOpenSignal, onMenuDismiss = {
                                            scope.launch { withFrameNanos { }; region = "player"; controlsSignal++ }
                                        })
                                }
                                TvGuideDetails(selection, state.channel, palette, Modifier.fillMaxSize())
                            }
                        }
                    }
                    if (!fullscreen) TvGuide(state, palette, Modifier.weight(1f), resetSignal = guideReset,
                        onSelection = { channel, program -> selection.value = channel to program },
                        onChannelFocus = { id, current -> region = "guide"; guideOnCurrent = id == state.channel?.id && current }) { channel ->
                        model.unmute(); model.watch(channel); fullscreen = true; controlsSignal++
                    }
                }
            }
        }
        }
    }
}


/** Read selection only here so each arrow does not recompose the player and shell. */
@Composable
private fun TvGuideDetails(selection: State<Pair<GuideChannel, GuideProgram?>?>, fallback: GuideChannel,
    palette: PhonePalette, modifier: Modifier) {
    Column(modifier.padding(horizontal = 4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        val channel = selection.value?.first ?: fallback
        val program = selection.value?.second
        Text("${channel.number} · ${channel.name}", style = MaterialTheme.typography.titleLarge, maxLines = 1, modifier = Modifier.padding(end = 180.dp))
        program?.let {
            Text(it.title + (if (it.year > 0) " (${it.year})" else ""), style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(top = 16.dp).semantics { contentDescription = "Selected program: ${it.title} · ${it.startsAt}" }, maxLines = 2, overflow = TextOverflow.Ellipsis)
            val episode = if (it.episodeNumber > 0) "S${it.seasonNumber.toString().padStart(2, '0')}E${it.episodeNumber.toString().padStart(2, '0')} · ${it.episodeTitle}" else ""
            val details = listOf(episode, it.rating, it.airDate).filter { value -> value.isNotBlank() }.joinToString(" · ")
            if (details.isNotBlank()) Text(details, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (it.overview.isNotBlank()) Text(it.overview, color = palette.muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
    }
}
