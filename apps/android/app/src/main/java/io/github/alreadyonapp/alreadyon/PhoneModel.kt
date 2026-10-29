package io.github.alreadyonapp.alreadyon

import android.app.Application
import android.media.MediaCodecInfo
import android.media.MediaCodecList
import android.media.MediaFormat
import android.os.SystemClock
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.time.Instant
import java.util.UUID

// Use one tag for both the log-level guard and playback timing messages.
internal const val PLAYBACK_LOG_TAG = "AlreadyOnPlayback"

/** Authorized media metadata is ephemeral and never saved in Android instance state. */
internal data class PhonePlayback(val url: String, val title: String, val startMs: Long, val revision: Long, val hlsTargetMs: Long? = null, val capturedAt: Long = SystemClock.elapsedRealtime(), val requestedAt: Long = capturedAt,
    val seriesTitle: String = "", val episodeLabel: String = "", val mediaFileId: Long = -1, val sourceHeight: Int = 720,
    val selectedAudioIndex: Int? = null, val audioTracks: List<PhoneTrack> = emptyList(), val subtitleTracks: List<PhoneTrack> = emptyList(),
    val streamOffsetSeconds: Double = 0.0, val hlsOriginMs: Long? = null, val trustedCa: String? = null, val entryStartsAt: String? = null)
internal data class PhoneState(
    val username: String = "", val role: String = "viewer", val mustChangePassword: Boolean = false,
    val passwordLocked: Boolean = true, val settings: String? = null, val notice: String? = null,
    val origin: String = "", val serverName: String = "AlreadyOn", val trustedCa: String? = null, val signedIn: Boolean = false, val busy: Boolean = false,
    val error: String? = null, val trustIssue: Boolean = false, val profiles: List<ViewerProfile> = emptyList(),
    val restoringConnection: Boolean = false, val playbackReady: Boolean = false,
    val profile: ViewerProfile? = null, val channels: List<GuideChannel> = emptyList(),
    val channel: GuideChannel? = null, val playback: PhonePlayback? = null,
    val quality: Int = 0, val subtitleIndex: Int? = null, val subtitleFileId: Long? = null, val audioIndex: Int? = null, val audioFileId: Long? = null,
    val paused: Boolean = false, val muted: Boolean = false,
)

/** Owns the phone session and single playback lease independently of Activity rotations. */
internal class PhoneModel(application: Application) : AndroidViewModel(application) {
    private val sessionStore = SessionStore(application)
    private val preferences = application.getSharedPreferences("connection", Application.MODE_PRIVATE)
    private val mutable = MutableStateFlow(PhoneState(serverName = preferences.getString("server_name", "AlreadyOn").orEmpty(), origin = preferences.getString("server_origin", "").orEmpty(), trustedCa = preferences.getString("trusted_ca", null), restoringConnection = BuildConfig.IS_TV))
    val state = mutable.asStateFlow()
    private var client: ServerClient? = null
    private var action: Job? = null
    private var viewing: Job? = null
    private var guideEntry: Job? = null
    private var serial = 0L
    private var subtitleOverride = false
    private var preferredSubtitle: PhoneTrack? = null
    private var capabilities: JSONObject? = null
    private var capabilityTime = 0L

    private fun update(block: (PhoneState) -> PhoneState) { mutable.value = block(mutable.value) }
    suspend fun profileIcon(path: String) = client?.profileIcon(path)

    fun dismissError() = update { it.copy(error = null) }
    fun dismissNotice() = update { it.copy(notice = null) }

    private fun perform(block: suspend () -> Unit) {
        if (action?.isActive == true) return
        action = viewModelScope.launch {
            update { it.copy(busy = true, error = null) }
            try { block() } catch (e: CancellationException) { throw e }
            catch (e: Exception) {
                handleFailure(e)
                update { it.copy(error = safeError(e)) }
            }
            finally { update { it.copy(busy = false, restoringConnection = false) } }
        }
    }

