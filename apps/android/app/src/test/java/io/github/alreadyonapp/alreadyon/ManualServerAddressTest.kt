// Address suggestions must never override an explicit port or become a hidden fallback.
package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class ManualServerAddressTest {
    @Test fun ipPrefillsButHostnameDoesNot() {
        for (host in listOf("192.168.1.5", "2001:db8::1", "[2001:db8::1]")) {
            val address = ManualServerAddress().withHost(host)
            assertEquals("19900", address.port)
            assertTrue(address.origin().endsWith(":19900"))
            assertFalse(address.withPort("").origin().contains("19900"))
        }
        assertEquals("", ManualServerAddress().withHost("server.local").port)
        assertEquals("https://server.local", ManualServerAddress().withHost("server.local").origin())
    }
    @Test fun editsAndClearedPortsSurviveHostChanges() {
        val ip = ManualServerAddress().withHost("192.168.1.5")
        assertEquals("", ip.withHost("server.local").port)
        assertEquals("8443", ip.withPort("8443").withHost("192.168.1.6").port)
        assertEquals("", ip.withPort("").withHost("192.168.1.6").port)
        assertEquals("https://192.168.1.5:8443", ip.withPort("8443").origin())
        assertEquals("https://[2001:db8::1]", ManualServerAddress("2001:db8::1").origin())
    }
    @Test fun invalidAddressesAndPortsFailBeforeConnection() {
        for (host in listOf("", "https://server", "server/path", "server:8443", "999.1.1.1", "user@server")) {
            assertTrue(runCatching { ManualServerAddress(host).origin() }.isFailure)
        }
        for (port in listOf("0", "65536", "-1", "abc", "999999999999"))
            assertTrue(runCatching { ManualServerAddress("server.local", port).origin() }.isFailure)
    }
}
