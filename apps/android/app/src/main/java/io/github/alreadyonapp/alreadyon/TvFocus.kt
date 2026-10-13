// Remote focus styling shared by TV buttons and guide cells.
package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.border
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/** Keep remote focus visible on shared controls without changing phone styling. */
internal fun Modifier.tvFocusOutline(): Modifier = if (!BuildConfig.IS_TV) this else composed {
    var focused by remember { mutableStateOf(false) }
    onFocusChanged { focused = it.hasFocus }
        .then(if (focused) Modifier.border(2.dp, Color.White) else Modifier)
}