    init { reconnect() }

    /** Startup restores only an attached server; local Sign Out waits for server selection. */
    fun reconnect() = perform {
        if (!preferences.getBoolean("server_detached", false)) resumeSavedServer(state.value.origin)
    }

    /** Revalidate an exact-origin encrypted session before skipping the credentials screen.
     * A network/TLS failure retains the cookie; server rejection removes it. Profile PINs
     * remain independent and are never bypassed by restoring the account session.
     */
    suspend fun resumeSavedServer(rawOrigin: String): Boolean {
        val origin = serverOrigin(rawOrigin) ?: return false
        if (origin != serverOrigin(state.value.origin)) return false
        val api = client ?: ServerClient(origin, sessionStore, state.value.trustedCa).also { client = it }
        if (!api.hasSession()) return false
        val status = try { JSONObject(api.request("/api/auth/status")) }
        catch (error: ServerFailure) {
            if (!error.sessionExpired) throw error
            expireSession()
            return false
        }
        if (!status.optBoolean("authenticated")) { expireSession(); return false }
        val profiles = if (status.optBoolean("mustChangePassword")) emptyList() else api.profiles()
        preferences.edit().putBoolean("server_detached", false).commit()
        readAccount(status)
        update { it.copy(profiles = profiles) }
        if (state.value.mustChangePassword) return true
        val saved = profiles.find { it.id == preferences.getLong("profile_id", -1) && !it.hasPin }
        if (saved != null) {
            val channels = api.guide(saved)
            update { it.copy(profile = saved, channels = channels) }
            restoreChannel(saved, channels)
        }
        return true
    }

    private fun expireSession() {
        stopWatching()
        client?.clearSession(); client = null
        preferences.edit().remove("profile_id").apply()
        update { PhoneState(origin = it.origin, serverName = it.serverName, trustedCa = it.trustedCa) }
    }

    fun login(rawOrigin: String, username: String, password: String) = perform {
        val origin = serverOrigin(rawOrigin) ?: throw IOException("Enter a valid HTTPS server address.")
        if (isLoopback(java.net.URI(origin).host)) throw IOException("Localhost points to this phone. Use the server hostname.")
        client?.clearSession()
        val api = ServerClient(origin, sessionStore)
        val status = JSONObject(api.request("/api/auth/login", "POST",
            JSONObject().put("username", username).put("password", password)))
        if (!status.optBoolean("authenticated")) throw IOException("Sign-in was not accepted.")
        client = api
        preferences.edit().putString("server_origin", origin).putBoolean("server_detached", false).remove("bypass_tls").remove("trusted_ca").commit()
        update { it.copy(origin = origin, trustedCa = null) }
        readAccount(JSONObject(api.request("/api/auth/status")))
        if (state.value.mustChangePassword) return@perform
        val profiles = api.profiles()
        update { it.copy(profiles = profiles) }
    }

    /** Native clients accept an instance CA before sending its PIN or saving a session. */
    fun signInWithPIN(origin: String, username: String, pin: String, trustedCa: String, serverName: String = "AlreadyOn", acceptNewCa: Boolean = false) = perform {
        val normalized = serverOrigin(origin) ?: throw IOException("Invalid local server address.")
        if (acceptNewCa && (!BuildConfig.IS_TV || normalized != state.value.origin)) throw IOException("Select the saved server before accepting its new CA.")
        if (username.isBlank() || pin.length !in 6..12 || pin.any { it !in '0'..'9' }) throw IOException("Enter your username and 6–12 digit TV PIN.")
        if (normalized == state.value.origin && state.value.trustedCa != null && !sameServerTrust(state.value.trustedCa, trustedCa) && !acceptNewCa) {
            throw IOException("The server CA changed. Review and accept the new CA before connecting.")
        }
        client?.clearSession()
        if (acceptNewCa) sessionStore.clear(normalized)
        val api = ServerClient(normalized, sessionStore, trustedCa = trustedCa)
        if (acceptNewCa) {
            preferences.edit().putString("trusted_ca", trustedCa).commit()
            client = null
            update { it.copy(trustedCa = trustedCa, trustIssue = false) }
        }
        val status = JSONObject(api.request("/api/auth/tv-login", "POST", JSONObject().put("username", username.trim()).put("pin", pin)))
        if (!status.optBoolean("authenticated")) throw IOException("TV PIN was not accepted.")
        client = api
        preferences.edit().putString("server_origin", normalized).putBoolean("server_detached", false).remove("bypass_tls")
            .putString("trusted_ca", trustedCa).putString("server_name", serverName).commit()
        update { it.copy(origin = normalized, serverName = serverName, trustedCa = trustedCa) }
        readAccount(status)
        val profiles = api.profiles()
        update { it.copy(profiles = profiles) }
    }

