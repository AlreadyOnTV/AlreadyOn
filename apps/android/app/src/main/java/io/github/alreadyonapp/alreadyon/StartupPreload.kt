package io.github.alreadyonapp.alreadyon

import android.os.SystemClock
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.pointer.pointerInput
import kotlinx.coroutines.delay

/** Shared static startup cover; the session, guide and player load underneath without another request. */
@Composable
internal fun StartupPreload(ready: Boolean = true, showContent: Boolean = true, content: @Composable () -> Unit) {
    var loading by rememberSaveable { mutableStateOf(true) }
    var completed by rememberSaveable { mutableStateOf(false) }
    val deadline = rememberSaveable { SystemClock.elapsedRealtime() + 2_000 }
    // Configuration changes retain the acknowledgement rather than restarting the delay.
    LaunchedEffect(Unit) { delay((deadline - SystemClock.elapsedRealtime()).coerceAtLeast(0)); loading = false }
    LaunchedEffect(loading, ready) { if (!loading && ready) completed = true }
    Box(Modifier.fillMaxSize().onPreviewKeyEvent { !completed }) {
        if (completed || showContent) content()
        if (!completed) PhoneTheme {
            Surface(Modifier.fillMaxSize().testTag("startup-cover").pointerInput(Unit) {
                awaitPointerEventScope {
                    while (true) awaitPointerEvent().changes.forEach { it.consume() }
                }
            }, color = phonePalette.background) {
                BoxWithConstraints(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    // Double the complete lockup, constrained only by narrow phone viewports.
                    PhoneBrand(scale = minOf(2f, (maxWidth.value - 32f) / 224f).coerceAtLeast(1f))
                }
            }
        }
    }
}
