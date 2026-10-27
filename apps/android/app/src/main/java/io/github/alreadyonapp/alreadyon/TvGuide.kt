package io.github.alreadyonapp.alreadyon

import android.graphics.Paint
import android.text.TextPaint
import android.text.TextUtils
import androidx.compose.animation.core.tween
import androidx.compose.foundation.gestures.animateScrollBy
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.BringIntoViewSpec
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.focus.*
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.input.key.*
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** One focus target per channel avoids hundreds of invisible Compose focus/layout nodes on small TVs. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun TvGuide(state: PhoneState, palette: PhonePalette, modifier: Modifier, now: Long, resetSignal: Int = 0, onNavigation: () -> Unit = {}, onSelection: (GuideChannel, GuideProgram?) -> Unit = { _, _ -> }, onChannelFocus: (Long, Boolean) -> Unit = { _, _ -> }, onTune: (GuideChannel) -> Unit) {
    val channels = state.channels
    val rows = rememberLazyListState(initialFirstVisibleItemIndex = channels.indexOfFirst { it.id == state.channel?.id }.coerceAtLeast(0))
    val scope = rememberCoroutineScope()
    var movingRow by remember { mutableStateOf(false) }
    val targets = remember(channels.map { it.id }) { channels.associate { it.id to FocusRequester() } }
    var selectedTime by remember { mutableLongStateOf(now) }
    var origin by remember { mutableLongStateOf(guideHalfHour(now)) }
    val format = remember(state.profile?.timeFormat) {
        DateTimeFormatter.ofPattern(when (state.profile?.timeFormat) { "24h" -> "HH:mm"; "12h-padded" -> "hh:mma"; else -> "h:mma" }, Locale.US).withZone(ZoneId.systemDefault())
    }
    LaunchedEffect(guideHalfHour(now)) {
        origin = guideHalfHour(now)
        if (selectedTime < origin) selectedTime = now
    }
    // Restoring the guide always restores the selected channel, independent of stream readiness.
    LaunchedEffect(channels.isNotEmpty(), state.profile?.id, state.channel?.id, resetSignal) {
        if (channels.isNotEmpty()) {
            val index = channels.indexOfFirst { it.id == state.channel?.id }.coerceAtLeast(0)
            val channel = channels[index]
            selectedTime = now
            origin = guideHalfHour(now)
            onChannelFocus(channel.id, true)
            onSelection(channel, channel.programs.find { selectedTime >= Instant.parse(it.startsAt).toEpochMilli() && selectedTime < Instant.parse(it.endsAt).toEpochMilli() }
                ?: channel.programs.firstOrNull())
            rows.scrollToItem(index)
            withFrameNanos { }
            targets[channels[index].id]?.requestFocus()
        }
    }
    BoxWithConstraints(modifier) {
    // Fit exactly two hours beside the fixed channel column at every viewport width.
    val minuteWidth = ((maxWidth.value - 100).coerceAtLeast(1f) / 120).dp
    val bottomPadding = (maxHeight - 34.dp - 56.dp).coerceAtLeast(0.dp)
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxWidth().background(palette.alternate).testTag("guide-timeline").semantics { stateDescription = origin.toString() }) {
            GuideClock(state.profile?.timeFormat, palette, Modifier.width(100.dp).padding(4.dp))
            Canvas(Modifier.weight(1f).height(34.dp)) {
                val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = palette.muted.toArgb(); textSize = 14.dp.toPx() }
                var x = 0f
                var stamp = origin
                while (x < size.width) {
                    drawIntoCanvas { it.nativeCanvas.drawText(format.format(Instant.ofEpochMilli(stamp)), x + 4.dp.toPx(), 23.dp.toPx(), paint) }
                    x += minuteWidth.toPx() * 30; stamp += 1_800_000
                }
            }
        }
        // Remote navigation owns scrolling; platform focus scrolling would recenter rows mid-animation.
        CompositionLocalProvider(LocalBringIntoViewSpec provides remember {
            object : BringIntoViewSpec {
                override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float) = 0f
            }
        }) {
        LazyColumn(state = rows, verticalArrangement = Arrangement.spacedBy(4.dp),
            modifier = Modifier.testTag("guide-rows"),
            contentPadding = PaddingValues(bottom = bottomPadding)) {
            if (channels.isEmpty()) item { Text("No channels are available for this profile.") }
            itemsIndexed(channels, key = { _, channel -> channel.id }) { index, channel ->
                var focused by remember { mutableStateOf(false) }
                val programs = remember(channel.programs) { channel.programs.map {
                    Triple(it, Instant.parse(it.startsAt).toEpochMilli(), Instant.parse(it.endsAt).toEpochMilli())
                } }
                val drawnPrograms = remember(channel.programs, channel.previousPrograms) {
                    (channel.previousPrograms + channel.programs).map {
                        Triple(it, Instant.parse(it.startsAt).toEpochMilli(), Instant.parse(it.endsAt).toEpochMilli())
                    }
                }
                // Row sizing uses the outer guide constraints; no subcomposition is needed here.
                Box(Modifier.fillMaxWidth()) {
                    val windowMs = 120 * 60_000L
                    fun selectionIndex(at: Long = selectedTime): Int = programs.indexOfFirst { at >= it.second && at < it.third }
                        .takeIf { it >= 0 } ?: programs.indexOfFirst { it.second >= at }.takeIf { it >= 0 } ?: programs.lastIndex
                    fun reveal(start: Long, end: Long) {
                        // Scroll only when the cell is outside the viewport; headers always retain half-hour boundaries.
                        if (end <= origin || start >= origin + windowMs) origin = guideHalfHour(start).coerceAtLeast(guideHalfHour(System.currentTimeMillis()))
                    }
                    fun reportFocus() {
                        val chosen = programs.getOrNull(selectionIndex())
                        if (chosen != null) {
                            selectedTime = selectedTime.coerceIn(chosen.second, (chosen.third - 1).coerceAtLeast(chosen.second))
                            reveal(chosen.second, chosen.third)
                        }
                        val current = chosen != null && chosen.first == channel.liveProgram(now)
                        onChannelFocus(channel.id, current)
                        onSelection(channel, chosen?.first)
                    }
                    LaunchedEffect(programs, now) {
                        if (focused) {
                            val chosen = programs.getOrNull(selectionIndex())
                            onChannelFocus(channel.id, chosen != null && chosen.first == channel.liveProgram(now))
                            onSelection(channel, chosen?.first)
                        }
                    }
                    Box(Modifier.fillMaxWidth().height(56.dp).testTag("guide-channel-${channel.id}").focusRequester(targets.getValue(channel.id))
                            .onFocusChanged { focused = it.hasFocus; if (it.isFocused) reportFocus() }
                            .semantics(mergeDescendants = true) { contentDescription = "Channel ${channel.number}: ${channel.name}"; stateDescription = programs.getOrNull(selectionIndex())?.let { "${it.first.title} · ${it.first.startsAt}" }.orEmpty() }
                            .onPreviewKeyEvent { event ->
                                if (event.key !in listOf(Key.DirectionUp, Key.DirectionDown, Key.DirectionLeft, Key.DirectionRight)) false
                                else {
                                    if (event.type == KeyEventType.KeyDown) {
                                        onNavigation()
                                        if (event.key == Key.DirectionUp || event.key == Key.DirectionDown) {
                                            val neighbour = nextPlayableChannel(channels, channel.id, if (event.key == Key.DirectionDown) 1 else -1)
                                                ?: return@onPreviewKeyEvent true
                                            val next = channels.indexOfFirst { it.id == neighbour.id }
                                            if (!movingRow) scope.launch {
                                                movingRow = true
                                                try {
                                                    // Reveal only the clipped distance, equally in both directions.
                                                    val layout = rows.layoutInfo
                                                    val target = layout.visibleItemsInfo.find { it.index == next }
                                                    val distance = if (target == null) {
                                                        val current = layout.visibleItemsInfo.find { it.index == index }
                                                        ((current?.size ?: 0) + layout.mainAxisItemSpacing) * if (next > index) 1f else -1f
                                                    } else if (target.offset < layout.viewportStartOffset) {
                                                        (target.offset - layout.viewportStartOffset).toFloat()
                                                    } else (target.offset + target.size - layout.viewportEndOffset).coerceAtLeast(0).toFloat()
                                                    // Wrapped/skipped targets may not be composed; attach them before requesting focus.
                                                    if (kotlin.math.abs(next - index) > 1) rows.scrollToItem(next)
                                                    else if (distance != 0f) rows.animateScrollBy(distance, tween(140))
                                                    withFrameNanos { }
                                                    targets[channels[next].id]?.requestFocus()
                                                } finally { movingRow = false }
                                            }
                                        } else {
                                            val current = selectionIndex()
                                            val next = if (programs.isEmpty()) -1 else Math.floorMod(current + if (event.key == Key.DirectionRight) 1 else -1, programs.size)
                                            programs.getOrNull(next)?.let { selectedTime = it.second; reportFocus() }
                                        }
                                    }
                                    true
                                }
                            }.clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onTune(channel) }) {
                        Row {
                            Column(Modifier.width(100.dp).fillMaxHeight().background(palette.alternate).padding(4.dp), verticalArrangement = Arrangement.Center) {
                                Text(channel.number.toString(), style = MaterialTheme.typography.titleLarge)
                                Text(channel.name, maxLines = 2, style = MaterialTheme.typography.bodySmall)
                            }
                            val labelPaint = remember { TextPaint(Paint.ANTI_ALIAS_FLAG) }
                            Box(Modifier.weight(1f).fillMaxHeight()) {
                            Canvas(Modifier.fillMaxSize().then(if (channel.id == state.channel?.id && channel.liveProgram(now) != null)
                                Modifier.testTag("guide-watched-airing").semantics { stateDescription = channel.liveProgram(now)?.startsAt.orEmpty() } else Modifier)) {
                                val scale = minuteWidth.toPx() / 60_000
                                val gap = 3.dp.toPx()
                                drawnPrograms.forEach { (program, start, end) ->
                                    val left = ((start - origin) * scale).coerceAtLeast(0f) + gap
                                    val right = ((end - origin) * scale).coerceAtMost(size.width)
                                    if (right <= left) return@forEach
                                    val current = now >= start && now < end
                                    val past = end <= now
                                    val fill = if (past) Color.Gray.copy(alpha = 0.28f).compositeOver(palette.background) else if (current) palette.now else palette.surface
                                    drawRoundRect(fill, androidx.compose.ui.geometry.Offset(left, 0f), androidx.compose.ui.geometry.Size(right-left, size.height), androidx.compose.ui.geometry.CornerRadius(6.dp.toPx()))
                                    labelPaint.color = (if (past) palette.muted else if (current) palette.nowText else palette.text).toArgb()
                                    val available = (right-left-8.dp.toPx()).coerceAtLeast(0f)
                                    labelPaint.textSize = 11.dp.toPx()
                                    val label = "${format.format(Instant.ofEpochMilli(start))} - ${format.format(Instant.ofEpochMilli(end))}"
                                    if (past && labelPaint.measureText(label) > available) return@forEach
                                    drawIntoCanvas { canvas ->
                                        canvas.nativeCanvas.drawText(TextUtils.ellipsize(label, labelPaint, available, TextUtils.TruncateAt.END).toString(), left+4.dp.toPx(), 17.dp.toPx(), labelPaint)
                                        canvas.nativeCanvas.drawText(TextUtils.ellipsize(program.title, labelPaint, available, TextUtils.TruncateAt.END).toString(), left+4.dp.toPx(), 33.dp.toPx(), labelPaint)
                                        if (program.episodeNumber > 0) {
                                            val episode = "S${program.seasonNumber}E${program.episodeNumber} - ${program.episodeTitle}"
                                            canvas.nativeCanvas.drawText(TextUtils.ellipsize(episode, labelPaint, available, TextUtils.TruncateAt.END).toString(), left+4.dp.toPx(), 49.dp.toPx(), labelPaint)
                                        }
                                    }
                                }
                            }
                            // Keep the hot remote-focus draw separate from the cached program labels.
                            Canvas(Modifier.fillMaxSize()) {
                                val scale = minuteWidth.toPx() / 60_000
                                val selected = selectionIndex()
                                fun outline(start: Long, end: Long, color: Color, inset: Float) {
                                    val left = ((start - origin) * scale).coerceIn(0f, (size.width - 12.dp.toPx()).coerceAtLeast(0f))
                                    val width = (((end - origin) * scale).coerceAtMost(size.width) - left).coerceAtLeast(12.dp.toPx())
                                    drawRoundRect(color, androidx.compose.ui.geometry.Offset(left + inset, inset),
                                        androidx.compose.ui.geometry.Size((width - 2 * inset).coerceAtLeast(0f), size.height - 2 * inset),
                                        androidx.compose.ui.geometry.CornerRadius(6.dp.toPx()), style = androidx.compose.ui.graphics.drawscope.Stroke(2.dp.toPx()))
                                }
                                if (channel.id == state.channel?.id) programs.firstOrNull { now >= it.second && now < it.third }?.let { (_, start, end) ->
                                    if (end > origin && start < origin + 120 * 60_000L) outline(start, end, Color.Red, 2f)
                                }
                                if (focused) programs.getOrNull(selected)?.let { (_, start, end) ->
                                    outline(start, end, Color.White, 5.dp.toPx())
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