    /** Account restrictions take precedence over profile and playback navigation. */
    private fun readAccount(status: JSONObject) = update { it.copy(
        signedIn = status.optBoolean("authenticated"), username = status.optString("username"),
        role = status.optString("role", "viewer"), mustChangePassword = status.optBoolean("mustChangePassword"),
        passwordLocked = status.optBoolean("passwordLocked", true)) }

    fun dismissTrustIssue() = update { it.copy(trustIssue = false) }

    fun reviewServerTrust() {
        stopWatching()
        update { it.copy(signedIn = false, trustIssue = false, error = null, settings = null, profile = null) }
    }

    /** Replacing saved trust requires an explicit user action; never retry under a new CA silently. */
    fun acceptServerTrust(certificate: String) = perform {
        val normalized = serverOrigin(state.value.origin) ?: throw IOException("Invalid local server address.")
        // Validate the replacement before persisting it. Reauthentication runs separately.
        ServerClient(normalized, sessionStore, certificate)
        preferences.edit().putString("trusted_ca", certificate).commit()
        client = null
        update { it.copy(trustedCa = certificate, trustIssue = false, error = null) }
        update { it.copy(notice = "New server CA accepted. Connect with your username and PIN.") }
    }

    private fun handleFailure(error: Exception) {
        if (isServerTrustFailure(error)) {
            stopWatching()
            update { if (BuildConfig.IS_TV) it.copy(trustIssue = true, signedIn = false, profile = null, settings = null)
                else it.copy(trustIssue = true) }
        }
        if (error is ServerFailure && error.sessionExpired) expireSession()
        if (error is ServerFailure && error.code == "password_change_required") {
            stopWatching()
            update { it.copy(mustChangePassword = true, settings = null, profile = null, profiles = emptyList(), channels = emptyList()) }
        }
    }

    /** Screen-owned polling is cancelled on exit; use the existing authenticated client. */
    suspend fun dashboardRequest(path: String): String {
        val api = client ?: throw IOException("Sign in to view Dashboard.")
        try { return api.request(path) }
        catch (e: Exception) { handleFailure(e); throw e }
    }

    fun openSettings(screen: String) {
        if (screen == "account" && state.value.profile?.role != "admin") return
        stopWatching()
        update { it.copy(settings = screen, error = null, notice = null) }
    }
    fun closeSettings() {
        update { it.copy(settings = null, error = null, notice = null) }
        enterGuide()
    }

    /** Every guide entry re-reads server state so another device’s successful tune wins. */
    fun enterGuide() {
        guideEntry?.cancel()
        guideEntry = viewModelScope.launch {
            // Do not lose navigation restoration when a periodic guide request is already running.
            action?.join()
            perform {
                val current = state.value.profile ?: return@perform
                if (state.value.settings != null) return@perform
                val api = requireNotNull(client)
                val profile = api.profiles().find { it.id == current.id } ?: return@perform
                val channels = api.guide(profile)
                update { it.copy(profile = profile, channels = channels, muted = true, paused = false) }
                restoreChannel(profile, channels)
            }
        }
    }

