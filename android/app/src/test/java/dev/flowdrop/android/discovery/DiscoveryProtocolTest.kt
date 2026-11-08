package dev.flowdrop.android.discovery

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DiscoveryProtocolTest {

    private fun raw(
        name: String = "FlowDrop-客厅",
        type: String = DiscoveryProtocol.SERVICE_TYPE,
        addresses: List<String> = listOf("192.168.1.50"),
        port: Int = 8080,
        version: String = "1",
        scheme: String = "http",
        path: String = "/",
    ) = RawService(
        serviceName = name,
        serviceType = type,
        hostAddresses = addresses,
        port = port,
        txt = mapOf("v" to version, "scheme" to scheme, "path" to path),
    )

    @Test
    fun `accepts the frozen protocol record`() {
        val hub = DiscoveryProtocol.parse(raw())!!
        assertEquals("FlowDrop-客厅", hub.serviceName)
        assertEquals("客厅", hub.displayName)
        assertEquals("192.168.1.50", hub.host)
        assertEquals(8080, hub.port)
        assertEquals("http://192.168.1.50:8080", hub.baseUrl)
    }

    @Test
    fun `tolerates a missing trailing dot and uppercase TXT keys`() {
        val hub = DiscoveryProtocol.parse(
            RawService(
                serviceName = "FlowDrop-Desk",
                serviceType = "_FLOWDROP._TCP",
                hostAddresses = listOf("10.1.2.3"),
                port = 18080,
                txt = mapOf("V" to "1", "Scheme" to "http", "PATH" to "/"),
            ),
        )!!
        assertEquals("Desk", hub.displayName)
        assertEquals("http://10.1.2.3:18080", hub.baseUrl)
    }

    @Test
    fun `rejects unrelated or malformed instance names`() {
        assertNull(DiscoveryProtocol.parse(raw(name = "Printer")))
        assertNull(DiscoveryProtocol.parse(raw(name = "flowdrop-客厅")))
        assertNull(DiscoveryProtocol.parse(raw(name = "FlowDrop")))
        assertNull(DiscoveryProtocol.parse(raw(name = "FlowDrop-")))
        assertNull(DiscoveryProtocol.parse(raw(name = "FlowDrop-   ")))
    }

    @Test
    fun `rejects another service type`() {
        assertNull(DiscoveryProtocol.parse(raw(type = "_http._tcp.")))
        assertNull(DiscoveryProtocol.parse(raw(type = "_flowdrop._udp.")))
    }

    @Test
    fun `rejects incompatible TXT records`() {
        assertNull(DiscoveryProtocol.parse(raw(version = "2")))
        assertNull(DiscoveryProtocol.parse(raw(version = "")))
        assertNull(DiscoveryProtocol.parse(raw(scheme = "https")))
        assertNull(DiscoveryProtocol.parse(raw(path = "/hub")))
        assertNull(
            DiscoveryProtocol.parse(
                RawService(
                    serviceName = "FlowDrop-X",
                    hostAddresses = listOf("192.168.1.2"),
                    port = 8080,
                    txt = mapOf("v" to "1", "scheme" to "http"),
                ),
            ),
        )
    }

    @Test
    fun `rejects invalid ports`() {
        assertNull(DiscoveryProtocol.parse(raw(port = 0)))
        assertNull(DiscoveryProtocol.parse(raw(port = -1)))
        assertNull(DiscoveryProtocol.parse(raw(port = 65536)))
        assertEquals(65535, DiscoveryProtocol.parse(raw(port = 65535))!!.port)
        assertEquals(1, DiscoveryProtocol.parse(raw(port = 1))!!.port)
    }

    @Test
    fun `accepts RFC1918 range boundaries`() {
        val addresses = listOf(
            "10.0.0.0",
            "10.255.255.255",
            "172.16.0.0",
            "172.31.255.255",
            "192.168.0.0",
            "192.168.255.255",
        )
        addresses.forEach { address ->
            assertTrue("$address should be private LAN", DiscoveryProtocol.isRfc1918Ipv4(address))
            assertEquals(address, DiscoveryProtocol.parse(raw(addresses = listOf(address)))!!.host)
        }
    }

    @Test
    fun `rejects public loopback link-local CGNAT and IPv6 addresses`() {
        val rejected = listOf(
            "8.8.8.8",
            "1.1.1.1",
            "127.0.0.1",
            "169.254.1.1",
            "100.64.0.1",
            "100.125.168.63", // Tailscale CGNAT, must never be advertised
            "172.15.255.255",
            "172.32.0.0",
            "192.169.0.1",
            "0.0.0.0",
            "224.0.0.251",
            "255.255.255.255",
            "::1",
            "fe80::1",
            "::ffff:192.168.1.1",
            "192.168.1",
            "192.168.1.256",
            "192.168.1.01",
            "192.168.1.1.1",
            "not-an-ip",
            "",
        )
        rejected.forEach { address ->
            assertFalse("$address must be rejected", DiscoveryProtocol.isRfc1918Ipv4(address))
            assertNull("$address must not produce a Hub", DiscoveryProtocol.parse(raw(addresses = listOf(address))))
        }
    }

    @Test
    fun `picks the first explicit LAN IPv4 from multiple addresses`() {
        val hub = DiscoveryProtocol.parse(
            raw(
                addresses = listOf(
                    "fe80::1",
                    "8.8.8.8",
                    "100.64.0.2",
                    "172.20.10.4",
                    "192.168.1.9",
                ),
            ),
        )!!
        assertEquals("172.20.10.4", hub.host)
    }

    @Test
    fun `no private IPv4 means no Hub`() {
        assertNull(DiscoveryProtocol.parse(raw(addresses = emptyList())))
        assertNull(DiscoveryProtocol.parse(raw(addresses = listOf("fe80::1", "203.0.113.9"))))
    }

    @Test
    fun `registry dedupes an instance and updates its address`() {
        val registry = HubDiscoveryRegistry()
        registry.found(raw(addresses = listOf("192.168.1.50")))
        registry.found(raw(addresses = listOf("192.168.1.51")))
        val hubs = registry.hubs()
        assertEquals(1, hubs.size)
        assertEquals("192.168.1.51", hubs.single().host)
    }

    @Test
    fun `registry keeps one row per address across renamed instances`() {
        val registry = HubDiscoveryRegistry()
        registry.found(raw(name = "FlowDrop-Old", addresses = listOf("192.168.1.50")))
        registry.found(raw(name = "FlowDrop-New", addresses = listOf("192.168.1.50")))
        val hubs = registry.hubs()
        assertEquals(1, hubs.size)
        assertEquals("FlowDrop-New", hubs.single().serviceName)
    }

    @Test
    fun `registry removes a lost service and ignores invalid records`() {
        val registry = HubDiscoveryRegistry()
        registry.found(raw())
        assertNull(registry.found(raw(name = "FlowDrop-Bad", version = "9")))
        assertEquals(1, registry.hubs().size)
        registry.lost("FlowDrop-客厅")
        assertTrue(registry.hubs().isEmpty())
        registry.lost("FlowDrop-客厅")
        assertTrue(registry.hubs().isEmpty())
    }

    @Test
    fun `registry sorts by display name and clears`() {
        val registry = HubDiscoveryRegistry()
        registry.found(raw(name = "FlowDrop-Beta", addresses = listOf("192.168.1.20"), port = 8080))
        registry.found(raw(name = "FlowDrop-Alpha", addresses = listOf("192.168.1.10"), port = 8080))
        registry.found(raw(name = "FlowDrop-客厅", addresses = listOf("192.168.1.30"), port = 8080))
        assertEquals(listOf("Alpha", "Beta", "客厅"), registry.hubs().map { it.displayName })
        registry.clear()
        assertTrue(registry.hubs().isEmpty())
    }
}
