package io.github.alreadyonapp.alreadyon

/** Only the management authentication challenge can invalidate the account
 * cookie. Scoped playback failures and rejected profile PINs are independent. */
internal fun expiresAccountSession(status: Int, path: String, challenge: String?): Boolean =
    status == 401 && challenge.equals("Bearer", ignoreCase = true) &&
        path != "/api/auth/login" && path != "/api/auth/tv-login" && !path.endsWith("/verify-pin")

/** Keep capacity errors distinct from client codec support on phone and TV. */
internal fun playbackFailureMessage(status: Int, path: String, code: String, challenge: String?): String? = when {
    code == "server_capacity_unsustainable" -> "The server cannot sustain this output quality. Choose a lower quality or try again after capacity is remeasured."
    code == "server_capacity_measurement_unavailable" -> "The server has no current capacity measurement for this video pipeline. Try again after capacity is measured."
    code == "playback_session_invalid" || (status == 401 && !expiresAccountSession(status, path, challenge) &&
        (path.startsWith("/api/playback/") || path.matches(Regex("/api/channels/\\d+/tune")))) ->
        "This playback session has ended. Choose the channel again."
    else -> null
}