    /** Rename first; retain the successful name if a subsequent password update fails. */
    fun saveAccount(current: String, username: String, password: String, confirmation: String) = perform {
        val api = requireNotNull(client)
        if (state.value.passwordLocked) throw ServerFailure(403, "This account is locked.")
        if (password != confirmation || (password.isNotEmpty() && password.length < 4))
            throw ServerFailure(400, "New passwords must match and contain at least four characters.")
        if (state.value.mustChangePassword && password.isEmpty()) throw ServerFailure(400, "Enter a new password.")
        if (!state.value.mustChangePassword && username.trim() != state.value.username) {
            val result = JSONObject(api.request("/api/auth/username", "POST", JSONObject().put("currentPassword", current).put("newUsername", username.trim())))
            update { it.copy(username = result.getString("username")) }
        }
        if (password.isNotEmpty()) api.request("/api/auth/password", "POST", JSONObject().put("currentPassword", current).put("newPassword", password))
        val forced = state.value.mustChangePassword
        readAccount(JSONObject(api.request("/api/auth/status")))
        if (forced) {
            val profiles = api.profiles()
            update { it.copy(profiles = profiles) }
        }
        update { it.copy(notice = "Changes saved.") }
    }

    /** Save only changed fields; unrelated edits from other clients remain intact. */
    fun saveProfile(patch: JSONObject, upload: ByteArray? = null, keepOpen: Boolean = false, onSaved: (ViewerProfile) -> Unit = {}) = perform {
        update { it.copy(notice = null) }
        val api = requireNotNull(client)
        val profile = state.value.profile ?: return@perform
        if (patch.length() > 0) api.request("/api/profiles/${profile.id}", "PATCH", patch)
        if (upload != null) api.request("/api/profiles/${profile.id}/icon", "POST", upload = upload)
        val profiles = api.profiles()
        val stayInSettings = keepOpen && state.value.settings == "profile"
        update { it.copy(profiles = profiles, profile = profiles.find { p -> p.id == profile.id }, settings = if (stayInSettings) it.settings else null, notice = if (stayInSettings) "Changes saved." else it.notice) }
        val updated = state.value.profile ?: return@perform
        if (stayInSettings) { onSaved(updated); return@perform }
        val channels = api.guide(updated)
        update { it.copy(channels = channels) }
        restoreChannel(updated, channels)
    }

    /** PIN actions apply immediately, independently of the settings draft, like the WebUI. */
    fun savePin(pin: String?) = perform {
        val profile = state.value.profile ?: return@perform
        requireNotNull(client).request("/api/profiles/${profile.id}/pin", if (pin == null) "DELETE" else "PUT",
            pin?.let { JSONObject().put("pin", it) })
        val profiles = requireNotNull(client).profiles()
        update { it.copy(profiles = profiles, profile = profiles.find { p -> p.id == profile.id }, notice = "PIN updated.") }
    }

    fun selectProfile(profile: ViewerProfile, pin: String) = perform {
        val api = requireNotNull(client)
        if (profile.hasPin) {
            val result = JSONObject(api.request("/api/profiles/${profile.id}/verify-pin", "POST", JSONObject().put("pin", pin)))
            if (!result.optBoolean("valid")) throw IOException("Profile PIN was not accepted.")
        }
        // The chooser can outlive a tune on another device; refresh history at selection time.
        val profiles = api.profiles()
        val current = profiles.find { it.id == profile.id } ?: throw IOException("Profile is no longer available.")
        val channels = api.guide(current)
        preferences.edit().putLong("profile_id", current.id).apply()
        update { it.copy(profile = current, profiles = profiles, channels = channels) }
        restoreChannel(current, channels)
    }

