// Phone and TV share release promotion prompts and APK installation progress.
package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.collectAsState
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.platform.LocalInputModeManager
import androidx.compose.ui.input.InputMode
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel

/** The shared phone/TV prompt distinguishes APK installation from accepting a promoted release. */
@Composable
internal fun UpdatePrompt(visible: Boolean = true) {
    val model: AppUpdateModel = viewModel()
    val state by model.state.collectAsStateWithLifecycle()
    val owner = LocalLifecycleOwner.current
    val context = LocalContext.current
    val lifecycle by owner.lifecycle.currentStateFlow.collectAsState()
    LaunchedEffect(state.readyToInstall, lifecycle, visible) {
        if (visible && lifecycle == Lifecycle.State.RESUMED && state.readyToInstall) model.continueInstallation()
    }
    DisposableEffect(owner, model) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_RESUME) model.refreshInstallation() }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
    if (!visible || (state.update == null && state.phase !in setOf(UpdatePhase.ERROR, UpdatePhase.INSTALLING))) return
    UpdateDialog(state, onUpdate = model::update, onCancel = model::cancel,
        onPermission = { try { context.startActivity(model.permissionIntent()) } catch (_: Exception) { model.permissionFailure() } },
        onContinue = model::continueInstallation)
}

@Composable
internal fun UpdateDialog(state: AppUpdateState, onUpdate: () -> Unit, onCancel: () -> Unit,
    onPermission: () -> Unit, onContinue: () -> Unit) {
    val focus = remember { FocusRequester() }
    AlertDialog(onDismissRequest = onCancel,
        title = { Text(if (state.phase == UpdatePhase.ERROR) "Update could not finish" else "Update Available") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("Current version: ${state.currentRelease.lowercase()}")
                state.update?.let {
                    Text("Available version: ${it.release.lowercase()}")
                }
                when (state.phase) {
                    UpdatePhase.AVAILABLE -> Text(if (state.promotion)
                        "This release uses your installed build. Update the app version shown to match this release?"
                        else "Download and install the new version of AlreadyOn?")
                    UpdatePhase.DOWNLOADING -> {
                        Text(if (state.promotion) "Updating release…" else "Downloading… ${(state.progress * 100).toInt()}%")
                        LinearProgressIndicator(progress = { state.progress }, modifier = Modifier.fillMaxWidth())
                    }
                    UpdatePhase.PERMISSION -> Text("Allow AlreadyOn to install apps in Android settings. Installation will continue when you return.")
                    UpdatePhase.INSTALLING -> Text("Finish the Android installation prompts. If AlreadyOn does not reopen automatically, select Open or launch it again.")
                    UpdatePhase.ERROR -> Text(state.error ?: "Could not update the app.")
                }
            }
        },
        confirmButton = {
            val inputMode = LocalInputModeManager.current
            val windowFocused = LocalWindowInfo.current.isWindowFocused
            LaunchedEffect(state.phase, windowFocused) {
                if (windowFocused && BuildConfig.IS_TV && state.phase != UpdatePhase.DOWNLOADING && !(state.phase == UpdatePhase.ERROR && state.update == null)) {
                    inputMode.requestInputMode(InputMode.Keyboard)
                    androidx.compose.runtime.withFrameNanos { }
                    focus.requestFocus()
                }
            }
            when (state.phase) {
                UpdatePhase.AVAILABLE, UpdatePhase.ERROR -> if (state.update != null) TextButton(onClick = onUpdate,
                    modifier = Modifier.focusRequester(focus).tvFocusOutline()) { Text(if (state.phase == UpdatePhase.ERROR) "Retry" else "Update") }
                UpdatePhase.PERMISSION -> TextButton(onClick = onPermission, modifier = Modifier.focusRequester(focus).tvFocusOutline()) { Text("Open Settings") }
                UpdatePhase.INSTALLING -> TextButton(onClick = onContinue,
                    modifier = Modifier.focusRequester(focus).tvFocusOutline()) { Text("Continue Installation") }
                else -> Unit
            }
        },
        dismissButton = {
            val needsFocus = state.phase == UpdatePhase.DOWNLOADING || (state.update == null && state.phase == UpdatePhase.ERROR)
            val inputMode = LocalInputModeManager.current
            val windowFocused = LocalWindowInfo.current.isWindowFocused
            LaunchedEffect(state.phase, windowFocused) { if (windowFocused && BuildConfig.IS_TV && needsFocus) { inputMode.requestInputMode(InputMode.Keyboard); androidx.compose.runtime.withFrameNanos { }; focus.requestFocus() } }
            TextButton(onClick = onCancel, modifier = (if (needsFocus) Modifier.focusRequester(focus) else Modifier).tvFocusOutline()) {
                Text(if (state.phase == UpdatePhase.ERROR) "Close" else "Cancel")
            }
        })
}
