package io.github.alreadyonapp.alreadyon

import android.os.SystemClock
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.focusable
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.dp
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

private fun JSONArray?.objects(): List<JSONObject> = if (this == null) emptyList() else
    List(length()) { optJSONObject(it) }.filterNotNull()
private fun JSONObject.number(key: String): Double? = if (isNull(key) || !has(key)) null else optDouble(key).takeIf { it.isFinite() }
private fun JSONObject.text(key: String, fallback: String = "Unavailable"): String = optString(key).takeUnless { it.isBlank() || it == "null" } ?: fallback
private fun number(value: Double?, unit: String = "%"): String = value?.let { String.format(Locale.US, "%.1f%s", it, unit) } ?: "Unavailable"
private fun bytes(value: Double?): String {
    if (value == null) return "Unavailable"
    var amount = value
    val units = listOf("B", "KB", "MB", "GB", "TB", "PB")
    var index = 0
    while (amount >= 1000 && index < units.lastIndex) { amount /= 1000; index++ }
    return number(amount, " ${units[index]}")
}
private fun capacity(sample: JSONObject, used: String, total: String) = "${bytes(sample.number(used))} / ${bytes(sample.number(total))}"

/** Native read-only monitoring; polling and in-flight requests end when the screen leaves STARTED. */
@Composable
internal fun DashboardScreen(state: PhoneState, model: PhoneModel, modifier: Modifier = Modifier) {
    val updates: AppUpdateModel = androidx.lifecycle.viewmodel.compose.viewModel()
    val appVersion by updates.currentRelease.collectAsState()
    var details by remember { mutableStateOf<JSONObject?>(null) }
    var activity by remember { mutableStateOf<JSONObject?>(null) }
    var metrics by remember { mutableStateOf<JSONObject?>(null) }
    var detailsError by remember { mutableStateOf<String?>(null) }
    var activityError by remember { mutableStateOf<String?>(null) }
    var metricsError by remember { mutableStateOf<String?>(null) }
    var latency by remember { mutableStateOf<Double?>(null) }
    var refresh by remember { mutableIntStateOf(1) }
    var minutes by remember { mutableIntStateOf(15) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val backFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) { if (BuildConfig.IS_TV) { withFrameNanos { }; backFocus.requestFocus() } }
    BackHandler { model.closeSettings() }
    LaunchedEffect(model, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            launch {
                while (true) {
                    try { details = JSONObject(model.dashboardRequest("/api/server/details")); detailsError = null }
                    catch (e: CancellationException) { throw e }
                    catch (e: Exception) { detailsError = safeError(e) }
                    delay(3000)
                }
            }
            launch {
                while (true) {
                    try { activity = JSONObject(model.dashboardRequest("/api/server/activity")); activityError = null }
                    catch (e: CancellationException) { throw e }
                    catch (e: Exception) { activityError = safeError(e) }
                    delay(3000)
                }
            }
            launch {
                while (true) {
                    val started = SystemClock.elapsedRealtime()
                    try { model.dashboardRequest("/healthz"); latency = (SystemClock.elapsedRealtime() - started).toDouble() }
                    catch (e: CancellationException) { throw e }
                    catch (_: Exception) { latency = null }
                    delay(1000)
                }
            }
        }
    }
    LaunchedEffect(model, lifecycle, refresh, minutes) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                try { metrics = JSONObject(model.dashboardRequest("/api/server/metrics?minutes=$minutes")); metricsError = null }
                catch (e: CancellationException) { throw e }
                catch (e: Exception) { metricsError = safeError(e) }
                delay(refresh * 1000L)
            }
        }
    }
    BoxWithConstraints(modifier.fillMaxWidth()) {
        val columns = if (!BuildConfig.IS_TV && maxWidth >= 700.dp) 2 else 1
        LazyColumn(verticalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(bottom = 12.dp)) {
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text("Dashboard", style = MaterialTheme.typography.headlineMedium)
                    OutlinedButton(onClick = model::closeSettings, modifier = Modifier.focusRequester(backFocus).tvFocusOutline()) { Text("Back to Guide") }
                }
            }
            item {
                DashboardCard("Server Details") {
                    DashboardStatus(details != null, detailsError)
                    DashboardField("Server Name", details?.text("serverName") ?: state.serverName)
                    DashboardField("Server Address", state.origin)
                    DashboardField("Server Version", details?.text("appVersion") ?: "Loading…")
                    DashboardField("App Version", appVersion.lowercase())
                }
            }
            item {
                DashboardCard("Active Streaming Sessions") {
                    DashboardStatus(activity != null, activityError)
                    if (activity != null && activity?.optJSONArray("sessions").objects().isEmpty()) Text("No active streaming sessions.")
                }
            }
            // Each session/process gets its own scroll target so TV navigation can reveal every row.
            items(activity?.optJSONArray("sessions").objects()) { session ->
                DashboardCard(session.text("channel")) {
                    Text(session.text("video"))
                    DashboardFields(listOf(
                        "Profile" to session.text("profile"),
                        "Source IP" to session.text("sourceIp"),
                        "Client Device" to session.text("device"),
                        "Status" to if (session.optBoolean("commercial")) "Commercial Break — Updates Paused" else if (session.optBoolean("paused")) "Paused" else "Playing",
                        "Bandwidth" to number(session.number("bandwidthMbps"), " Mbps"),
                        "Latency" to number(session.number("latencyMs"), " ms"),
                        "Playback" to when (session.text("mode")) { "direct" -> "Direct Play"; "remux" -> "Remux"; else -> "Transcode" },
                        "Transcoding" to session.text("encoder", "Not Required")
                    ))
                    DashboardField("Video", session.text("videoReason", "Playback details unavailable."))
                    DashboardField("Audio", session.text("audioReason", "Playback details unavailable."))
                }
            }
            item {
                DashboardCard("Active Background Processes") {
                    DashboardStatus(activity != null, activityError)
                    if (activity != null && activity?.optJSONArray("processes").objects().isEmpty()) Text("No active background processes.")
                }
            }
            items(activity?.optJSONArray("processes").objects()) { process ->
                DashboardCard(process.text("name")) {
                    val seconds = process.optLong("runtimeSeconds")
                    DashboardField("Runtime", "${seconds / 3600}h ${seconds % 3600 / 60}m ${seconds % 60}s")
                    val percent = process.number("percent")
                    if (percent != null) LinearProgressIndicator(progress = { (percent / 100).toFloat().coerceIn(0f, 1f) }, modifier = Modifier.fillMaxWidth())
                    else LinearProgressIndicator(Modifier.fillMaxWidth())
                    DashboardField("Completion", if (percent == null) "In Progress · ${process.text("unit", "")}" else "${number(percent)} · ${process.optLong("completed")} / ${process.optLong("total")} ${process.text("unit", "")}")
                    process.optString("currentFile").takeUnless { it.isBlank() || it == "null" }?.let { Text(it, maxLines = 2, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis) }
                }
            }
            item {
                Spacer(Modifier.height(16.dp))
                HorizontalDivider(color = phonePalette.muted.copy(alpha = 0.3f))
                Spacer(Modifier.height(12.dp))
                Text("Server Metrics", style = MaterialTheme.typography.titleLarge)
                Spacer(Modifier.height(8.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    DashboardChoice("Refresh", refresh, listOf(1, 5, 10, 30, 60, 120, 300), { "${it}s" }, Modifier.weight(1f)) { refresh = it }
                    DashboardChoice("History", minutes, listOf(15, 30, 60, 120, 240, 720), { "${it}min" }, Modifier.weight(1f)) { minutes = it }
                }
                DashboardStatus(metrics != null, metricsError)
            }
            listOf("cpu", "ram", "disk", "bandwidth").chunked(columns).forEach { keys ->
                item {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        keys.forEach { key -> DashboardMetric(key, metrics, minutes, latency, Modifier.weight(1f)) }
                    }
                }
            }
        }
    }
}

