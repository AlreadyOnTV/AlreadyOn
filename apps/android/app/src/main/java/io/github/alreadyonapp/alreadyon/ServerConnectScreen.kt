// Shared phone/tablet and TV discovery, address fallback and account PIN onboarding.
package io.github.alreadyonapp.alreadyon

import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.ui.Alignment
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.window.Dialog
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
    var selected by remember { mutableStateOf<TvServer?>(null) }
    var trust by remember { mutableStateOf<TvTrust?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var checking by remember { mutableStateOf(false) }
    var pin by remember { mutableStateOf("") }
    var pinVisible by remember { mutableStateOf(false) }
    var username by remember { mutableStateOf("") }
    var address by remember { mutableStateOf("https://") }
    var remaining by remember { mutableIntStateOf(30) }
    val scope = rememberCoroutineScope()
    val keyboard = LocalSoftwareKeyboardController.current
    val focusManager = LocalFocusManager.current
    val usernameFocus = remember { FocusRequester() }
    LaunchedEffect(trust) { if (trust != null) usernameFocus.requestFocus() }
    val pinFocus = remember { FocusRequester() }
    val connectFocus = remember { FocusRequester() }
    var pinKeypad by remember { mutableStateOf(false) }
    fun enterPin() { keyboard?.hide(); pinFocus.requestFocus(); pinKeypad = true }
    fun closePin() {
        pinKeypad = false
        focusManager.clearFocus(force = true)
        if (username.isNotBlank() && pin.length in 6..12) connectFocus.requestFocus()
    }
    LaunchedEffect(scan) {
        remaining = 30
        while (remaining > 0) { delay(1000); remaining-- }
    }
    fun select(server: TvServer) {
        selected = server; checking = true; error = null; trust = null; model.dismissError()
        scope.launch {
            try { val result = probe(server); selected = server.copy(name = result.name); trust = result }
            catch (e: kotlinx.coroutines.CancellationException) { throw e }
            catch (e: Exception) { error = e.message ?: "Could not check server." }
            finally { checking = false }
        }
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
    Box(modifier.fillMaxWidth(), contentAlignment = if (BuildConfig.IS_TV) Alignment.Center else Alignment.TopStart) {
    Column(Modifier.widthIn(max = if (BuildConfig.IS_TV) 560.dp else androidx.compose.ui.unit.Dp.Infinity).fillMaxWidth()
        .verticalScroll(rememberScrollState()).padding(12.dp),
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
            OutlinedTextField(address, { address = it }, label = { Text("HTTPS server address") },
                singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri), modifier = Modifier.fillMaxWidth())
            Button(modifier = Modifier.tvFocusOutline(), onClick = {
                val input = address.trim().let { if ("://" in it) it else "https://$it" }
                val origin = serverOrigin(input)?.toHttpUrl()?.let { url ->
                    if (url.port == 443 && java.net.URI(input).port == -1) url.newBuilder().port(19900).build().toString().removeSuffix("/")
                    else url.toString().removeSuffix("/")
                }
                if (origin == null) error = "Enter a valid HTTPS server address."
                else select(TvServer("AlreadyOn server", origin))
            }, enabled = address.removePrefix("https://").isNotBlank() && !checking && !state.busy) { Text("Check Server") }
            Button(modifier = Modifier.tvFocusOutline(), onClick = { servers.clear(); recent?.let { servers.add(it) }; error = null; remaining = 30; scan++ }) { Text("Rescan") }
        } else {
            Text("${selected!!.name} · ${selected!!.origin.removePrefix("https://")}", style = MaterialTheme.typography.headlineSmall)
            if (checking) Text("Checking server…")
            if (trust != null) {
                val changedCa = state.origin == selected!!.origin && state.trustedCa != null && state.trustedCa != trust!!.certificate
                if (changedCa) {
                    Text("The server CA has changed. Only accept it if you expected this change; another device may be impersonating your server.", color = MaterialTheme.colorScheme.error)
                    Button(onClick = { model.acceptServerTrust(trust!!.certificate) }, enabled = !state.busy) { Text("Accept New CA") }
                }

                OutlinedTextField(username, { username = it }, label = { Text("Username") },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(imeAction = if (BuildConfig.IS_TV) ImeAction.Next else ImeAction.Default),
                    keyboardActions = KeyboardActions(onNext = { if (BuildConfig.IS_TV) enterPin() }, onDone = { if (BuildConfig.IS_TV) enterPin() }),
                    modifier = Modifier.fillMaxWidth(if (BuildConfig.IS_TV) 0.6f else 1f).focusRequester(usernameFocus).onPreviewKeyEvent {
                        if (BuildConfig.IS_TV && (it.key == Key.Enter || it.key == Key.NumPadEnter)) {
                            if (it.type == KeyEventType.KeyUp) enterPin()
                            true
                        } else false
                    }, enabled = !state.busy)
                Text("Enter your 6–12 digit TV PIN")
                if (!BuildConfig.IS_TV) OutlinedTextField(pin, { pin = it.filter { digit -> digit in '0'..'9' }.take(12) },
                    label = { Text("TV PIN") }, singleLine = true,
                    visualTransformation = if (pinVisible) VisualTransformation.None else PasswordVisualTransformation(),
                    trailingIcon = { PinVisibilityButton(pinVisible, !state.busy) { pinVisible = !pinVisible } },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword), modifier = Modifier.fillMaxWidth(), enabled = !state.busy)
                else OutlinedTextField(pin, {}, label = { Text("TV PIN") }, readOnly = true,
                    singleLine = true, visualTransformation = PasswordVisualTransformation(),
                    modifier = Modifier.fillMaxWidth(0.6f).focusRequester(pinFocus).onFocusChanged {
                        if (it.isFocused) { keyboard?.hide(); pinKeypad = true }
                    }, enabled = !state.busy)
                Button(modifier = Modifier.focusRequester(connectFocus).tvFocusOutline(), onClick = { model.signInWithPIN(selected!!.origin, username, pin, trust!!.certificate, trust!!.name); pin = ""; pinVisible = false },
                    enabled = !changedCa && username.isNotBlank() && pin.length in 6..12 && !state.busy) { Text("Connect") }
            }
            Button(modifier = Modifier.tvFocusOutline(), onClick = { selected = null; trust = null; pin = ""; pinVisible = false; error = null; model.dismissError() }, enabled = !checking && !state.busy) { Text("Choose Another Server") }
        }
        error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        if (BuildConfig.IS_TV) state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
    }
    }
    if (BuildConfig.IS_TV && pinKeypad) Dialog(onDismissRequest = { closePin() }) {
        val firstDigit = remember { FocusRequester() }
        val keypadView = LocalView.current
        // The dialog must own window focus before assigning focus to its first key.
        LaunchedEffect(Unit) {
            while (!keypadView.hasWindowFocus()) delay(16)
            firstDigit.requestFocus()
        }
        Surface(shape = MaterialTheme.shapes.large) {
            Column(Modifier.width(360.dp).padding(20.dp), horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("TV PIN", style = MaterialTheme.typography.headlineSmall)
                Text("●".repeat(pin.length), style = MaterialTheme.typography.headlineMedium)
                listOf(listOf("1", "2", "3"), listOf("4", "5", "6"), listOf("7", "8", "9"), listOf("Clear", "0", "Delete")).forEach { row ->
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        row.forEach { digit ->
                            Button(onClick = {
                                pin = when (digit) { "Clear" -> ""; "Delete" -> pin.dropLast(1); else -> (pin + digit).take(12) }
                            }, modifier = Modifier.width(100.dp).then(if (digit == "1") Modifier.focusRequester(firstDigit) else Modifier).tvFocusOutline()) { Text(digit) }
                        }
                    }
                }
                Button(onClick = { closePin() }, modifier = Modifier.tvFocusOutline()) { Text("✓ Done") }
            }
        }
    }
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
    if (!state.trustIssue) return
    AlertDialog(onDismissRequest = model::dismissTrustIssue,
        title = { Text("Server certificate trust failed") },
        text = { Text("The server no longer validates against its saved CA, or its certificate has expired. Connections are blocked. Review the server and accept a new CA only if you expected a change.") },
        confirmButton = { TextButton(onClick = model::reviewServerTrust) { Text("Review Server CA") } },
        dismissButton = { TextButton(onClick = model::dismissTrustIssue) { Text("Cancel") } })
}
