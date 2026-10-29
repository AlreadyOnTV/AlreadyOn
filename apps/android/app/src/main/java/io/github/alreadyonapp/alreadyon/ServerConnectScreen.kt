// Shared phone/tablet and TV discovery, address fallback and account PIN onboarding.
package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.ui.Alignment
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalInputModeManager
import androidx.compose.ui.input.InputMode
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.input.key.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.text.input.PasswordVisualTransformation
import kotlinx.coroutines.delay
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.io.IOException
import java.io.ByteArrayInputStream
import java.security.cert.CertificateFactory
import java.util.concurrent.TimeUnit

private data class TvTrust(val certificate: String, val name: String)

/** Only public bootstrap metadata uses an untrusted TLS connection; PINs never do. */
private suspend fun probe(server: TvServer): TvTrust = withContext(Dispatchers.IO) {
    val base = server.origin.toHttpUrl()
    val http = OkHttpClient.Builder().bootstrapTls(base).connectTimeout(5, TimeUnit.SECONDS)
        .callTimeout(10, TimeUnit.SECONDS).build()
    fun get(path: String): JSONObject = http.newCall(Request.Builder().url(base.resolve(path)!!).build()).execute().use { response ->
        if (!response.isSuccessful) throw IOException("Server is not available for local TV pairing.")
        val source = response.body?.source() ?: throw IOException("Empty server response.")
        if (source.request(64 * 1024L + 1)) throw IOException("Server metadata is too large.")
        JSONObject(source.readUtf8())
    }
    val info = get("/api/tv/server")
    if (info.optInt("protocol") != 1 || !info.optBoolean("pairingEnabled")) throw IOException("Set your TV PIN in this server’s Admin Settings first.")
    val data = get("/api/tv/trust")
    val pem = data.getString("certificate")
    val certificate = CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(pem.toByteArray())) as java.security.cert.X509Certificate
    certificate.checkValidity()
    return@withContext TvTrust(pem, info.getString("name"))

}

