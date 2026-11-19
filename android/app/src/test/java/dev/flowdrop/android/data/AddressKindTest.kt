package dev.flowdrop.android.data

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The boot screen annotates an address with the network it belongs to, so a user
 * stuck on `100.x` understands which path is being tried. The label must follow
 * the Hub's own classification and must never turn a guess into a decision, so
 * only the well-known ranges are named.
 */
class AddressKindTest {

    @Test
    fun `private ipv4 ranges are lan`() {
        assertEquals(RouteCandidate.KIND_LAN, routeKindOf("http://192.168.1.184:8080"))
        assertEquals(RouteCandidate.KIND_LAN, routeKindOf("http://10.0.0.5:8080"))
        assertEquals(RouteCandidate.KIND_LAN, routeKindOf("http://172.16.0.1:8080"))
        assertEquals(RouteCandidate.KIND_LAN, routeKindOf("http://172.31.255.254:8080"))
        assertEquals(RouteCandidate.KIND_LAN, routeKindOf("192.168.1.50:8080"))
    }

    @Test
    fun `tailscale cgnat range and magic dns names are labelled tailscale`() {
        assertEquals(RouteCandidate.KIND_TAILSCALE, routeKindOf("http://100.100.100.63:8080"))
        assertEquals(RouteCandidate.KIND_TAILSCALE, routeKindOf("http://100.64.0.1:8080"))
        assertEquals(RouteCandidate.KIND_TAILSCALE, routeKindOf("http://100.127.255.254:8080"))
        assertEquals(
            RouteCandidate.KIND_TAILSCALE,
            routeKindOf("https://example-host.tailnet.ts.net"),
        )
    }

    @Test
    fun `everything else stays other`() {
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf("http://172.32.0.1:8080"))
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf("http://100.128.0.1:8080"))
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf("http://100.63.255.254:8080"))
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf("https://hub.example.com"))
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf("http://[::1]:8080"))
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf("not a url at all"))
        assertEquals(RouteCandidate.KIND_OTHER, routeKindOf(""))
    }
}