@Composable
private fun DashboardCard(title: String, modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Surface(modifier.fillMaxWidth().then(if (BuildConfig.IS_TV) Modifier.tvFocusOutline().focusable() else Modifier), color = phonePalette.surface, shape = MaterialTheme.shapes.medium) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            content()
        }
    }
}
@Composable
private fun DashboardField(label: String, value: String, modifier: Modifier = Modifier) {
    Column(modifier) { Text(label, color = phonePalette.muted, style = MaterialTheme.typography.labelMedium); Text(value, maxLines = 3, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis) }
}
@Composable
private fun DashboardFields(fields: List<Pair<String, String>>) {
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val columns = if (maxWidth >= 700.dp) 3 else if (maxWidth >= 450.dp) 2 else 1
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            fields.chunked(columns).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    row.forEach { (label, value) -> DashboardField(label, value, Modifier.weight(1f)) }
                }
            }
        }
    }
}
@Composable
private fun DashboardStatus(loaded: Boolean, error: String?) {
    if (error != null) Text("Live updates unavailable. Displayed information may be out of date; retrying… $error", color = MaterialTheme.colorScheme.error)
    else if (!loaded) Text("Loading…")
}
@Composable
private fun DashboardChoice(label: String, value: Int, choices: List<Int>, format: (Int) -> String, modifier: Modifier, onSelect: (Int) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box(modifier) {
        OutlinedButton(onClick = { open = true }, modifier = Modifier.fillMaxWidth().tvFocusOutline()) { Text("$label: ${format(value)}") }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            choices.forEach { choice -> DropdownMenuItem(text = { Text(format(choice)) }, modifier = Modifier.tvFocusOutline(), onClick = { open = false; onSelect(choice) }) }
        }
    }
}