    private fun restoreChannel(profile: ViewerProfile, channels: List<GuideChannel>) {
        subtitleOverride = false
        preferredSubtitle = null
        update { it.copy(muted = true, paused = false, audioIndex = null, audioFileId = null, subtitleIndex = null, subtitleFileId = null) }
        (channels.find { it.id == profile.lastChannel && it.programs.isNotEmpty() }
            ?: channels.firstOrNull { it.programs.isNotEmpty() })?.let { watch(it, restart = true) }
    }
    fun setQuality(height: Int) {
        update { it.copy(quality = height) }
        state.value.channel?.let { watch(it, restart = true) }
    }
    fun setAudio(track: PhoneTrack) {
        if (state.value.profile?.subtitleMode == "other_audio") {
            subtitleOverride = false
            preferredSubtitle = null
            update { it.copy(subtitleFileId = null) }
        }
        update { it.copy(audioIndex = track.index, audioFileId = it.playback?.mediaFileId) }
        state.value.channel?.let { watch(it, restart = true) }
    }
    fun setSubtitle(index: Int?) {
        subtitleOverride = true
        preferredSubtitle = state.value.playback?.subtitleTracks?.find { it.index == index }
        update { it.copy(subtitleIndex = index, subtitleFileId = it.playback?.mediaFileId) }
    }

    /** Circular neighbours follow visible guide order without changing player settings. */
    fun stepChannel(direction: Int) {
        val current = state.value.channel ?: return
        nextPlayableChannel(state.value.channels, current.id, direction)?.let { watch(it, restart = true) }
    }

    private fun selectedSubtitle(media: PhonePlayback, profile: ViewerProfile): Int? {
        if (state.value.subtitleFileId == media.mediaFileId) return state.value.subtitleIndex
        if (subtitleOverride) {
            val preferred = preferredSubtitle ?: return null // Explicit Off survives new media.
            // Track indices belong to a file. Match the choice, then its language on the new file.
            return (media.subtitleTracks.find { it.label == preferred.label }
                ?: media.subtitleTracks.find { it.language == preferred.language && it.language != "und" })?.index
        }
        return profileSubtitle(media, profile)
    }

    /** Poll shared preferences without retuning for names, avatars or another device's channel.
     * Explicit playback-track choices stay local; changed profile defaults apply automatically.
     */
    fun refreshProfiles() = perform {
        val api = client ?: return@perform
        if (!state.value.signedIn) return@perform
        val current = state.value.profile
        val profiles = api.profiles()
        val updated = profiles.find { it.id == current?.id }
        if (current != null && updated == null) {
            stopWatching()
            preferences.edit().remove("profile_id").apply()
        }
        val channels = if (current != null && updated != null && current.tier != updated.tier) api.guide(updated) else state.value.channels
        update { it.copy(profiles = profiles, profile = updated, channels = if (current != null && updated == null) emptyList() else channels) }
        if (updated != null && state.value.channel != null && channels.none { it.id == state.value.channel?.id }) {
            stopWatching()
        }
        val defaultsChanged = current != null && updated != null &&
            (current.language != updated.language || current.subtitleMode != updated.subtitleMode || current.subtitleLanguage != updated.subtitleLanguage)
        if (defaultsChanged) {
            if (!subtitleOverride) update { it.copy(subtitleFileId = null) }
            state.value.channel?.let { watch(it, restart = true) }
        }
    }

    fun refreshGuide() = perform {
        val profile = state.value.profile ?: return@perform
        val channels = requireNotNull(client).guide(profile)
        update { it.copy(channels = channels) }
    }

    fun changeProfile() {
        stopWatching()
        preferences.edit().remove("profile_id").apply()
        update { it.copy(profile = null, channels = emptyList(), settings = null, error = null) }
    }

    /** Native Sign Out detaches this server locally, retaining its encrypted session.
     * Web session revocation remains authoritative when the server is selected again.
     */
    fun logout() = perform {
        stopWatching()
        viewing?.join()
        client = null
        preferences.edit().remove("profile_id").putBoolean("server_detached", true).commit()
        update { PhoneState(origin = it.origin, serverName = it.serverName, trustedCa = it.trustedCa) }
    }

