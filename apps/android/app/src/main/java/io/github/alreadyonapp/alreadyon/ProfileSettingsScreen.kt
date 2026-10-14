package io.github.alreadyonapp.alreadyon

import android.graphics.Bitmap
import android.graphics.ImageDecoder
import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.focusGroup
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.input.key.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.text.font.FontWeight
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.ByteArrayOutputStream

/** Web-equivalent settings cards keep drafts local until Save; PIN changes apply separately. */
@Composable
internal fun ProfileSettingsScreen(state: PhoneState, model: PhoneModel) {
    val profile = state.profile ?: return
    val tv = BuildConfig.IS_TV
    var mounted by remember { mutableStateOf(true) }
    DisposableEffect(Unit) { onDispose { mounted = false } }
    val sections = listOf("Display Name", "Icon", "PIN", "Time Format", "Audio Language", "Subtitles")
    var section by remember { mutableStateOf(sections.first()) }
    val sidebarFocus = remember { sections.associateWith { FocusRequester() } }
    val primaryFocus = remember { sections.associateWith { FocusRequester() } }
    var inControls by remember { mutableStateOf(false) }
    // Popups and the keyboard dismiss first; the settings screen then unwinds focus before leaving.
    BackHandler(enabled = tv) {
        if (inControls) sidebarFocus.getValue(section).requestFocus() else model.closeSettings()
    }
    fun entryModifier(title: String) = if (tv) Modifier.focusRequester(primaryFocus.getValue(title)).focusProperties { left = sidebarFocus.getValue(title) } else Modifier
    val cards = rememberLazyListState()
    LaunchedEffect(section) { if (tv) cards.scrollToItem(0) }
    LaunchedEffect(Unit) { if (tv) { withFrameNanos { }; sidebarFocus.getValue(sections.first()).requestFocus() } }
    var name by remember(profile.id) { mutableStateOf(profile.name) }
    var icon by remember(profile.id) { mutableStateOf(profile.icon) }
    var upload by remember(profile.id) { mutableStateOf<ByteArray?>(null) }
    var time by remember(profile.id) { mutableStateOf(profile.timeFormat) }
    var audio by remember(profile.id) { mutableStateOf(profile.language) }
    var subtitleMode by remember(profile.id) { mutableStateOf(profile.subtitleMode) }
    var language by remember(profile.id) { mutableStateOf(profile.subtitleLanguage) }
    var editingPin by remember { mutableStateOf(false) }
    var removePin by remember { mutableStateOf(false) }
    var pin by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var localError by remember { mutableStateOf<String?>(null) }
    var processing by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    fun selectSection(title: String) {
        if (section != title) {
            section = title
            model.dismissNotice()
        }
    }
    fun enterSection(title: String) {
        selectSection(title)
        scope.launch {
            // Sidebar changes mount a different card; wait for its target before transferring focus.
            cards.scrollToItem(0)
            withFrameNanos { }
            primaryFocus.getValue(title).requestFocus()
        }
    }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        if (uri != null) scope.launch {
            processing = true
            try {
                upload = withContext(Dispatchers.IO) {
                    // Decode directly to a bounded image, then crop to the WebUI's 128px square.
                    val bitmap = if (android.os.Build.VERSION.SDK_INT >= 28) ImageDecoder.decodeBitmap(ImageDecoder.createSource(context.contentResolver, uri)) { decoder, info, _ ->
                        val scale = 256f / maxOf(info.size.width, info.size.height)
                        decoder.setTargetSize((info.size.width * scale).toInt().coerceAtLeast(1), (info.size.height * scale).toInt().coerceAtLeast(1))
                        decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
                    }
                    else {
                        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                        context.contentResolver.openInputStream(uri).use { BitmapFactory.decodeStream(it, null, bounds) }
                        require(bounds.outWidth > 0 && bounds.outHeight > 0)
                        val options = BitmapFactory.Options().apply { inSampleSize = (maxOf(bounds.outWidth, bounds.outHeight) / 256).coerceAtLeast(1) }
                        context.contentResolver.openInputStream(uri).use { requireNotNull(BitmapFactory.decodeStream(it, null, options)) }
                    }
                    val side = minOf(bitmap.width, bitmap.height)
                    val square = Bitmap.createBitmap(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side)
                    val small = Bitmap.createScaledBitmap(square, 128, 128, true)
                    ByteArrayOutputStream().use { bytes -> small.compress(Bitmap.CompressFormat.PNG, 100, bytes); bytes.toByteArray() }
                }
                localError = null
            } catch (e: kotlinx.coroutines.CancellationException) { throw e }
            catch (_: Exception) { localError = "Couldn’t read that image." }
            finally { processing = false }
        }
    }
    val changed = name.trim() != profile.name || icon != profile.icon || upload != null || time != profile.timeFormat ||
        audio != profile.language || subtitleMode != profile.subtitleMode || language != profile.subtitleLanguage
    Column(Modifier.fillMaxSize()) {
        if (tv) Row(Modifier.fillMaxWidth().padding(bottom = 16.dp), horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically) {
            Text("Profile Settings", style = MaterialTheme.typography.headlineMedium)
            Text(profile.name, color = phonePalette.muted)
        }
        Row(Modifier.weight(1f)) {
            // TV keeps category navigation visible while only mounting the selected settings card.
            if (tv) Column(Modifier.width(190.dp).padding(end = 20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                sections.forEach { title ->
                    FilledTonalButton(onClick = { enterSection(title) }, modifier = Modifier.fillMaxWidth()
                        .focusRequester(sidebarFocus.getValue(title))
                        .onFocusChanged { if (it.isFocused) selectSection(title) }
                        .onPreviewKeyEvent { event ->
                            if (event.key != Key.DirectionRight) false else {
                                if (event.type == KeyEventType.KeyDown) enterSection(title)
                                true
                            }
                        }.tvFocusOutline(),
                        colors = ButtonDefaults.filledTonalButtonColors(containerColor = if (section == title) phonePalette.alternate else phonePalette.surface)) {
                        Text(title)
                    }
                }
            }
            LazyColumn(Modifier.weight(1f).then(if (tv) Modifier.onFocusChanged { inControls = it.hasFocus }.focusGroup() else Modifier), state = cards, contentPadding = PaddingValues(12.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                item { OutlinedButton(shape = MaterialTheme.shapes.small, onClick = model::closeSettings, modifier = Modifier.tvFocusOutline()) { Text("← Back To Guide") } }
                if (!tv || section == "Display Name") item { SettingsCard("Display Name") { OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, modifier = entryModifier("Display Name").fillMaxWidth().tvFocusOutline()) } }
                if (!tv || section == "Icon") item { SettingsCard("Icon") {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        if (upload == null) ProfileName(profile.copy(name = name, icon = icon), model, avatarSize = 64.dp, showName = false)
                        upload?.let { bytes ->
                            val bitmap = remember(bytes) { BitmapFactory.decodeByteArray(bytes, 0, bytes.size) }
                            Image(bitmap.asImageBitmap(), "Custom icon preview", Modifier.size(64.dp), contentScale = ContentScale.Crop)
                        }
                        OutlinedButton(modifier = Modifier.tvFocusOutline(), shape = MaterialTheme.shapes.small, onClick = { icon = null; upload = null }) { Text("Remove Icon") }
                    }
                    if (!tv) OutlinedButton(shape = MaterialTheme.shapes.small, onClick = { picker.launch("image/*") }, enabled = !processing) { Text("Upload Custom Icon") }
                    // Wrap presets to the available width so every icon remains visible on phones.
                    FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        avatarPresets.forEach { (id, preset) ->
                            FilledTonalButton(onClick = { icon = id; upload = null }, contentPadding = PaddingValues(0.dp),
                                modifier = (if (id == avatarPresets.keys.first()) entryModifier("Icon") else Modifier).size(48.dp).tvFocusOutline().then(if (icon == id && upload == null) Modifier.border(2.dp, Color(0xff389bea), MaterialTheme.shapes.extraLarge) else Modifier),
                                colors = ButtonDefaults.filledTonalButtonColors(containerColor = Color(preset.second))) { Text(preset.first) }
                        }
                    }
                } }
                if (!tv || section == "PIN") item { SettingsCard("PIN") {
                    Text("Status   ${if (profile.hasPin) "PIN Is Set" else "No PIN Set"}")
                    if (editingPin) {
                        OutlinedTextField(pin, { pin = it.filter(Char::isDigit).take(6) }, label = { Text("New PIN (4-6 Digits)") },
                            visualTransformation = androidx.compose.ui.text.input.PasswordVisualTransformation(),
                            keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(keyboardType = androidx.compose.ui.text.input.KeyboardType.NumberPassword), modifier = entryModifier("PIN").tvFocusOutline())
                        OutlinedTextField(confirm, { confirm = it.filter(Char::isDigit).take(6) }, label = { Text("Confirm PIN") },
                            visualTransformation = androidx.compose.ui.text.input.PasswordVisualTransformation(),
                            keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(keyboardType = androidx.compose.ui.text.input.KeyboardType.NumberPassword))
                        Row {
                            Button(modifier = Modifier.tvFocusOutline(), onClick = { model.savePin(pin); pin = ""; confirm = ""; editingPin = false }, enabled = !state.busy && pin.length in 4..6 && pin == confirm) { Text("Save PIN") }
                            TextButton(modifier = Modifier.tvFocusOutline(), onClick = { editingPin = false; pin = ""; confirm = "" }) { Text("Cancel") }
                        }
                    } else Row(Modifier.horizontalScroll(rememberScrollState())) {
                        OutlinedButton(modifier = (if (!profile.hasPin) entryModifier("PIN") else Modifier).tvFocusOutline(), shape = MaterialTheme.shapes.small, onClick = { editingPin = true; removePin = false }) { Text(if (profile.hasPin) "Change PIN" else "Set PIN") }
                        if (profile.hasPin) OutlinedButton(modifier = entryModifier("PIN").tvFocusOutline(), shape = MaterialTheme.shapes.small, onClick = { if (removePin) { model.savePin(null); removePin = false } else removePin = true }, enabled = !state.busy) { Text(if (removePin) "Confirm remove?" else if (tv) "Clear PIN" else "Remove PIN") }
                    }
                } }
                if (!tv || section == "Time Format") item { SettingsCard(if (tv) "Time Format" else "Guide Display") { SettingsChoice(if (tv) "" else "Time Format", time, listOf("12h-padded" to "01:00am-02:00am", "12h" to "1:00am-2:00am", "24h" to "22:00-23:00"), entryModifier("Time Format")) { time = it } } }
                if (!tv || section == "Audio Language") item { SettingsCard(if (tv) "Audio Language" else "Audio") { SettingsChoice(if (tv) "" else "Preferred Audio Language", audio, profileLanguages, entryModifier("Audio Language")) { audio = it } } }
                if (!tv || section == "Subtitles") item { SettingsCard("Subtitles") {
                    SettingsChoice("Subtitle language", language, profileLanguages, entryModifier("Subtitles")) { language = it }
                    SettingsChoice("Subtitles", subtitleMode, listOf("always" to "Always on", "other_audio" to "For other audio", "off" to "Off")) { subtitleMode = it }
                } }
                localError?.let { item { Text(it, color = MaterialTheme.colorScheme.error) } }
                item { Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(modifier = Modifier.tvFocusOutline(), onClick = {
                        val savedSection = section
                        // Save disables this button and inserts a notice; move focus before either can choose a fallback.
                        if (tv) sidebarFocus.getValue(savedSection).requestFocus()
                        val patch = JSONObject()
                        if (name.trim() != profile.name) patch.put("name", name.trim())
                        if (upload == null && icon != profile.icon) patch.put("icon", icon ?: JSONObject.NULL)
                        if (time != profile.timeFormat) patch.put("guideTimeFormat", time)
                        if (audio != profile.language) patch.put("audioLanguage", audio)
                        if (subtitleMode != profile.subtitleMode) patch.put("subtitleMode", subtitleMode)
                        if (language != profile.subtitleLanguage) patch.put("subtitleLanguage", language)
                        model.saveProfile(patch, upload, keepOpen = tv) { saved ->
                            if (!mounted) return@saveProfile
                            // Refresh the draft baseline only after the server accepts every save operation.
                            name = saved.name; icon = saved.icon; upload = null
                            time = saved.timeFormat; audio = saved.language
                            subtitleMode = saved.subtitleMode; language = saved.subtitleLanguage
                            if (tv) {
                                section = savedSection
                                sidebarFocus.getValue(savedSection).requestFocus()
                            }
                        }
                    }, enabled = changed && !state.busy && !processing && name.isNotBlank()) { Text("Save Changes") }
                    OutlinedButton(shape = MaterialTheme.shapes.small, onClick = model::closeSettings, modifier = Modifier.tvFocusOutline()) { Text("Cancel") }
                } }
                state.notice?.let { item { Text(it) } }
            }
        }
    }
}

