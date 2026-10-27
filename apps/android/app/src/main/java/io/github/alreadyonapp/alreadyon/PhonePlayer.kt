package io.github.alreadyonapp.alreadyon

import android.os.SystemClock
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupProperties
import androidx.compose.ui.window.PopupPositionProvider
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.input.key.*
import kotlinx.coroutines.CancellationException
import androidx.compose.runtime.*
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.sp
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import java.time.Instant
import androidx.compose.ui.unit.dp
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.contentDescription
import kotlinx.coroutines.delay
import androidx.activity.compose.BackHandler
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.onFocusChanged
import kotlinx.coroutines.channels.ReceiveChannel
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.layout.onSizeChanged
import androidx.media3.common.C
import androidx.media3.common.Timeline
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.compose.ContentFrame
import androidx.media3.ui.compose.SURFACE_TYPE_TEXTURE_VIEW
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import java.io.IOException

/** Foreground-only native player with isolated media transport and no management cookies. */
@androidx.annotation.OptIn(UnstableApi::class)
@Composable
private fun NativeVideo(playback: PhonePlayback, model: PhoneModel, modifier: Modifier,
    paused: Boolean, volume: Float, onPosition: (Double) -> Unit, onTap: () -> Unit) {
    val context = LocalContext.current
    val player = remember(playback.revision) {
        val authorized = playback.url.toHttpUrl()
        val mediaPrefix = authorized.encodedPath.substringBeforeLast('/') + "/"
        val token = authorized.queryParameter("token") ?: throw IOException("Media authorization is missing.")
        // Check every manifest/segment request; never follow redirects or send credentials to another host.
        val http = OkHttpClient.Builder().serverTls(authorized, playback.trustedCa).followRedirects(false).followSslRedirects(false)
            .addInterceptor { chain ->
                val original = chain.request()
                val url = original.url
                if (!sameOrigin(authorized, url) || !url.encodedPath.startsWith(mediaPrefix) ||
                    url.username.isNotEmpty() || url.password.isNotEmpty()) throw IOException("Unexpected media route.")
                val actualToken = url.queryParameter("token")
                if (actualToken != null && actualToken != token) throw IOException("Unexpected media authorization.")
                val scoped = if (actualToken == null) url.newBuilder().addQueryParameter("token", token).build() else url
                chain.proceed(original.newBuilder().url(scoped).removeHeader("Cookie").removeHeader("Authorization").build())
            }.build()
        val sources = DefaultMediaSourceFactory(context).setDataSourceFactory(OkHttpDataSource.Factory(http))
        val builder = ExoPlayer.Builder(context).setMediaSourceFactory(sources)
        if (BuildConfig.IS_TV) builder.setLoadControl(DefaultLoadControl.Builder()
            .setBufferDurationsMs(5000, 15000, 500, 1000).setTargetBufferBytes(16 * 1024 * 1024).build())
        builder.build().apply {
            val preparedAt = SystemClock.elapsedRealtime()
            addListener(object : Player.Listener {
                private var firstFrame = false
                override fun onRenderedFirstFrame() {
                    if (firstFrame) return
                    firstFrame = true
                    if (android.util.Log.isLoggable(PLAYBACK_LOG_TAG, android.util.Log.DEBUG)) android.util.Log.d(PLAYBACK_LOG_TAG, "First frame revision=${playback.revision} playerMs=${SystemClock.elapsedRealtime()-preparedAt} tuneMs=${SystemClock.elapsedRealtime()-playback.requestedAt}")
                }
                private var aligned = false
                private var bufferingAt: Long? = null
                override fun onPlaybackStateChanged(playbackState: Int) {
                    if (playbackState == Player.STATE_BUFFERING) bufferingAt = SystemClock.elapsedRealtime()
                    if (playbackState == Player.STATE_READY) bufferingAt?.let {
                        if (android.util.Log.isLoggable(PLAYBACK_LOG_TAG, android.util.Log.DEBUG)) android.util.Log.d(PLAYBACK_LOG_TAG, "Buffer ready revision=${playback.revision} durationMs=${SystemClock.elapsedRealtime()-it}")
                        bufferingAt = null
                    }
                    if (playbackState != Player.STATE_READY || aligned) return
                    aligned = true
                    // Map the server's muxer date onto the current sliding HLS window, not playlist zero.
                    val window = Timeline.Window()
                    if (!currentTimeline.isEmpty) {
                        currentTimeline.getWindow(currentMediaItemIndex, window)
                        val target = playback.hlsTargetMs
                        if (target != null && window.windowStartTimeMs != C.TIME_UNSET) {
                            var position = (target + SystemClock.elapsedRealtime() - playback.capturedAt - window.windowStartTimeMs).coerceAtLeast(0)
                            if (duration != C.TIME_UNSET) position = position.coerceAtMost((duration - 500).coerceAtLeast(0))
                            seekTo(position)
                        }
                    }
                }
                override fun onPlayerError(error: PlaybackException) { model.playerFailed(playback.revision, error) }
            })
            // HLS positions are relative to its sliding window; align by date after the first timeline is ready.
            setMediaItem(MediaItem.fromUri(playback.url), if (playback.hlsTargetMs != null) C.TIME_UNSET else playback.startMs)
            prepare()
            playWhenReady = !paused
            this.volume = volume
        }
    }
    val positionCallback by rememberUpdatedState(onPosition)
    LaunchedEffect(player, playback.subtitleTracks.isNotEmpty()) {
        if (playback.subtitleTracks.isEmpty()) return@LaunchedEffect
        while (true) {
            val window = Timeline.Window()
            var shift = 0.0
            if (!player.currentTimeline.isEmpty && playback.hlsOriginMs != null) {
                player.currentTimeline.getWindow(player.currentMediaItemIndex, window)
                if (window.windowStartTimeMs != C.TIME_UNSET) shift = (window.windowStartTimeMs - playback.hlsOriginMs) / 1000.0
            }
            positionCallback(player.currentPosition / 1000.0 + playback.streamOffsetSeconds + shift)
            delay(200)
        }
    }
    SideEffect { player.playWhenReady = !paused; player.volume = volume }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(player, lifecycle) {
        var released = false
        fun release() { if (!released) { player.release(); released = true } }
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) release()
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer); release() }
    }
    val hostView = LocalView.current
    DisposableEffect(hostView) {
        val previous = hostView.keepScreenOn
        hostView.keepScreenOn = true
        onDispose { hostView.keepScreenOn = previous }
    }
    // TV uses the lower-power SurfaceView path; the phone retains its rotation-compatible TextureView.
    // TextureView participates in the Compose view's resizing instead of retaining an
    // independently composed SurfaceView buffer with stale bounds after Android 17 rotation.
    // Keep the same player and surface across display modes, preserving the stream and audio.
    ContentFrame(player = player, modifier = if (BuildConfig.IS_TV) modifier else modifier.clickable(onClick = onTap),
        surfaceType = if (BuildConfig.IS_TV) SURFACE_TYPE_SURFACE_VIEW else SURFACE_TYPE_TEXTURE_VIEW)
}