    fun watch(channel: GuideChannel, restart: Boolean = false) {
        // Opening an already playing channel must not tear down its lease and buffer again.
        if (!restart && state.value.channel?.id == channel.id && state.value.playback != null) return
        val api = client ?: return
        val profile = state.value.profile ?: return
        if (state.value.mustChangePassword || state.value.settings != null) return
        val requestedAt = SystemClock.elapsedRealtime()
        val viewerId = UUID.randomUUID().toString()
        val previous = viewing
        previous?.cancel()
        update { it.copy(channel = channel, playback = null, playbackReady = false, error = null) }
        viewing = viewModelScope.launch {
            previous?.join()
            var activityStarted = false
            try {
                while (true) {
                    var token: String? = null
                    try {
                        // Finish issuance before cancellation so every issued token is known and revocable.
                        token = withContext(NonCancellable) {
                            JSONObject(api.request("/api/playback/sessions", "POST", JSONObject().put("channelId", channel.id))).getString("token")
                        }
                        val body = JSONObject().put("token", token).put("profileId", profile.id)
                            .put("audioLanguage", profile.language)
                            // Auto stays within this client's tested 720p ceiling, including remembered-channel starts.
                            .put("maxHeight", state.value.quality.takeIf { it > 0 } ?: 720)
                            .put("capabilities", decoderCapabilities())
                        if (state.value.audioIndex != null && state.value.audioFileId != null) body
                            .put("audioTrackIndex", state.value.audioIndex).put("audioMediaFileId", state.value.audioFileId)
                        var tune = JSONObject(api.request("/api/channels/${channel.id}/tune", "POST", body))
                        val started = SystemClock.elapsedRealtime()
                        while (!tune.getBoolean("ready")) {
                            if (SystemClock.elapsedRealtime() - started > 90_000) throw IOException("Playback did not become ready. Try again.")
                            delay(if (BuildConfig.IS_TV) 200 else 1000)
                            tune = status(api, channel.id, token)
                        }
                        if (android.util.Log.isLoggable(PLAYBACK_LOG_TAG, android.util.Log.DEBUG)) android.util.Log.d(PLAYBACK_LOG_TAG, "Tune ready preparationMs=${SystemClock.elapsedRealtime()-requestedAt} mode=${tune.getJSONObject("plan").getString("mode")}")
                        val now = Instant.parse(tune.getString("serverTime"))
                        val end = Instant.parse(tune.getString("entryEndsAt"))
                        val delaySeconds = tune.optDouble("presentationDelaySeconds", 3.0)
                        val remainingMs = (end.toEpochMilli() - now.toEpochMilli() + delaySeconds * 1000).toLong()
                        if (remainingMs <= 0) continue
                        val direct = tune.getJSONObject("plan").getString("mode") == "direct"
                        val sourcePosition = (now.toEpochMilli() - Instant.parse(tune.getString("entryStartsAt")).toEpochMilli()) / 1000.0 +
                            tune.optDouble("sourceOffsetSeconds") - delaySeconds
                        val offset = if (direct) sourcePosition else sourcePosition - tune.optDouble("streamOffsetSeconds")
                        val media = PhonePlayback(api.mediaURL(tune.getString("url"), channel.id),
                            tune.optJSONObject("program")?.optString("title") ?: channel.name,
                            (offset.coerceAtLeast(0.0) * 1000).toLong(), ++serial,
                            if (direct || tune.optString("hlsOrigin").isBlank()) null else
                                Instant.parse(tune.getString("hlsOrigin")).toEpochMilli() + (offset * 1000).toLong(),
                            requestedAt = requestedAt,
                            seriesTitle = tune.optJSONObject("program")?.takeIf { it.optString("mediaType") == "episode" }?.optString("seriesTitle").orEmpty(),
                            episodeLabel = tune.optJSONObject("program")?.takeIf { it.optString("mediaType") == "episode" && it.has("episodeNumber") }?.let {
                                "S${it.optInt("seasonNumber").toString().padStart(2, '0')}E${it.optInt("episodeNumber").toString().padStart(2, '0')} - "
                            }.orEmpty(),
                            mediaFileId = tune.optLong("mediaFileId", -1), sourceHeight = tune.optInt("sourceHeight", 720),
                            selectedAudioIndex = tune.getJSONObject("plan").optInt("audioTrackIndex", -1).takeIf { it >= 0 },
                            audioTracks = phoneTracks(tune.optJSONArray("audioTracks")), subtitleTracks = phoneTracks(tune.optJSONArray("subtitles")),
                            streamOffsetSeconds = if (direct) 0.0 else tune.optDouble("streamOffsetSeconds"),
                            entryStartsAt = tune.getString("entryStartsAt"),
                            trustedCa = state.value.trustedCa, hlsOriginMs = tune.optString("hlsOrigin").takeIf { it.isNotBlank() }?.let { Instant.parse(it).toEpochMilli() })
                        update { it.copy(playback = media, profile = it.profile?.copy(lastChannel = channel.id),
                            subtitleIndex = selectedSubtitle(media, state.value.profile ?: profile),
                            subtitleFileId = media.mediaFileId) }
                        activityStarted = true
                        reportActivity(api, viewerId, channel.id, token, profile.id, state.value.paused)
                        val boundary = SystemClock.elapsedRealtime() + remainingMs
                        var lastStatusAt = SystemClock.elapsedRealtime()
                        while (SystemClock.elapsedRealtime() < boundary) {
                            delay(minOf(5_000, (boundary - SystemClock.elapsedRealtime()).coerceAtLeast(1)))
                            val elapsed = SystemClock.elapsedRealtime()
                            if (elapsed < boundary) {
                                reportActivity(api, viewerId, channel.id, token, profile.id, state.value.paused)
                                if (elapsed - lastStatusAt >= 10_000) {
                                    status(api, channel.id, token)
                                    lastStatusAt = elapsed
                                }
                            }
                        }
                        update { it.copy(playback = null) }
                    } finally {
                        // Best-effort remote release runs even when channel switching cancels preparation.
                        if (token != null) withContext(NonCancellable) {
                            try { withTimeout(5000) { api.request("/api/playback/sessions", "DELETE", JSONObject().put("channelId", channel.id).put("token", token)) } }
                            catch (_: Exception) { /* Offline servers expire the bounded viewer lease themselves. */ }
                        }
                    }
                }
            } catch (e: CancellationException) { throw e }
            catch (e: Exception) {
                handleFailure(e)
                update { it.copy(playback = null, error = safeError(e)) }
            } finally {
                if (activityStarted) withContext(NonCancellable) { endActivity(api, viewerId) }
            }
        }
    }

