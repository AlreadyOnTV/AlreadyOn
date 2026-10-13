// Discovery stays within the local subnet and probes nearby addresses first.
package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class SubnetCandidatesTest {
    private fun ip(a: Int, b: Int, c: Int, d: Int) = byteArrayOf(a.toByte(), b.toByte(), c.toByte(), d.toByte())
    @Test fun homeSubnetExcludesOwnNetworkAndBroadcast() {
        val targets = subnetCandidates(ip(192, 168, 60, 25), 24).toList()
        assertEquals(253, targets.size)
        assertTrue(targets.contains("192.168.60.5"))
        assertFalse(targets.contains("192.168.60.25"))
        assertFalse(targets.contains("192.168.60.0"))
        assertFalse(targets.contains("192.168.60.255"))
        assertTrue(targets.all { it.startsWith("192.168.60.") })
    }
    @Test fun broadSubnetPrioritizesNearbyWithoutDuplicates() {
        val targets = subnetCandidates(ip(192, 168, 60, 25), 16).toList()
        assertEquals(65533, targets.size)
        assertEquals(targets.size, targets.toSet().size)
        assertTrue(targets.take(255).all { it.startsWith("192.168.60.") })
        assertTrue(targets.contains("192.168.1.5"))
    }
    @Test fun narrowSubnetsDoNotProbeOutsideTheirBoundary() {
        assertEquals(listOf("192.168.60.26"), subnetCandidates(ip(192, 168, 60, 25), 30).toList())
        assertEquals(listOf("192.168.60.24"), subnetCandidates(ip(192, 168, 60, 25), 31).toList())
        assertTrue(subnetCandidates(ip(192, 168, 60, 25), 32).none())
    }
}
