package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.clickable
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.BringIntoViewSpec
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.foundation.Image
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.layout.ContentScale
import android.app.PictureInPictureParams
import android.os.Build
import android.util.Rational
import android.os.Bundle
import android.util.TypedValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import android.content.res.Configuration
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.border
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.input.key.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.delay
import okhttp3.HttpUrl.Companion.toHttpUrl
import java.net.URI
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Native phone shell; credentials exist only while the sign-in form is on screen. */
class MainActivity : ComponentActivity() {
    private var pip by mutableStateOf(false)
    private var pipEligible = false
    private val pipSupported get() = packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_PICTURE_IN_PICTURE)
    override fun onPictureInPictureModeChanged(isInPictureInPictureMode: Boolean, newConfig: Configuration) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        pip = isInPictureInPictureMode
    }
    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        if (pipSupported && pipEligible && Build.VERSION.SDK_INT < 31) enterPictureInPictureMode(PictureInPictureParams.Builder().setAspectRatio(Rational(16, 9)).build())
    }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            val model: PhoneModel = viewModel()
            val state by model.state.collectAsStateWithLifecycle()
            val navigationDeadline by model.guideNavigationDeadline.collectAsStateWithLifecycle()
            val lifecycleOwner = LocalLifecycleOwner.current
            val refreshSharedProfiles = state.signedIn && state.profile != null
            LaunchedEffect(lifecycleOwner, model, refreshSharedProfiles) {
                if (refreshSharedProfiles) lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
                    while (true) {
                        model.refreshProfiles()
                        delay(300_000)
                    }
                }
            }
            val startupReady = !BuildConfig.IS_TV || (!state.restoringConnection &&
                (state.channel == null || state.playbackReady || state.error != null))
            PhoneTheme { UpdatePrompt(visible = !pip && (!BuildConfig.IS_TV || startupReady)) }
            StartupPreload(ready = startupReady, showContent = !BuildConfig.IS_TV || !state.restoringConnection) {
                val guideNow = rememberGuideTime(state.channels, navigationDeadline)
                if (BuildConfig.IS_TV) {
                    TvShell(state, model, guideNow)
                    return@StartupPreload
                }
                val owner = LocalLifecycleOwner.current
                var fullscreen by rememberSaveable { mutableStateOf(false) }
                val orientation = LocalConfiguration.current.orientation
                LaunchedEffect(orientation) {
                    if (state.channel != null) {
                        fullscreen = orientation == Configuration.ORIENTATION_LANDSCAPE
                    }
                }
                // Rotation/fullscreen/PiP change presentation only; lifecycle resume handles a released lease.
                LaunchedEffect(state.channel == null) { if (state.channel == null) fullscreen = false }
                DisposableEffect(owner, model) {
                    val observer = LifecycleEventObserver { _, event ->
                        when (event) {
                            Lifecycle.Event.ON_STOP -> model.suspendWatching()
                            Lifecycle.Event.ON_START -> { model.resumeWatching(); model.refreshGuide() }
                            else -> Unit
                        }
                    }
                    owner.lifecycle.addObserver(observer)
                    onDispose { owner.lifecycle.removeObserver(observer) }
                }
                LaunchedEffect(state.playback != null, state.paused) {
                    pipEligible = state.playback != null && !state.paused
                    if (pipSupported && Build.VERSION.SDK_INT >= 31) setPictureInPictureParams(PictureInPictureParams.Builder()
                        .setAspectRatio(Rational(16, 9)).setAutoEnterEnabled(pipEligible).build())
                }
                val expanded = fullscreen || pip
                val view = LocalView.current
                DisposableEffect(fullscreen, view) {
                    val controller = WindowCompat.getInsetsController(window, view)
                    controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                    if (fullscreen) controller.hide(WindowInsetsCompat.Type.systemBars())
                    else controller.show(WindowInsetsCompat.Type.systemBars())
                    onDispose { controller.show(WindowInsetsCompat.Type.systemBars()) }
                }
                BackHandler(enabled = fullscreen || state.settings != null || state.channel != null || state.profile != null) {
                    when {
                        state.settings != null -> model.closeSettings()
                        fullscreen -> fullscreen = false
                        state.channel != null -> model.changeProfile()
                        else -> model.changeProfile()
                    }
                }
                LaunchedEffect(state.profile?.id) {
                    while (state.profile != null) { delay(GUIDE_REFRESH_INTERVAL_MS); model.refreshGuide() }
                }
                PhoneTheme {
                    val palette = phonePalette
                    val choosingProfile = state.signedIn && !state.mustChangePassword && state.profile == null
                    Scaffold(contentWindowInsets = if (expanded) WindowInsets(0, 0, 0, 0) else WindowInsets.safeDrawing) { insets ->
                        Column(Modifier.fillMaxSize().padding(insets).padding(if (expanded) 0.dp else 8.dp),
                            verticalArrangement = if (choosingProfile) Arrangement.spacedBy(24.dp, Alignment.CenterVertically) else Arrangement.spacedBy(if (expanded) 0.dp else 4.dp)) {
                            // Center the chooser as one group, keeping generous gaps between profile rows.
                            if (!expanded) Surface(
                                modifier = Modifier.fillMaxWidth(),
                                color = palette.surface, shape = MaterialTheme.shapes.medium) {
                                if (state.profile != null) Row(Modifier.fillMaxWidth().padding(8.dp),
                                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                                    PhoneBrand()
                                    ProfileMenu(state, model)
                                } else Column(Modifier.fillMaxWidth().padding(8.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                                    PhoneBrand()
                                    if (choosingProfile) Text("Who’s Watching?", style = MaterialTheme.typography.headlineMedium)
                                }
                            }
                            if (!expanded) {
                                state.error?.let { message ->
                                    Text(message, color = MaterialTheme.colorScheme.error)
                                    TextButton(onClick = model::dismissError) { Text("Dismiss") }
                                }
                                if (state.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                            }
                            when {
                                !state.signedIn -> ServerConnectScreen(state, model, Modifier.weight(1f))
                                state.mustChangePassword -> AccountScreen(state, model, forced = true)
                                state.settings == "account" -> AccountScreen(state, model)
                                state.settings == "admin" -> AdminSettingsScreen(state, model, Modifier.weight(1f))
                                state.settings == "profile" -> ProfileSettingsScreen(state, model)
                                state.settings == "dashboard" -> DashboardScreen(state, model, Modifier.weight(1f))
                                state.profile == null -> ProfileScreen(state, model)
                                else -> {
                                    if (state.channel != null) {
                                        // Keep the player at the same Compose call site; fullscreen changes bounds, not its identity.
                                        val playerModifier = if (expanded) Modifier.fillMaxWidth().weight(1f)
                                            else Modifier.fillMaxWidth().aspectRatio(16f / 9f)
                                        PhonePlayer(state, model, playerModifier, expanded, pictureInPicture = pip,
                                            onFullscreen = { fullscreen = !fullscreen })
                                    }
                                    if (!expanded) ChannelGuide(state, model, palette, Modifier.weight(1f), now = guideNow)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

/** Profile administration is separate from the account's Owner authorization. */
@Composable
internal fun ProfileMenu(state: PhoneState, model: PhoneModel, modifier: Modifier = Modifier, openSignal: Int = 0, onExitApp: (() -> Unit)? = null, onMenuBack: (() -> Unit)? = null, onMenuNavigation: () -> Unit = {}, onMenuDismiss: () -> Unit = {}) {
    var open by remember { mutableStateOf(false) }
    var consumedSignal by remember { mutableIntStateOf(openSignal) }
    val switchFocus = remember { FocusRequester() }
    var menuBackDown by remember { mutableStateOf(false) }
    LaunchedEffect(openSignal) {
        if (openSignal > consumedSignal) open = true
        consumedSignal = openSignal
    }
    LaunchedEffect(open) { if (open && BuildConfig.IS_TV) { withFrameNanos { }; switchFocus.requestFocus() } }
    Box {
        TextButton(onClick = { open = true }, modifier = modifier) { ProfileName(state.profile!!, model) }
        DropdownMenu(expanded = open, onDismissRequest = { open = false; onMenuDismiss() },
            properties = androidx.compose.ui.window.PopupProperties(focusable = true, dismissOnBackPress = onMenuBack == null),
            modifier = Modifier.onPreviewKeyEvent { event ->
                if (event.key == Key.Back && onMenuBack != null) {
                    // A popup opened by Back can receive that opening press's trailing
                    // KeyUp. Only a new down/up pair belongs to the menu itself.
                    if (event.type == KeyEventType.KeyDown) menuBackDown = true
                    if (event.type == KeyEventType.KeyUp && menuBackDown) {
                        menuBackDown = false
                        open = false
                        onMenuBack()
                    }
                    true
                } else {
                    if (event.type == KeyEventType.KeyDown) onMenuNavigation()
                    false
                }
            }) {
            DropdownMenuItem(text = { Text("Switch Profile") }, modifier = Modifier.focusRequester(switchFocus).tvFocusOutline(), onClick = { open = false; model.changeProfile() })
            DropdownMenuItem(text = { Text("Profile") }, modifier = Modifier.tvFocusOutline(), onClick = { open = false; model.openSettings("profile") })
            if (state.profile?.role == "admin") DropdownMenuItem(text = { Text("Settings") }, modifier = Modifier.tvFocusOutline(), onClick = { open = false; model.openSettings("admin") })
            DropdownMenuItem(text = { Text("Sign Out") }, modifier = Modifier.tvFocusOutline(), onClick = { open = false; model.logout() })
            if (BuildConfig.IS_TV && onExitApp != null) DropdownMenuItem(
                text = { Text("Exit App") }, modifier = Modifier.tvFocusOutline(),
                onClick = { open = false; onExitApp() })
        }
    }
}

/** Password values remain in memory and disappear when this screen is removed. */
@Composable
internal fun AccountScreen(state: PhoneState, model: PhoneModel, forced: Boolean = false) {
    var current by remember { mutableStateOf("") }
    var username by remember(state.username) { mutableStateOf(state.username) }
    var password by remember { mutableStateOf("") }
    var confirmation by remember { mutableStateOf("") }
    LaunchedEffect(state.notice) { if (state.notice != null) { current = ""; password = ""; confirmation = "" } }
    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item { Text(if (forced) "Change Password" else "Admin Settings", style = MaterialTheme.typography.headlineSmall) }
        if (forced) item { Text("Change your password before continuing.") }
        if (state.passwordLocked) item { Text("Your account’s username and password are locked. Contact an Owner for changes.") }
        if (!state.passwordLocked) item { AccountPasswordField("Current Password", current, { current = it }) }
        if (!forced) item { OutlinedTextField(username, { username = it }, readOnly = state.passwordLocked,
            label = { Text("Username") }, singleLine = true, modifier = Modifier.fillMaxWidth()) }
        if (!state.passwordLocked) {
            item { AccountPasswordField("New Password", password, { password = it }) }
            item { AccountPasswordField("Confirm New Password", confirmation, { confirmation = it }) }
            item { Button(onClick = { model.saveAccount(current, username, password, confirmation) },
                enabled = !state.busy && current.isNotEmpty() && username.isNotBlank() && (!forced || password.isNotEmpty())) { Text("Save Changes") } }
        }
        state.notice?.let { item { Text(it) } }
        item { TextButton(onClick = if (forced) model::logout else model::closeSettings, enabled = !state.busy) { Text(if (forced) "Sign Out" else "Back") } }
    }
}

@Composable
private fun AccountPasswordField(label: String, value: String, changed: (String) -> Unit) {
    var visible by remember { mutableStateOf(false) }
    OutlinedTextField(value, changed, label = { Text(label) }, singleLine = true,
        visualTransformation = if (visible) VisualTransformation.None else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password), modifier = Modifier.fillMaxWidth(),
        trailingIcon = { TextButton(onClick = { visible = !visible }) { Text(if (visible) "Hide" else "Show") } })
}

@Composable
internal fun ProfileScreen(state: PhoneState, model: PhoneModel, modifier: Modifier = Modifier) {
    var selected by remember { mutableStateOf<ViewerProfile?>(null) }
    var pin by remember { mutableStateOf("") }
    val firstProfile = remember { FocusRequester() }
    var focusedInitialProfile by remember { mutableStateOf(false) }
    var profilesLoaded by remember { mutableStateOf(false) }
    LaunchedEffect(model) {
        model.refreshProfilesOnEntry()
        profilesLoaded = true
    }
    LaunchedEffect(state.profiles.firstOrNull()?.id, state.busy, profilesLoaded) {
        if (BuildConfig.IS_TV && profilesLoaded && state.profiles.isNotEmpty() && !state.busy && !focusedInitialProfile) {
            firstProfile.requestFocus()
            focusedInitialProfile = true
        }
    }
    val linkPixels = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_PT, 2f, LocalContext.current.resources.displayMetrics)
    val linkIncrease = with(LocalDensity.current) { linkPixels.toSp() }
    val linkStyle = MaterialTheme.typography.labelLarge.copy(
        fontSize = (MaterialTheme.typography.labelLarge.fontSize.value + linkIncrease.value).sp)
    // A single scrollable, centered chooser retains room for long profile lists and small screens.
    LazyColumn(modifier.fillMaxWidth().then(if (BuildConfig.IS_TV) Modifier.fillMaxHeight() else Modifier).padding(horizontal = 8.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(24.dp, if (BuildConfig.IS_TV) Alignment.CenterVertically else Alignment.Top)) {
        if (BuildConfig.IS_TV) item { Text("Select Profile", style = MaterialTheme.typography.headlineMedium) }
        items(state.profiles, key = { it.id }) { profile ->
            Button(onClick = { if (profile.hasPin) { selected = profile; pin = "" } else model.selectProfile(profile, "") },
                enabled = profilesLoaded && !state.busy, modifier = Modifier.fillMaxWidth(if (BuildConfig.IS_TV) 0.5f else 1f).heightIn(min = 48.dp)
                    .then(if (BuildConfig.IS_TV && profile.id == state.profiles.firstOrNull()?.id) Modifier.focusRequester(firstProfile) else Modifier)
                    .tvFocusOutline(), shape = RoundedCornerShape(4.dp)) {
                ProfileName(profile, model)
            }
        }
        if (state.profiles.isEmpty()) item {
            Text("Create a viewer profile in your server’s web interface, then reopen this screen.", textAlign = TextAlign.Center)
        }
        item {
            Row(horizontalArrangement = Arrangement.Center) {
                TextButton(onClick = model::logout, enabled = !state.busy) { Text("Sign Out", style = linkStyle) }
            }
        }
    }
    selected?.let { profile ->
        AlertDialog(onDismissRequest = { selected = null; pin = "" }, title = { Text("PIN for ${profile.name}") },
            text = { OutlinedTextField(pin, { pin = it }, label = { Text("Profile PIN") }, singleLine = true,
                visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword)) },
            confirmButton = { TextButton(onClick = { model.selectProfile(profile, pin); pin = ""; selected = null }, enabled = !state.busy && pin.isNotEmpty()) { Text("Continue") } },
            dismissButton = { TextButton(onClick = { pin = ""; selected = null }) { Text("Cancel") } })
    }
}

@Composable
@OptIn(ExperimentalFoundationApi::class)
internal fun ChannelGuide(state: PhoneState, model: PhoneModel, palette: PhonePalette, modifier: Modifier, now: Long, onTune: (GuideChannel) -> Unit = model::watch) {
    val channels = state.channels
    val format = remember(state.profile?.timeFormat) { DateTimeFormatter.ofPattern(when (state.profile?.timeFormat) {
        "24h" -> "HH:mm"; "12h-padded" -> "hh:mma"; else -> "h:mma"
    }, Locale.US).withZone(ZoneId.systemDefault()) }
    val rows = rememberLazyListState(initialFirstVisibleItemIndex = channels.indexOfFirst { it.id == state.channel?.id }.coerceAtLeast(0))
    val scroll = rememberScrollState()
    // Labels and program geometry share a local half-hour anchor, while Now uses the actual clock.
    val textMeasurer = rememberTextMeasurer()
    val density = LocalDensity.current
    val currentProgramFocus = remember { FocusRequester() }
    val focusChannel = channels.firstOrNull { it.id == state.channel?.id }
        ?: channels.firstOrNull { channel -> channel.programs.any {
            Instant.parse(it.startsAt).toEpochMilli() <= now && Instant.parse(it.endsAt).toEpochMilli() > now
        } }
    val focusProgram = focusChannel?.liveProgram(now)
    LaunchedEffect(focusChannel?.id, focusProgram?.startsAt, now / GUIDE_REFRESH_INTERVAL_MS) {
        if (focusChannel != null && focusProgram != null) {
            rows.scrollToItem(channels.indexOf(focusChannel))
            scroll.scrollTo(0)
            withFrameNanos { } // Allow a lazily composed airing to attach its focus target.
            currentProgramFocus.requestFocus()
        }
    }
    val localNow = Instant.ofEpochMilli(now).atZone(ZoneId.systemDefault())
    val origin = localNow.withMinute(localNow.minute / 30 * 30).withSecond(0).withNano(0)
        .toInstant().toEpochMilli()
    LaunchedEffect(origin) { scroll.scrollTo(0) }
    val minutesToDp = 6f
    val width = (12 * 60 * minutesToDp).dp
    // Use nearest-edge focus scrolling, not a device's TV-style center pivot.
    CompositionLocalProvider(LocalBringIntoViewSpec provides remember { object : BringIntoViewSpec {} }) {
    Column(modifier.guideNavigationInput(model::noteGuideNavigation, model::resetGuideNavigation)) {
        Row(Modifier.fillMaxWidth().background(palette.alternate).testTag("guide-timeline").semantics { stateDescription = origin.toString() }) {
            GuideClock(state.profile?.timeFormat, palette, Modifier.width(100.dp).padding(4.dp))
            Row(Modifier.horizontalScroll(scroll).width(width)) {
                repeat(24) { column -> Text(format.format(Instant.ofEpochMilli(origin + column * 30 * 60_000L)),
                    Modifier.width((30 * minutesToDp).dp).padding(4.dp), color = palette.muted) }
            }
        }
        BoxWithConstraints(Modifier.weight(1f)) {
        LazyColumn(state = rows, verticalArrangement = Arrangement.spacedBy(4.dp),
            modifier = Modifier.testTag("guide-rows"),
            contentPadding = PaddingValues(bottom = (maxHeight - 72.dp).coerceAtLeast(0.dp))) {
            if (channels.isEmpty()) item { Text("No channels are available for this profile.") }
            items(channels, key = { it.id }) { channel ->
                Row(Modifier.fillMaxWidth().height(72.dp).testTag("guide-channel-${channel.id}")) {
                    Surface(onClick = { onTune(channel) }, color = palette.alternate,
                        shape = MaterialTheme.shapes.small, modifier = Modifier.width(100.dp).fillMaxHeight().tvFocusOutline()) {
                        Column(Modifier.padding(4.dp), verticalArrangement = Arrangement.Center) {
                            Text(channel.number.toString(), style = MaterialTheme.typography.titleLarge.copy(fontSize = 18.sp))
                            Text(channel.name, maxLines = 2, style = MaterialTheme.typography.bodySmall)
                        }
                    }
                    Box(Modifier.weight(1f).horizontalScroll(scroll)) {
                        Box(Modifier.width(width).fillMaxHeight()) {
                            (channel.previousPrograms + channel.programs).forEach { program ->
                                val begin = Instant.parse(program.startsAt).toEpochMilli()
                                val end = Instant.parse(program.endsAt).toEpochMilli()
                                val left = ((begin - origin).coerceAtLeast(0) / 60_000f * minutesToDp).dp
                                val block = ((end.coerceAtMost(origin + 12 * 60 * 60_000L) - begin.coerceAtLeast(origin)) / 60_000f * minutesToDp).coerceAtLeast(0f).dp
                                if (block.value > 0) {
                                    val current = begin <= now && end > now
                                    val past = end <= now
                                    val range = "${format.format(Instant.ofEpochMilli(begin))} - ${format.format(Instant.ofEpochMilli(end))}"
                                    val showText = !past || textMeasurer.measure(range, style = MaterialTheme.typography.labelSmall).size.width <= with(density) { (block - 11.dp).toPx() }
                                    Surface(modifier = Modifier.offset(x = left).width(block).fillMaxHeight().padding(start = 3.dp)
                                            .then(if (channel.id == focusChannel?.id && program == focusProgram) Modifier.focusRequester(currentProgramFocus) else Modifier)
                                            .tvFocusOutline()
                                            .then(if (current && channel.id == state.channel?.id) Modifier.testTag("guide-watched-airing").semantics { stateDescription = program.startsAt } else Modifier)
                                            .then(if (current && channel.id == focusChannel?.id) Modifier.border(2.dp, Color.Red, MaterialTheme.shapes.small) else Modifier)
                                            .then(if (past) Modifier else Modifier.clickable { onTune(channel) }),
                                        color = if (past) Color.Gray.copy(alpha = 0.28f).compositeOver(palette.background) else if (current) palette.now else palette.surface, shape = MaterialTheme.shapes.small) {
                                        if (showText) Column(Modifier.padding(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                            Text(range,
                                                color = if (current) palette.nowText else palette.muted, style = MaterialTheme.typography.labelSmall, maxLines = 1, softWrap = false)
                                            Text(program.title, color = if (past) palette.muted else if (current) palette.nowText else palette.text, maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
                                            if (program.episodeNumber > 0) Text("S${program.seasonNumber}E${program.episodeNumber} - ${program.episodeTitle}", color = palette.muted,
                                                maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis, style = MaterialTheme.typography.labelSmall)
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
        }
    }
    }
}

/** Accept only HTTPS origins so API requests cannot inherit credentials or arbitrary paths. */
internal fun serverOrigin(input: String): String? {
    val uri = runCatching { URI(input.trim()) }.getOrNull() ?: return null
    if (!uri.scheme.equals("https", ignoreCase = true) || uri.host.isNullOrBlank() ||
        uri.rawUserInfo != null || uri.rawQuery != null || uri.rawFragment != null ||
        (uri.rawPath != "" && uri.rawPath != "/") || (uri.port != -1 && uri.port !in 1..65535)) return null
    return URI("https", null, uri.host.lowercase(Locale.ROOT), uri.port, null, null, null).toString().toHttpUrl().toString().removeSuffix("/")
}

// Physical-phone loopback identifies the phone itself, not the selected network server.
internal fun isLoopback(host: String): Boolean {
    val normalized = host.lowercase(Locale.ROOT).removeSuffix(".").removePrefix("[").removeSuffix("]")
    return normalized == "localhost" || normalized.endsWith(".localhost") || normalized == "::1" ||
        normalized == "0:0:0:0:0:0:0:1" || normalized == "0.0.0.0" || normalized.startsWith("127.")
}

// Keep the app wordmark and launcher icon on the shared, font-free brand paths.
@Composable
internal fun PhoneBrand(scale: Float = 1f) {
    // Wordmark visible height is 88/601 of its width; icon padding is 80/108.
    val wordmarkHeight = 170.dp * scale * (88f / 601f)
    val iconSize = wordmarkHeight * 1.05f * (108f / 80f)
    Column {
        Row(verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp * scale)) {
            Image(painterResource(R.drawable.ic_brand), contentDescription = null,
                modifier = Modifier.size(iconSize).offset(y = -iconSize * (5.5f / 108f) - wordmarkHeight * .025f),
                contentScale = ContentScale.Fit)
            Image(painterResource(R.drawable.brand_wordmark), contentDescription = "AlreadyOn",
                modifier = Modifier.width(170.dp * scale).height(32.dp * scale), contentScale = ContentScale.Fit)
        }
        Text("Your library, tuned to you", modifier = Modifier.padding(start = iconSize + 8.dp * scale).offset(y = (-4).dp * scale),
            style = MaterialTheme.typography.labelSmall.let { it.copy(fontSize = it.fontSize * scale, lineHeight = it.lineHeight * scale) }, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