    fun playbackStarted(revision: Long) {
        if (state.value.playback?.revision == revision) update { it.copy(playbackReady = true) }
    }

    // Backgrounding releases the lease but retains channel choice for a fresh live tune on return.
    fun suspendWatching() {
        viewing?.cancel()
        update { it.copy(playback = null) }
    }
    fun resumeWatching() {
        if (viewing?.isActive != true && state.value.profile != null && state.value.settings == null) enterGuide()
    }

    fun stopWatching() {
        viewing?.cancel()
        update { it.copy(channel = null, playback = null, paused = false) }
    }

    fun togglePause() {
        val resume = state.value.paused
        update { it.copy(paused = !it.paused) }
        // Resume rejoins the server's current schedule rather than playing behind live time.
        if (resume) state.value.channel?.let { watch(it, restart = true) }
    }
    // Physical phone buttons control output volume; the player controls only mute.
    // Deliberate fullscreen entry starts audible playback without toggling an already audible stream.
    fun unmute() = update { it.copy(muted = false) }
    fun toggleMute() = update { it.copy(muted = !it.muted) }

    fun playerFailed(revision: Long, error: Exception? = null) {
        if (state.value.playback?.revision != revision) return
        if (error != null) {
            handleFailure(error)
            if (state.value.trustIssue) return
        }
        stopWatching()
        update { it.copy(error = "The phone could not play this stream. Select the channel to try again.") }
    }

