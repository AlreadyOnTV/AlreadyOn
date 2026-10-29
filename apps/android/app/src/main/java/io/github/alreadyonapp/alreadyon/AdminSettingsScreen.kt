// Profile administration stays account-scoped; update preferences belong to this installation.
package io.github.alreadyonapp.alreadyon

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalInputModeManager
import androidx.compose.ui.input.InputMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.launch
import org.json.JSONObject

@Composable
internal fun AdminSettingsScreen(state: PhoneState, model: PhoneModel, modifier: Modifier = Modifier) {
    if (!state.signedIn || state.mustChangePassword || state.profile?.role != "admin") return
    val cycle by model.releaseCycle.collectAsStateWithLifecycle()
    val tv = BuildConfig.IS_TV
    val sections = listOf("Updates", "Profiles")
    var section by rememberSaveable { mutableStateOf("Updates") }
    val navigation = remember { sections.associateWith { FocusRequester() } }
    val contentFocus = remember { FocusRequester() }
    var inControls by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<ViewerProfile?>(null) }
    var creating by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<ViewerProfile?>(null) }
    var resetting by remember { mutableStateOf<ViewerProfile?>(null) }
    val scope = rememberCoroutineScope()
    val inputMode = LocalInputModeManager.current
    fun selectSection(value: String) { section = value; model.dismissNotice() }
    LaunchedEffect(Unit) {
        if (tv) inputMode.requestInputMode(InputMode.Keyboard)
        model.refreshProfilesOnEntry()
        if (tv) { withFrameNanos { }; navigation.getValue(section).requestFocus() }
    }
    BackHandler(enabled = tv && !creating && editing == null && deleting == null && resetting == null) {
        if (inControls) navigation.getValue(section).requestFocus() else model.closeSettings()
    }
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween) {
            Text("Settings", style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = model::closeSettings, modifier = Modifier.tvFocusOutline()) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Back to Guide")
            }
        }
        if (!tv) PrimaryTabRow(selectedTabIndex = sections.indexOf(section)) {
            sections.forEach { title -> Tab(selected = section == title, onClick = { selectSection(title) }, text = { Text(title) }) }
        }
        Row(Modifier.weight(1f)) {
            if (tv) Column(Modifier.width(190.dp).padding(end = 20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                sections.forEach { title ->
                    FilledTonalButton(onClick = {
                        selectSection(title)
                        scope.launch { withFrameNanos { }; contentFocus.requestFocus() }
                    }, modifier = Modifier.fillMaxWidth().focusRequester(navigation.getValue(title))
                        .focusProperties { right = contentFocus }
                        .onFocusChanged { if (it.isFocused) selectSection(title) }.tvFocusOutline(),
                        colors = ButtonDefaults.filledTonalButtonColors(containerColor = if (section == title) phonePalette.alternate else phonePalette.surface)) {
                        Text(title)
                    }
                }
            }
            key(section) {
                LazyColumn(Modifier.weight(1f).fillMaxHeight().testTag("admin-settings-content")
                    .then(if (tv) Modifier.onFocusChanged { inControls = it.hasFocus }
                        .focusProperties { left = navigation.getValue(section) }.focusGroup() else Modifier),
                    contentPadding = PaddingValues(bottom = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    if (section == "Updates") {
                        item { Text("Release Cycle", style = MaterialTheme.typography.titleLarge) }
                        item {
                            Column(Modifier.fillMaxWidth().selectableGroup()) {
                                ReleaseCycle.entries.forEach { option ->
                                    Row(Modifier.fillMaxWidth().heightIn(min = 56.dp)
                                        .then(if (option == cycle) Modifier.focusRequester(contentFocus) else Modifier)
                                        .selectable(selected = cycle == option, role = Role.RadioButton,
                                            onClick = { model.setReleaseCycle(option) }).tvFocusOutline().padding(horizontal = 8.dp),
                                        verticalAlignment = Alignment.CenterVertically) {
                                        RadioButton(selected = cycle == option, onClick = null)
                                        Spacer(Modifier.width(12.dp))
                                        Text(option.label)
                                    }
                                }
                            }
                        }
                    } else {
                        item {
                            Button(onClick = { model.dismissError(); creating = true }, enabled = !state.busy,
                                modifier = Modifier.focusRequester(contentFocus).tvFocusOutline()) {
                                Icon(Icons.Default.Add, contentDescription = null)
                                Spacer(Modifier.width(8.dp))
                                Text("Add Profile")
                            }
                        }
                        items(state.profiles.sortedBy { it.name.lowercase() }, key = { it.id }) { profile ->
                            Column(Modifier.fillMaxWidth().testTag("managed-profile-${profile.id}"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                ProfileName(profile, model)
                                Text(listOf(if (profile.role == "admin") "Admin" else "Viewer", audienceLabel(profile.tier),
                                    if (profile.hasPin) "PIN Set" else "No PIN").joinToString(" / "), color = phonePalette.muted)
                                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    SettingsAction("Edit ${profile.name}", Icons.Default.Edit, !state.busy) {
                                        model.dismissError(); editing = profile
                                    }
                                    SettingsAction("Delete ${profile.name}", Icons.Default.Delete, !state.busy) {
                                        model.dismissError(); deleting = profile
                                    }
                                    if (profile.hasPin) SettingsAction("Reset PIN for ${profile.name}", Icons.Default.Lock, !state.busy) {
                                        model.dismissError(); resetting = profile
                                    }
                                }
                                HorizontalDivider(color = phonePalette.border)
                            }
                        }
                    }
                    state.notice?.let { item { Text(it) } }
                }
            }
        }
    }
    if (creating || editing != null) ProfileAdminEditor(editing, state.busy, state.error,
        onDismiss = { creating = false; editing = null; model.dismissError() },
        onSave = { name, role, tier ->
            val original = editing
            val saved = { creating = false; editing = null }
            if (original == null) model.createManagedProfile(name, role, tier, saved)
            else {
                val patch = JSONObject()
                if (name != original.name) patch.put("name", name)
                if (role != original.role) patch.put("role", role)
                if (tier != original.tier) patch.put("audienceTier", tier)
                model.updateManagedProfile(original.id, patch, saved)
            }
        })
    deleting?.let { profile ->
        SettingsConfirmation("Delete Profile", "Delete ${profile.name}? This removes its preferences and viewing history.",
            "Delete", state.busy, state.error, onDismiss = { deleting = null; model.dismissError() },
            onConfirm = { model.deleteManagedProfile(profile.id) { deleting = null } })
    }
    resetting?.let { profile ->
        SettingsConfirmation("Reset Profile PIN", "Remove the PIN for ${profile.name}?", "Reset PIN", state.busy, state.error,
            onDismiss = { resetting = null; model.dismissError() },
            onConfirm = { model.resetManagedProfilePin(profile.id) { resetting = null } })
    }
}

