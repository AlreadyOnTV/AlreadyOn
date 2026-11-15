package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

// Failed tuning and its best-effort release must never erase the account session.
class ServerFailurePolicyTest {
    @Test fun playbackFailureAndCleanupPreserveAccountSession() {
        for (path in listOf("/api/channels/107/tune", "/api/playback/sessions", "/api/playback/status")) {
            for (status in listOf(401, 409, 422, 503)) assertFalse(expiresAccountSession(status, path, null))
        }
        assertFalse(expiresAccountSession(401, "/api/profiles/1/verify-pin", "Bearer"))
        assertFalse(expiresAccountSession(401, "/api/auth/tv-login", "Bearer"))
        assertFalse(expiresAccountSession(401, "/api/auth/login", "Bearer"))
        assertTrue(expiresAccountSession(401, "/api/profiles", "Bearer"))
        assertTrue(expiresAccountSession(401, "/api/playback/sessions", "Bearer"))
        assertFalse(expiresAccountSession(422, "/api/channels/107/tune", "Bearer"))
    }

    @Test fun capacityFailureDoesNotBlameClientCodecsOrPin() {
        val message = playbackFailureMessage(422, "/api/channels/107/tune", "server_capacity_unsustainable", null)
        assertTrue(message!!.contains("server"))
        assertTrue(message.contains("lower quality"))
        assertFalse(message.contains("supported formats"))
        assertTrue(playbackFailureMessage(422, "/api/channels/107/tune", "server_capacity_measurement_unavailable", null)!!.contains("measurement"))
        assertTrue(playbackFailureMessage(401, "/api/playback/sessions", "", null)!!.contains("playback session"))
        assertNull(playbackFailureMessage(401, "/api/profiles", "", "Bearer"))
    }
}