    private suspend fun status(api: ServerClient, channel: Long, token: String): JSONObject =
        JSONObject(api.request("/api/playback/status", "POST", JSONObject().put("channelId", channel).put("token", token)))

    private suspend fun reportActivity(api: ServerClient, viewerId: String, channel: Long, token: String,
        profileId: Long, paused: Boolean) {
        withTimeoutOrNull(4000) {
            try {
                api.request("/api/playback/activity", "POST", JSONObject()
                    .put("viewerId", viewerId).put("channelId", channel).put("token", token)
                    .put("profileId", profileId).put("paused", paused)
                    .put("appVersion", BuildConfig.VERSION_NAME))
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
            }
        }
    }

    private suspend fun endActivity(api: ServerClient, viewerId: String) {
        withTimeoutOrNull(5000) {
            try {
                api.request("/api/playback/activity", "DELETE", JSONObject().put("viewerId", viewerId))
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
            }
        }
    }

    /** Query Android decoder support conservatively; do not advertise HDR, HEVC or direct containers yet. */
    private fun decoderCapabilities(): JSONObject {
        if (capabilities != null && SystemClock.elapsedRealtime() - capabilityTime < 86_400_000) return capabilities!!
        val codecs = MediaCodecList(MediaCodecList.REGULAR_CODECS)
        val video = JSONArray()
        val audio = JSONArray()
        val format = MediaFormat.createVideoFormat("video/avc", 1280, 720).apply { setInteger(MediaFormat.KEY_FRAME_RATE, 30); setInteger(MediaFormat.KEY_PROFILE, MediaCodecInfo.CodecProfileLevel.AVCProfileConstrainedBaseline); setInteger(MediaFormat.KEY_LEVEL, MediaCodecInfo.CodecProfileLevel.AVCLevel31) }
        if (codecs.findDecoderForFormat(format) != null) video.put(JSONObject().put("codec", "h264")
            .put("profile", "Constrained Baseline").put("level", 31).put("width", 1280).put("height", 720)
            .put("fps", 30).put("bitrate", 5_000_000).put("pixelFormat", "yuv420p"))
        if (codecs.findDecoderForFormat(MediaFormat.createAudioFormat("audio/mp4a-latm", 48000, 2).apply { setInteger(MediaFormat.KEY_AAC_PROFILE, MediaCodecInfo.CodecProfileLevel.AACObjectLC); setInteger(MediaFormat.KEY_BIT_RATE, 192000) }) != null) {
            audio.put(JSONObject().put("codec", "aac").put("channels", 2).put("sampleRate", 48000).put("bitrate", 192000).put("profile", "LC").put("hls", true))
        }
        // No direct containers forces the existing server HLS compatibility path; mbps=0 uses its 25 Mbps default.
        capabilities = JSONObject().put("hls", true).put("containers", JSONArray()).put("video", video).put("audio", audio).put("mbps", 0)
        capabilityTime = SystemClock.elapsedRealtime()
        return capabilities!!
    }
}

/** Avoid reflecting TLS exceptions or request URLs, which can contain scoped media tokens. */
internal fun safeError(e: Exception): String = when (e) {
    is ServerFailure -> e.message ?: "The server refused the request."
    is javax.net.ssl.SSLException -> "TLS verification failed. Check the server hostname and certificate trust."
    is java.net.UnknownHostException -> "The server hostname could not be found."
    else -> "Could not connect or read the server response. Check the address and network, then try again."
}

/** Media track tags may use ISO 639-2 while profile preferences use ISO 639-1. */