private fun audienceLabel(tier: String) = when (tier) { "young_child" -> "Kids"; "teen" -> "Teen"; else -> "Unrestricted" }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SettingsAction(label: String, icon: ImageVector, enabled: Boolean, action: () -> Unit) {
    TooltipBox(positionProvider = TooltipDefaults.rememberPlainTooltipPositionProvider(),
        tooltip = { PlainTooltip { Text(label) } }, state = rememberTooltipState()) {
        IconButton(onClick = action, enabled = enabled, modifier = Modifier.size(48.dp).tvFocusOutline()) {
            Icon(icon, contentDescription = label)
        }
    }
}

@Composable
private fun ProfileAdminEditor(profile: ViewerProfile?, busy: Boolean, error: String?, onDismiss: () -> Unit,
    onSave: (String, String, String) -> Unit) {
    var name by remember { mutableStateOf(profile?.name.orEmpty()) }
    var role by remember { mutableStateOf(profile?.role ?: "viewer") }
    var tier by remember { mutableStateOf(profile?.tier ?: "adult") }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { if (BuildConfig.IS_TV) { withFrameNanos { }; focus.requestFocus() } }
    val trimmed = name.trim()
    val valid = trimmed.isNotEmpty() && trimmed.codePointCount(0, trimmed.length) <= 40 && trimmed.none { Character.isISOControl(it) }
    val changed = profile == null || trimmed != profile.name || role != profile.role || tier != profile.tier
    AlertDialog(onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(if (profile == null) "Add Profile" else "Edit Profile") },
        text = {
            Column(Modifier.heightIn(max = 280.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true,
                    isError = name.isNotEmpty() && !valid, modifier = Modifier.fillMaxWidth().focusRequester(focus).tvFocusOutline())
                SettingsChoice("Role", role, listOf("admin" to "Admin", "viewer" to "Viewer")) { role = it }
                SettingsChoice("Audience", tier, listOf("young_child" to "Kids", "teen" to "Teen", "adult" to "Unrestricted")) { tier = it }
                if (name.isNotEmpty() && !valid) Text("Enter a name of 1-40 characters without control characters.", color = MaterialTheme.colorScheme.error)
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        }, confirmButton = {
            Button(onClick = { onSave(trimmed, role, tier) }, enabled = valid && changed && !busy, modifier = Modifier.tvFocusOutline()) {
                Text(if (profile == null) "Create" else "Save Changes")
            }
        }, dismissButton = {
            TextButton(onClick = onDismiss, enabled = !busy, modifier = Modifier.tvFocusOutline()) { Text("Cancel") }
        })
}

@Composable
private fun SettingsConfirmation(title: String, message: String, confirm: String, busy: Boolean, error: String?,
    onDismiss: () -> Unit, onConfirm: () -> Unit) {
    val cancel = remember { FocusRequester() }
    LaunchedEffect(Unit) { if (BuildConfig.IS_TV) { withFrameNanos { }; cancel.requestFocus() } }
    AlertDialog(onDismissRequest = { if (!busy) onDismiss() }, title = { Text(title) },
        text = { Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(message)
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        } }, confirmButton = {
            TextButton(onClick = onConfirm, enabled = !busy, modifier = Modifier.tvFocusOutline()) { Text(confirm) }
        }, dismissButton = {
            TextButton(onClick = onDismiss, enabled = !busy, modifier = Modifier.focusRequester(cancel).tvFocusOutline()) { Text("Cancel") }
        })
}