/** Cards share the native dark palette and adapt the web fields to phone width. */
@Composable
private fun SettingsCard(title: String, content: @Composable ColumnScope.() -> Unit) {
    Surface(color = phonePalette.surface, shape = MaterialTheme.shapes.medium,
        modifier = Modifier.fillMaxWidth().border(1.dp, phonePalette.border, MaterialTheme.shapes.medium)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            content()
        }
    }
}

@Composable
private fun SettingsChoice(label: String, value: String, options: List<Pair<String, String>>, modifier: Modifier = Modifier, changed: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val selected = remember { FocusRequester() }
    LaunchedEffect(open) { if (open && BuildConfig.IS_TV) { withFrameNanos { }; selected.requestFocus() } }
    if (label.isNotBlank()) Text(label, color = phonePalette.muted)
    Box {
        OutlinedButton(shape = MaterialTheme.shapes.small, onClick = { open = true }, modifier = modifier.fillMaxWidth().tvFocusOutline()) { Text((options.find { it.first == value }?.second ?: value) + " ▾") }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            options.forEach { (code, name) -> DropdownMenuItem(text = { Text((if (code == value) "✓ " else "") + name) }, modifier = Modifier.then(if (code == value || (options.none { it.first == value } && code == options.first().first)) Modifier.focusRequester(selected) else Modifier).tvFocusOutline(), onClick = { changed(code); open = false }) }
        }
    }
}

// Phone and TV share alphabetical display names, with English always first.
private val profileLanguages = listOf("en", "es", "fr", "de", "it", "pt", "ja", "ko", "zh", "ru", "nl", "sv", "da", "no", "fi", "pl", "tr", "ar", "he", "hi", "cs", "el", "hu", "th", "uk")
    .map { it to java.util.Locale.forLanguageTag(it).getDisplayLanguage(java.util.Locale.ENGLISH) }
    .sortedWith(compareBy<Pair<String, String>> { it.first != "en" }.thenBy { it.second })