/** Overlay controls occupy the video surface; fullscreen never reserves guide or toolbar space. */
@Composable
internal fun PhonePlayer(state: PhoneState, model: PhoneModel, modifier: Modifier, fullscreen: Boolean, pictureInPicture: Boolean = false,
    tv: Boolean = false, tvActive: Boolean = true, controlsSignal: Int = 0, onControlsVisibility: (Boolean) -> Unit = {},
    remoteActions: ReceiveChannel<PlayerRemoteAction>? = null, onMenuVisibility: (Boolean) -> Unit = {},
    onFullscreen: () -> Unit, onExitFullscreen: () -> Unit = onFullscreen) {
    var visible by remember { mutableStateOf(!tv) }
    val transportFocus = remember { FocusRequester() }
    val muteFocus = remember { FocusRequester() }
    val settingsFocus = remember { FocusRequester() }
    val fullscreenFocus = remember { FocusRequester() }
    val controlTargets = listOf(transportFocus, muteFocus, settingsFocus, fullscreenFocus)
    var controlCursor by remember { mutableIntStateOf(0) }

    var controlsHeight by remember { mutableIntStateOf(0) }
    val density = LocalDensity.current
    var interaction by remember { mutableIntStateOf(0) }
    var menu by remember { mutableStateOf(false) }
    var page by remember { mutableStateOf<String?>(null) }
    // Process every remote command in order, including rapid Right/Right/Select.
    LaunchedEffect(fullscreen, tvActive) {
        if (tv && tvActive) { visible = true; menu = false; page = null; controlCursor = 0; interaction++ }
    }
    LaunchedEffect(state.playback?.revision) { if (tv && tvActive) { visible = true; controlCursor = 0; interaction++ } }
    LaunchedEffect(controlsSignal) { if (tv && tvActive) { visible = true; controlCursor = 0; interaction++ } }
    LaunchedEffect(menu) { onMenuVisibility(menu) }
    DisposableEffect(Unit) { onDispose { onMenuVisibility(false) } }
    LaunchedEffect(remoteActions, fullscreen) {
        if (remoteActions == null) return@LaunchedEffect
        if (!tv || !fullscreen) { while (remoteActions.tryReceive().isSuccess) { }; return@LaunchedEffect }
        for (action in remoteActions) {
            if (action == PlayerRemoteAction.Back) {
                if (page != null) page = null else if (menu) menu = false
                else if (visible) visible = false else onExitFullscreen()
                continue
            }
            if (menu) continue // The focusable popup owns its remote events.
            if (action == PlayerRemoteAction.ChannelUp || action == PlayerRemoteAction.ChannelDown) {
                model.stepChannel(if (action == PlayerRemoteAction.ChannelDown) 1 else -1)
                continue
            }
            val hidden = !visible
            visible = true; interaction++
            if (hidden) {
                controlCursor = 0; withFrameNanos { }; withFrameNanos { }
                transportFocus.requestFocus()
                if (action == PlayerRemoteAction.Left || action == PlayerRemoteAction.Right) continue
            }
            when (action) {
                PlayerRemoteAction.Left, PlayerRemoteAction.Right -> {
                    controlCursor = (controlCursor + if (action == PlayerRemoteAction.Right) 1 else -1).coerceIn(0, 3)
                    controlTargets[controlCursor].requestFocus()
                }
                PlayerRemoteAction.Select -> when (controlCursor) {
                    0 -> if (model.state.value.playback != null) model.togglePause()
                    1 -> model.toggleMute()
                    2 -> { menu = true; page = null }
                    3 -> onFullscreen()
                }
                else -> Unit
            }
        }
    }
    val controlModifier = if (tv) Modifier.focusProperties { canFocus = tvActive } else Modifier

    var sourceTime by remember { mutableDoubleStateOf(0.0) }
    var cues by remember { mutableStateOf<List<PhoneCue>>(emptyList()) }
    var captionStatus by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(state.playback?.revision, state.subtitleIndex, state.subtitleFileId) {
        cues = emptyList(); captionStatus = null
        val playback = state.playback
        val index = state.subtitleIndex
        if (playback != null && index != null && state.subtitleFileId == playback.mediaFileId) {
            captionStatus = "Preparing subtitles…"
            try { cues = subtitleCues(playback, state.channel!!.id, index); captionStatus = null }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { captionStatus = "Subtitles unavailable" }
        }
    }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(state.playback?.revision, state.paused) {
        while (true) { if (!state.paused) now = System.currentTimeMillis(); delay(1000) }
    }
    val program = state.channel?.programs?.find {
        now >= Instant.parse(it.startsAt).toEpochMilli() && now < Instant.parse(it.endsAt).toEpochMilli()
    }
    val duration = program?.let { Instant.parse(it.endsAt).toEpochMilli() - Instant.parse(it.startsAt).toEpochMilli() } ?: 0L
    val elapsed = program?.let { (now - Instant.parse(it.startsAt).toEpochMilli()).coerceIn(0, duration) } ?: 0L
    LaunchedEffect(visible, interaction, menu, state.paused, state.playback?.revision) {
        if (visible && (!tv || fullscreen) && !menu && !state.paused && state.playback != null) { delay(5000); visible = false }
    }
    BackHandler(enabled = tv && fullscreen) {
        if (page != null) page = null else if (menu) menu = false
        else if (visible) visible = false
        else onExitFullscreen() // A second fast Back must not be swallowed before handler recomposition.
    }
    val silent = state.muted
    fun interact(action: () -> Unit) { interaction++; visible = true; action() }
    val swipeEnabled = fullscreen && !pictureInPicture && !menu && !tv
    val swipeThreshold = with(density) { 64.dp.toPx() }
    // Wait for release and require deliberate travel; taps and short drags do not tune.
    val swipeModifier = if (swipeEnabled) Modifier.pointerInput(model, swipeThreshold) {
        var travel = 0f
        detectVerticalDragGestures(
            onDragStart = { travel = 0f },
            onVerticalDrag = { change, amount -> change.consume(); travel += amount },
            onDragCancel = { travel = 0f },
            onDragEnd = {
                if (kotlin.math.abs(travel) >= swipeThreshold) {
                    interact { model.stepChannel(if (travel < 0f) 1 else -1) }
                }
                travel = 0f
            },
        )
    } else Modifier
    Box(modifier.background(Color.Black).then(swipeModifier).then(if (tv && !fullscreen) Modifier.focusProperties { canFocus = false } else Modifier).then(if (tv) Modifier else Modifier.clickable { visible = !visible })) {
        state.playback?.let { playback ->
            NativeVideo(playback, model, Modifier.fillMaxSize(), state.paused,
                if (state.muted) 0f else 1f, onPosition = { sourceTime = it }, onTap = { if (!tv || fullscreen) visible = !visible })
        } ?: Text(state.error ?: "Preparing channel…", Modifier.align(Alignment.Center), color = Color.White)
        if (!pictureInPicture) captionStatus?.let { message -> Text(message,
            Modifier.align(Alignment.TopEnd).padding(4.dp).background(Color.Black.copy(alpha = 0.75f)).padding(4.dp),
            color = Color.White, style = MaterialTheme.typography.bodySmall) }
        val caption = cues.filter { sourceTime >= it.start && sourceTime < it.end }.joinToString("\n") { it.text }
        // Keep captions near the lower edge; reserve overlay clearance only while controls are shown.
        val controlsShown = !pictureInPicture && (!tv || fullscreen) && (visible || (!tv && state.paused) || state.playback == null)
        LaunchedEffect(controlsShown) { onControlsVisibility(controlsShown) }
        LaunchedEffect(controlsShown, fullscreen, state.playback?.revision, tvActive) {
            if (tv && tvActive && controlsShown && state.playback != null && !menu) {
                controlCursor = 0
                withFrameNanos { }
                transportFocus.requestFocus()
            }
        }
        val subtitleLineHeight = with(density) { 20.sp.toDp() }
        val captionInset = if (pictureInPicture) 4.dp else
            ((if (controlsShown) with(density) { controlsHeight.toDp() } + 8.dp else 56.dp) - subtitleLineHeight * 1.5f).coerceAtLeast(4.dp)
        if (caption.isNotBlank()) Text(caption, Modifier.align(Alignment.BottomCenter).padding(start = 4.dp, end = 4.dp, bottom = captionInset)
            .background(Color.Black.copy(alpha = 0.6f)).padding(4.dp), color = Color.White, fontSize = if (pictureInPicture) 10.sp else 16.sp, lineHeight = if (pictureInPicture) 12.sp else 20.sp)
        if (controlsShown) {
            // Match the web touch layout: gradient and shared station/program metadata above the transport row.
            Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth().onPreviewKeyEvent { event ->
                // Remote navigation extends the overlay timeout while leaving focus to its controls.
                if (tv && event.type == KeyEventType.KeyDown) interaction++
                false
            }.onSizeChanged { controlsHeight = it.height }
                .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.85f))))
                .padding(if (fullscreen) WindowInsets.safeDrawing.asPaddingValues() else PaddingValues(0.dp))
                .padding(horizontal = if (fullscreen) 16.dp else 4.dp, vertical = 4.dp),
                verticalArrangement = Arrangement.spacedBy(if (fullscreen) 4.dp else 0.dp)) {
                // Both phone sizes share tune metadata; reserve at most 30% for the station so titles stay readable.
                if (state.playback != null) BoxWithConstraints(Modifier.fillMaxWidth()) {
                    val stationWidth = if (fullscreen) minOf(140.dp, maxWidth * 0.3f) else maxWidth * 0.3f
                    Row(Modifier.fillMaxWidth().padding(top = if (fullscreen) 12.dp else 8.dp, bottom = if (fullscreen) 4.dp else 0.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.widthIn(max = stationWidth).padding(end = if (fullscreen) 12.dp else 8.dp)) {
                            Text(state.channel?.number?.toString()?.padStart(3, '0').orEmpty(), color = Color.White,
                                fontSize = if (fullscreen) 22.sp else 18.sp, fontWeight = FontWeight.Bold,
                                maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(state.channel?.name.orEmpty(), color = Color.White, style = MaterialTheme.typography.bodySmall,
                                maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                        Box(Modifier.width(1.dp).height(36.dp).background(Color.White.copy(alpha = 0.45f)))
                        Column(Modifier.padding(start = if (fullscreen) 12.dp else 8.dp).weight(1f)) {
                            state.playback.seriesTitle.takeIf { it.isNotBlank() }?.let {
                                Text(cleanPlayerTitle(it), color = Color.White, fontSize = if (fullscreen) 22.sp else 16.sp,
                                    fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                            Text(state.playback.let { it.episodeLabel + cleanPlayerTitle(it.title) }, color = Color.White,
                                fontSize = if (fullscreen) MaterialTheme.typography.titleMedium.fontSize else if (state.playback.seriesTitle.isBlank()) 16.sp else 12.sp,
                                fontWeight = if (!fullscreen && state.playback.seriesTitle.isBlank()) FontWeight.SemiBold else FontWeight.Normal,
                                maxLines = if (fullscreen) 2 else 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
                LinearProgressIndicator(progress = { if (duration > 0) elapsed.toFloat() / duration else 0f },
                        modifier = Modifier.fillMaxWidth().height(4.dp), color = Color(0xff389bea), trackColor = Color.White.copy(alpha = 0.3f))
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    WebControl(if (state.paused) "Play" else "Pause", if (state.paused) "M8 5v14l11-7z" else "M6 5h4v14H6zm8 0h4v14h-4z", state.playback != null, if (tv) controlModifier.focusProperties { left = FocusRequester.Cancel }.focusRequester(transportFocus).onFocusChanged { if (it.isFocused) controlCursor = 0 } else Modifier) { interact(model::togglePause) }
                    Text("${clock(elapsed)} / ${clock(duration)}", color = Color.White, fontSize = 10.sp)
                    Spacer(Modifier.weight(1f))
                    WebControl(if (silent) "Unmute" else "Mute", if (silent) "M3 9v6h4l5 5V4L7 9H3zm13.6 3 2.4 2.4-1.4 1.4-2.4-2.4-2.4 2.4-1.4-1.4 2.4-2.4-2.4-2.4 1.4-1.4 2.4 2.4 2.4-2.4 1.4 1.4z" else "M3 9v6h4l5 5V4L7 9H3zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4z", modifier = controlModifier.then(if (tv) Modifier.focusRequester(muteFocus).onFocusChanged { if (it.isFocused) controlCursor = 1 } else Modifier)) { interact { model.toggleMute(); menu = false } }
                    Box {
                        WebControl("Settings", "M12 8a2 2 0 1 0 0-4 2 2 0 0 0 0 4zm0 6a2 2 0 1 0 0-4 2 2 0 0 0 0 4zm0 6a2 2 0 1 0 0-4 2 2 0 0 0 0 4z", modifier = controlModifier.then(if (tv) Modifier.focusRequester(settingsFocus).onFocusChanged { if (it.isFocused) controlCursor = 2 } else Modifier)) { interact { menu = !menu; page = null } }
                        PlayerPopup(expanded = menu, onDismiss = { if (page != null) page = null else menu = false }) {
                                key(page) {
                                when (page) {
                                    null -> listOf("Quality", "Audio Tracks", "Subtitles").forEach { label ->
                                        PlayerMenuItem(text = { Text(playerMenuLabel(label, state)) }, onClick = { interact { page = label } }, autofocus = tv && label == "Quality")
                                    }
                                    "Quality" -> {
                                        PlayerMenuItem(text = { Text("‹ Quality") }, onClick = { page = null })
                                        (listOf(0) + (listOf(1080, 720, 480, 360) + state.quality).distinct().sortedDescending().filter { it > 0 && (it <= (state.playback?.sourceHeight ?: 720) || it == state.quality) }).forEach { height ->
                                            PlayerMenuItem(text = { Text((if (height == state.quality) "✓ " else "") + if (height == 0) "Auto" else "${height}p") },
                                                onClick = { menu = false; interact { model.setQuality(height) } }, autofocus = tv && height == state.quality)
                                        }
                                    }
                                    "Audio Tracks" -> {
                                        PlayerMenuItem(text = { Text("‹ Audio Tracks") }, onClick = { page = null })
                                        if (state.playback?.audioTracks.isNullOrEmpty()) PlayerMenuItem(text = { Text("No audio tracks") }, enabled = false, onClick = {})
                                        state.playback?.audioTracks?.preferredFirst(state.profile?.language)?.forEach { track ->
                                            PlayerMenuItem(text = { Text((if (track.index == state.playback?.selectedAudioIndex) "✓ " else "") + track.label) }, onClick = { menu = false; interact { model.setAudio(track) } }, autofocus = tv && track.index == state.playback?.selectedAudioIndex)
                                        }
                                    }
                                    "Subtitles" -> {
                                        PlayerMenuItem(text = { Text("‹ Subtitles") }, onClick = { page = null })
                                        PlayerMenuItem(text = { Text((if (state.subtitleIndex == null) "✓ " else "") + "Off") }, onClick = { menu = false; interact { model.setSubtitle(null) } }, autofocus = tv && state.subtitleIndex == null)
                                        state.playback?.subtitleTracks?.preferredFirst(state.profile?.subtitleLanguage)?.forEach { track ->
                                            PlayerMenuItem(text = { Text((if (track.index == state.subtitleIndex) "✓ " else "") + track.label) }, onClick = { menu = false; interact { model.setSubtitle(track.index) } }, autofocus = tv && track.index == state.subtitleIndex)
                                        }
                                        captionStatus?.let { text -> PlayerMenuItem(text = { Text(text) }, enabled = false, onClick = {}) }
                                    }
                                }
                                }
                        }
                    }
                    WebControl(if (fullscreen) "Exit fullscreen" else "Fullscreen", "M5 5h5v2H7v3H5V5zm9 0h5v5h-2V7h-3V5zM5 14h2v3h3v2H5v-5zm12 0h2v5h-5v-2h3v-3z", modifier = controlModifier.then(if (tv) Modifier.focusProperties { right = FocusRequester.Cancel }.focusRequester(fullscreenFocus).onFocusChanged { if (it.isFocused) controlCursor = 3 } else Modifier)) { interact(onFullscreen) }
                }
            }
        }
    }
}

/** Use the web SVG geometry with a 48dp touch target rather than text transport buttons. */
@Composable
private fun WebControl(label: String, svg: String, enabled: Boolean = true, modifier: Modifier = Modifier, action: () -> Unit) {
    val vector = remember(svg) {
        ImageVector.Builder(defaultWidth = 18.dp, defaultHeight = 18.dp, viewportWidth = 24f, viewportHeight = 24f)
            .addPath(PathParser().parsePathString(svg).toNodes(), fill = androidx.compose.ui.graphics.SolidColor(Color.White)).build()
    }
    IconButton(onClick = action, enabled = enabled, modifier = modifier.size(48.dp).tvFocusOutline()) {
        Icon(vector, contentDescription = label, tint = Color.White, modifier = Modifier.size(18.dp))
    }
}

// Schedule progress is informational; arbitrary seeking would break shared live television timing.
private fun clock(milliseconds: Long): String {
    val seconds = milliseconds / 1000
    return if (seconds >= 3600) "%d:%02d:%02d".format(seconds / 3600, seconds / 60 % 60, seconds % 60)
        else "%d:%02d".format(seconds / 60, seconds % 60)
}

/** Anchor measured popup content above its transport icon; translucent fill leaves the video visible. */
@Composable
private fun PlayerPopup(expanded: Boolean, onDismiss: () -> Unit,
    content: @Composable ColumnScope.() -> Unit) {
    if (!expanded) return
    val position = remember(Unit) {
        object : PopupPositionProvider {
            override fun calculatePosition(anchorBounds: IntRect, windowSize: IntSize,
                layoutDirection: LayoutDirection, popupContentSize: IntSize): IntOffset {
                val x = anchorBounds.right - popupContentSize.width
                return IntOffset(x.coerceIn(0, (windowSize.width - popupContentSize.width).coerceAtLeast(0)),
                    (anchorBounds.top - popupContentSize.height).coerceAtLeast(0))
            }
        }
    }
    Popup(popupPositionProvider = position, onDismissRequest = onDismiss,
        properties = PopupProperties(focusable = true)) {
        Surface(color = Color(0xff151515).copy(alpha = 0.9f), contentColor = Color.White,
            shape = RoundedCornerShape(4.dp), shadowElevation = 0.dp) {
            Column(Modifier.heightIn(max = 240.dp).verticalScroll(rememberScrollState()).padding(vertical = 4.dp), content = content)
        }
    }
}

/** Compact menu rows retain distinct clickable choices without Material's extra vertical spacing. */
@Composable
private fun PlayerMenuItem(text: @Composable () -> Unit, onClick: () -> Unit, enabled: Boolean = true, autofocus: Boolean = false) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(autofocus) { if (autofocus) { withFrameNanos { }; focus.requestFocus() } }
    Box(Modifier.focusRequester(focus).tvFocusOutline().widthIn(min = 128.dp, max = 240.dp).clickable(enabled = enabled, onClick = onClick)
        .padding(horizontal = 12.dp, vertical = 8.dp)) {
        ProvideTextStyle(MaterialTheme.typography.bodyMedium.copy(color = Color.White.copy(alpha = if (enabled) 1f else 0.5f))) { text() }
    }
}

/** Match the WebUI's presentation cleanup; stored metadata remains untouched. */
private fun cleanPlayerTitle(value: String): String {
    val clean = value.replace(Regex("\\.(mkv|mp4|avi|mov|m4v|ts|webm)$", RegexOption.IGNORE_CASE), "")
        .replace(Regex("\\[[^\\]]*\\]|\\{[^}]*\\}"), " ")
        .replace(Regex("\\b(480p|720p|1080[pi]|2160p|remux|bluray|blu-ray|web-dl|webrip|hdtv|x26[45]|h26[45]|hevc|eac3|aac|dts)\\b.*$", RegexOption.IGNORE_CASE), "")
        .replace(Regex("[._]"), " ").replace(Regex("[^\\p{L}\\p{M}\\p{N}\\s,:'-]"), "")
        .replace(Regex("\\s+"), " ").trim()
    return String(clean.codePoints().limit(50).toArray(), 0, minOf(50, clean.codePointCount(0, clean.length))).trimEnd()
}