@Composable
internal fun ServerConnectScreen(state: PhoneState, model: PhoneModel, modifier: Modifier) {
    val context = LocalContext.current
    val recent = remember { serverOrigin(state.origin)?.let { TvServer("${state.serverName} (Recently connected)", it) } }
    val servers = remember { mutableStateListOf<TvServer>().apply { recent?.let { add(it) } } }
    var scan by remember { mutableIntStateOf(0) }
    var selected by remember { mutableStateOf<TvServer?>(
        if (BuildConfig.IS_TV && state.trustIssue) serverOrigin(state.origin)?.let { TvServer(state.serverName, it) } else null) }
    var trust by remember { mutableStateOf<TvTrust?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var checking by remember { mutableStateOf(false) }
    var pin by remember { mutableStateOf("") }
    var pinVisible by remember { mutableStateOf(false) }
    var username by remember { mutableStateOf("") }
    var help by remember { mutableStateOf(false) }
    var address by remember { mutableStateOf(ManualServerAddress()) }
    val portFocus = remember { FocusRequester() }
    var remaining by remember { mutableIntStateOf(30) }
    val scope = rememberCoroutineScope()
    val keyboard = LocalSoftwareKeyboardController.current
    val focusManager = LocalFocusManager.current
    val inputMode = LocalInputModeManager.current
    // TV arrows leave the address editor vertically instead of moving its text cursor.
    // Keep Left/Right available for editing and leave phone keyboard behavior unchanged.
    val addressNavigation = Modifier.onPreviewKeyEvent { event ->
        if (BuildConfig.IS_TV && event.key in listOf(Key.DirectionUp, Key.DirectionDown)) {
            if (event.type == KeyEventType.KeyDown) {
                keyboard?.hide()
                focusManager.moveFocus(if (event.key == Key.DirectionUp) FocusDirection.Up else FocusDirection.Down)
            }
            true
        } else false
    }
    val usernameFocus = remember { FocusRequester() }
    LaunchedEffect(trust) { if (trust != null) usernameFocus.requestFocus() }
    val pinFocus = remember { FocusRequester() }
    val connectFocus = remember { FocusRequester() }
    fun enterPin() { pinFocus.requestFocus(); keyboard?.show() }
    fun closePin() {
        keyboard?.hide()
        focusManager.clearFocus(force = true)
        if (username.isNotBlank() && pin.length in 6..12) scope.launch {
            if (BuildConfig.IS_TV) inputMode.requestInputMode(InputMode.Keyboard)
            withFrameNanos { }
            connectFocus.requestFocus()
        }
    }
    LaunchedEffect(scan) {
        remaining = 30
        while (remaining > 0) { delay(1000); remaining-- }
    }
    fun select(server: TvServer) {
        selected = server; checking = true; error = null; trust = null; model.dismissError()
        scope.launch {
            try {
                // Saved trust and cookies are usable only for the exact recently connected origin.
                // Validate with the server before presenting the PIN form or fetching new trust.
                try {
                    if (model.resumeSavedServer(server.origin)) return@launch
                } catch (failure: Exception) {
                    // A changed identity needs public trust review, not a dead-end retry.
                    // Network outages keep the encrypted session and must not fetch new trust.
                    if (!isServerTrustFailure(failure)) throw failure
                }
                val result = probe(server)
                selected = server.copy(name = result.name); trust = result
            }
            catch (e: kotlinx.coroutines.CancellationException) { throw e }
            catch (e: Exception) { error = e.message ?: "Could not check server." }
            finally { checking = false }
        }
    }
    LaunchedEffect(state.trustIssue, state.origin) {
        if (BuildConfig.IS_TV && state.trustIssue) {
            serverOrigin(state.origin)?.let { select(TvServer(state.serverName, it)) }
        }
    }
    fun goBack() {
        keyboard?.hide()
        selected = null; trust = null; pin = ""; pinVisible = false; error = null
        model.dismissError(); model.dismissTrustIssue()
    }
    DisposableEffect(scan, remaining == 0, selected) {
        if (remaining == 0 || selected != null) return@DisposableEffect onDispose {}
        val discovery = TvDiscovery(context, found = { server ->
            val index = servers.indexOfFirst { it.origin == server.origin }
            if (index < 0) servers.add(server)
            else servers[index] = if (server.origin == recent?.origin) server.copy(name = "${server.name} (Recently connected)") else server
        }, failed = { error = it })
        onDispose { discovery.close() }
    }
    Box(modifier.fillMaxWidth(), contentAlignment = if (BuildConfig.IS_TV) Alignment.TopCenter else Alignment.TopStart) {
    Column(Modifier.widthIn(max = if (BuildConfig.IS_TV) 560.dp else androidx.compose.ui.unit.Dp.Infinity).fillMaxWidth()
        .verticalScroll(rememberScrollState()).padding(horizontal = 12.dp, vertical = if (BuildConfig.IS_TV) 0.dp else 12.dp),
        horizontalAlignment = if (BuildConfig.IS_TV) Alignment.CenterHorizontally else Alignment.Start,
        verticalArrangement = Arrangement.spacedBy(12.dp)) {
        if (selected == null) {
            Text("Connect to your AlreadyOn server", style = MaterialTheme.typography.headlineSmall)
            Text("Select a server on your home network.")
            if (remaining > 0) Text("Searching for local servers… ${remaining}s remaining")
            else Text(if (servers.isEmpty()) "No local server found. Enter its HTTPS address below." else "Select a server or enter an HTTPS address below.")
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                servers.forEach { server ->
                    Button(modifier = Modifier.tvFocusOutline(), onClick = {
                        select(server)
                    }, enabled = !checking && !state.busy) { Text("${server.name} · ${server.origin.removePrefix("https://")}") }
                }
            }
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("https://", color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedTextField(address.host, { address = address.withHost(it) }, label = { Text("Hostname / IP") },
                    singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next),
                    keyboardActions = KeyboardActions(onNext = { portFocus.requestFocus() }), modifier = Modifier.weight(1f).then(addressNavigation))
                OutlinedTextField(address.port, { address = address.withPort(it) }, label = { Text("Port") },
                    singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    modifier = Modifier.width(90.dp).focusRequester(portFocus).then(addressNavigation))
            }
            Button(modifier = Modifier.tvFocusOutline(), onClick = {
                runCatching { address.origin() }.onSuccess { select(TvServer("AlreadyOn server", it)) }
                    .onFailure { error = it.message ?: "Enter a valid hostname or IP address." }
            }, enabled = address.host.isNotBlank() && !checking && !state.busy) { Text("Check Server") }
            Button(modifier = Modifier.tvFocusOutline(), onClick = { servers.clear(); recent?.let { servers.add(it) }; error = null; remaining = 30; scan++ }) { Text("Rescan") }
        } else {
            Text("${selected!!.name} · ${selected!!.origin.removePrefix("https://")}", style = MaterialTheme.typography.headlineSmall)
            if (checking) Text("Checking server…")
            if (trust != null) {
                val changedCa = state.origin == selected!!.origin && state.trustedCa != null && !sameServerTrust(state.trustedCa, trust!!.certificate)
                if (changedCa) {
                    Text("The server CA has changed. Only accept it if you expected this change; another device may be impersonating your server.", color = MaterialTheme.colorScheme.error)
                }

                OutlinedTextField(username, { username = it }, label = { Text("Username") },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                    keyboardActions = KeyboardActions(onNext = { enterPin() }, onDone = { enterPin() }),
                    modifier = Modifier.fillMaxWidth(if (BuildConfig.IS_TV) 0.6f else 1f).focusRequester(usernameFocus).onPreviewKeyEvent {
                        if (BuildConfig.IS_TV && (it.key == Key.Enter || it.key == Key.NumPadEnter)) {
                            if (it.type == KeyEventType.KeyUp) enterPin()
                            true
                        } else false
                    }, enabled = !state.busy)
                if (!BuildConfig.IS_TV) Text("Enter your 6–12 digit TV PIN")
                ServerPinField(pin, { pin = it }, pinVisible, { pinVisible = !pinVisible },
                    enabled = !state.busy, onDone = ::closePin,
                    modifier = Modifier.fillMaxWidth(if (BuildConfig.IS_TV) 0.6f else 1f).focusRequester(pinFocus))
                if (BuildConfig.IS_TV) {
                    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Button(modifier = Modifier.focusRequester(connectFocus).tvFocusOutline(), onClick = {
                            if (username.isBlank() || pin.length !in 6..12) error = "Enter your username and 6–12 digit TV Pairing Pin."
                            else {
                                error = null
                                keyboard?.hide()
                                model.signInWithPIN(selected!!.origin, username, pin, trust!!.certificate, trust!!.name, acceptNewCa = changedCa)
                            }
                        }, enabled = !state.busy && !checking) { Text(if (changedCa) "Accept New CA" else "Connect") }
                        Button(modifier = Modifier.tvFocusOutline(), onClick = ::goBack,
                            enabled = !state.busy && !checking) { Text("Go Back") }
                        Button(modifier = Modifier.tvFocusOutline(), onClick = { keyboard?.hide(); help = true },
                            enabled = !state.busy) { Text("Help") }
                    }
                } else {
                var acceptingTrust by remember { mutableStateOf(false) }
                LaunchedEffect(changedCa, state.busy) {
                    if (acceptingTrust && !changedCa && !state.busy) {
                        acceptingTrust = false
                        connectFocus.requestFocus()
                    }
                }
                if (changedCa) {
                    Button(modifier = Modifier.focusRequester(connectFocus).tvFocusOutline(), onClick = {
                        acceptingTrust = true
                        model.acceptServerTrust(trust!!.certificate)
                    }, enabled = !state.busy) { Text("Accept New CA") }
                } else Button(modifier = Modifier.focusRequester(connectFocus).tvFocusOutline(), onClick = {
                    if (username.isBlank() || pin.length !in 6..12) error = "Enter your username and 6–12 digit TV PIN."
                    else { model.signInWithPIN(selected!!.origin, username, pin, trust!!.certificate, trust!!.name); pin = ""; pinVisible = false }
                }, enabled = !state.busy) { Text("Connect") }
                }
            }
            if (!BuildConfig.IS_TV || trust == null) Button(modifier = Modifier.tvFocusOutline(), onClick = ::goBack,
                enabled = !checking && !state.busy) { Text(if (BuildConfig.IS_TV) "Go Back" else "Choose Another Server") }
        }
        error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        if (BuildConfig.IS_TV) state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
    }
    }
    if (help) AlertDialog(onDismissRequest = { help = false },
        title = { Text("Connect to your server") },
        text = { Text("Set your username and TV Pairing Pin in your server’s WebUI under Settings. Use that username and 6–12 digit pairing PIN to connect. Your profile PIN is separate.") },
        confirmButton = { TextButton(onClick = { help = false }) { Text("Close") } })
}

/** Use the installed numeric keyboard for server pairing. */
@Composable
internal fun ServerPinField(pin: String, onPin: (String) -> Unit, visible: Boolean,
    onToggleVisibility: () -> Unit, enabled: Boolean, onDone: () -> Unit, modifier: Modifier = Modifier) {
    val visibilityIcon: (@Composable () -> Unit)? = if (BuildConfig.IS_TV) null else {
        { PinVisibilityButton(visible, enabled, onToggleVisibility) }
    }
    OutlinedTextField(pin, { onPin(it.filter { digit -> digit in '0'..'9' }.take(12)) },
        label = { Text(if (BuildConfig.IS_TV) "TV Pairing Pin" else "TV PIN") }, singleLine = true,
        visualTransformation = if (BuildConfig.IS_TV || visible) VisualTransformation.None else PasswordVisualTransformation(),
        trailingIcon = visibilityIcon,
        keyboardOptions = KeyboardOptions(keyboardType = if (BuildConfig.IS_TV) KeyboardType.Number else KeyboardType.NumberPassword, imeAction = ImeAction.Done),
        keyboardActions = KeyboardActions(onDone = { onDone() }), modifier = modifier.onPreviewKeyEvent {
            if (BuildConfig.IS_TV && enabled && it.key in listOf(Key.Enter, Key.NumPadEnter)) {
                if (it.type == KeyEventType.KeyUp) onDone()
                true
            } else false
        }, enabled = enabled)
}

/** Match the WebUI eye control without adding an icon library for two paths. */
@Composable
private fun PinVisibilityButton(visible: Boolean, enabled: Boolean, toggle: () -> Unit) {
    val vector = remember(visible) {
        val path = if (visible)
            "M2.5 12C3.5 9.5 7 5 12 5S20.5 9.5 21.5 12C20.5 14.5 17 19 12 19S3.5 14.5 2.5 12Z M14.5 12A2.5 2.5 0 1 1 9.5 12A2.5 2.5 0 1 1 14.5 12"
        else
            "M3 3L21 21 M10.6 10.6A2 2 0 0 0 13.4 13.4 M9.9 5.2A10.8 10.8 0 0 1 12 5C17 5 20.5 9.5 21.5 12C21.1 13 20.3 14.1 19.2 15.1 M6.2 6.2C3.9 7.6 2.8 9.5 2.5 12C3.5 14.5 7 19 12 19C13.4 19 14.7 18.6 15.8 18"
        ImageVector.Builder(defaultWidth = 24.dp, defaultHeight = 24.dp, viewportWidth = 24f, viewportHeight = 24f)
            .addPath(PathParser().parsePathString(path).toNodes(), stroke = SolidColor(Color.Black),
                strokeLineWidth = 2f, strokeLineCap = StrokeCap.Round, strokeLineJoin = StrokeJoin.Round).build()
    }
    IconButton(onClick = toggle, enabled = enabled) {
        Icon(vector, contentDescription = if (visible) "Hide PIN" else "Show PIN")
    }
}

@Composable
internal fun ServerTrustAlert(state: PhoneState, model: PhoneModel) {
    if (BuildConfig.IS_TV || !state.trustIssue) return
    AlertDialog(onDismissRequest = model::dismissTrustIssue,
        title = { Text("Server certificate trust failed") },
        text = { Text("The server no longer validates against its saved CA, or its certificate has expired. Connections are blocked. Review the server and accept a new CA only if you expected a change.") },
        confirmButton = { TextButton(onClick = model::reviewServerTrust) { Text("Review Server CA") } },
        dismissButton = { TextButton(onClick = model::dismissTrustIssue) { Text("Cancel") } })
}