private data class DashboardLine(val label: String, val color: Color, val percent: Boolean = true, val read: (JSONObject) -> Double?)
private val chartColors = listOf(Color(0xff39b5a1), Color(0xfff2b84b), Color(0xff5289f4), Color(0xffce92ef), Color(0xffef7795), Color(0xff6fd3e8))

/** Charts have no click or pointer handlers; missing readings break paths rather than inventing zeros. */
@Composable
private fun DashboardMetric(key: String, history: JSONObject?, minutes: Int, latency: Double?, modifier: Modifier) {
    val current = history?.optJSONObject("current") ?: JSONObject()
    val cores = current.optJSONArray("cpuCores").objects().map { it.optInt("core") }
    val points = history?.optJSONArray("points").objects().filter { key != "bandwidth" || (it.optString("networkInterface") == current.optString("networkInterface") && it.optString("networkScope") == current.optString("networkScope")) }
    val lines = when (key) {
        "cpu" -> if (cores.isEmpty()) listOf(DashboardLine("CPU", chartColors[0]) { it.number("cpu") }) else cores.mapIndexed { index, core ->
            DashboardLine("CPU $core", chartColors[index % chartColors.size]) { sample -> sample.optJSONArray("cpuCores").objects().find { it.optInt("core") == core }?.number("utilization") }
        }
        "ram" -> listOf(DashboardLine("Memory Used", chartColors[0]) { it.number("ram") })
        "disk" -> listOf(DashboardLine("Read", chartColors[0], false) { it.number("diskReadMBps") }, DashboardLine("Write", chartColors[1], false) { it.number("diskWriteMBps") }, DashboardLine("IO Pressure", chartColors[3]) { it.number("ioPressure") })
        else -> listOf(DashboardLine("Ingress", chartColors[0], false) { it.number("ingressMbps") }, DashboardLine("Egress", chartColors[1], false) { it.number("egressMbps") })
    }
    val ceiling = if (key == "cpu" || key == "ram") 100.0 else maxOf(1.0, (points + current).flatMap { p -> lines.filter { !it.percent }.mapNotNull { it.read(p) } }.maxOrNull() ?: 1.0)
    val end = maxOf(Instant.now().epochSecond, current.optLong("at"))
    val start = end - minutes * 60L
    val timeFormat = DateTimeFormatter.ofPattern("HH:mm", Locale.US).withZone(ZoneId.systemDefault())
    val metricStyle = MaterialTheme.typography.bodySmall
    DashboardCard(if (key == "cpu" || key == "ram") key.uppercase(Locale.US) else if (key == "disk") "Disk" else "Bandwidth · ${current.text("networkInterface", "")}", modifier) {
        when (key) {
            "cpu" -> Text("Load: ${number(current.number("cpuLoad"), "")} · Pressure: ${number(current.number("cpuPressure"))} · Utilization: ${number(current.number("cpu"))}", style = metricStyle)
            "ram" -> Text("Usage: ${capacity(current, "memoryUsedBytes", "memoryLimitBytes")} · Utilization: ${number(current.number("ram"))}", style = metricStyle)
            "disk" -> {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text("Utilization: ${number(current.number("disk"))}", style = metricStyle)
                        Text("Config: ${capacity(current, "diskUsedBytes", "diskTotalBytes")}", style = metricStyle)
                        Text("Media: ${capacity(current, "mediaUsedBytes", "mediaTotalBytes")}", style = metricStyle)
                    }
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        lines.forEach { line -> Text("${line.label}: ${number(line.read(current), if (line.percent) "%" else " MB/s")}", color = line.color, style = metricStyle) }
                    }
                }
            }
            else -> Text("Latency: ${number(latency, " ms")}", style = metricStyle)
        }
        if (key != "cpu" && key != "disk") lines.forEach { line ->
            Text("${line.label}: ${number(line.read(current), if (line.percent) "%" else " Mbps")}", color = line.color, style = metricStyle)
        }
        val gridColor = phonePalette.muted.copy(alpha = 0.3f)
        val labelColor = phonePalette.muted
        Canvas(Modifier.fillMaxWidth().height((if (BuildConfig.IS_TV) 210f else 180f).times(0.5625f).dp).semantics { contentDescription = "$key performance history" }) {
            // Reserve an axis gutter and match legend text using the user’s font scale.
            // Samples outside the selected history never draw into this gutter.
            val left = 64.dp.toPx()
            val right = size.width - if (key == "disk") 52.dp.toPx() else 6.dp.toPx()
            val top = 10.dp.toPx()
            val bottom = size.height - 24.dp.toPx()
            val paint = android.graphics.Paint(android.graphics.Paint.ANTI_ALIAS_FLAG).apply {
                color = labelColor.toArgb()
                textSize = metricStyle.fontSize.toPx()
            }
            repeat(5) { index ->
                val fraction = index / 4.0
                val y = top + (bottom - top) * index / 4
                drawLine(gridColor, Offset(left, y), Offset(right, y))
                paint.textAlign = android.graphics.Paint.Align.RIGHT
                val value = ceiling * (1 - fraction)
                val label = if (key == "cpu" || key == "ram") "${value.toInt()}%" else String.format(Locale.US, "%.1f", value)
                drawContext.canvas.nativeCanvas.drawText(label, left - 12.dp.toPx(), y + 3.dp.toPx(), paint)
                if (key == "disk") {
                    paint.textAlign = android.graphics.Paint.Align.LEFT
                    drawContext.canvas.nativeCanvas.drawText("${((1 - fraction) * 100).toInt()}%", right + 4.dp.toPx(), y + 3.dp.toPx(), paint)
                }
            }
            if (key == "disk" || key == "bandwidth") {
                paint.textAlign = android.graphics.Paint.Align.LEFT
                drawContext.canvas.nativeCanvas.drawText(if (key == "disk") "MB/s" else "Mbps", 0f, size.height - 2.dp.toPx(), paint)
            }
            drawLine(gridColor, Offset(left, top), Offset(left, bottom))
            drawLine(gridColor, Offset(left, bottom), Offset(right, bottom))
            repeat(4) { index ->
                val x = left + (right - left) * index / 3
                val at = start + (end - start) * index / 3
                paint.textAlign = when (index) { 0 -> android.graphics.Paint.Align.LEFT; 3 -> android.graphics.Paint.Align.RIGHT; else -> android.graphics.Paint.Align.CENTER }
                drawContext.canvas.nativeCanvas.drawText(timeFormat.format(Instant.ofEpochSecond(at)), x, size.height - 3.dp.toPx(), paint)
            }
            lines.forEach { line ->
                val path = Path()
                var connected = false
                points.forEach { point ->
                    val value = line.read(point)
                    val at = point.optLong("at")
                    if (value == null || at !in start..end) connected = false
                    else {
                        val x = left + ((at - start).toDouble() / (end - start) * (right - left)).toFloat()
                        val y = top + ((bottom - top) * (1 - (value / if (line.percent) 100.0 else ceiling).coerceIn(0.0, 1.0))).toFloat()
                        if (connected) path.lineTo(x, y) else path.moveTo(x, y)
                        connected = true
                    }
                }
                drawPath(path, line.color, style = Stroke(2.dp.toPx()))
            }
        }
        Text("${history?.optInt("intervalSeconds") ?: 15} second averages", style = metricStyle)
    }
}
