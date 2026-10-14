package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp

// Static onboarding has no animation, image assets, timers or background playback.
@Composable
internal fun TvNavigationTutorial(modifier: Modifier, onContinue: () -> Unit) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    Box(modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
        Column(Modifier.widthIn(max = 620.dp).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("Your remote, at a glance", style = MaterialTheme.typography.headlineMedium)
            Text("Guide: ↑ ↓ channels · ← → shows · Select to watch")
            Text("Watching: ↑ ↓ change channels · ← → controls · Select to activate")
            Text("⋮ menu: ↑ ↓ choose options · Back closes menus and controls")
            Text("Guide: arrows browse and wrap\nBack: current show → profile menu → current show")
            Button(onClick = onContinue, modifier = Modifier.align(Alignment.End)
                .focusRequester(focus).tvFocusOutline()) { Text("Got it") }
        }
    }
}
