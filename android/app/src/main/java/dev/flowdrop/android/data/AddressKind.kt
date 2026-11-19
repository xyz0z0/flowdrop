package dev.flowdrop.android.data

/**
 * Coarse network hint for a Hub address, using the same labels the Hub itself
 * publishes in its `endpoints` list (`lan` / `tailscale` / `other`).
 *
 * Only used to annotate an address in the UI: a user who sees
 * `http://100.100.100.63:8080` has no way to tell that this is the Tailscale
 * path and the office LAN is what failed. It never decides which address is
 * probed, and it must never be treated as proof about the peer.
 */
fun routeKindOf(raw: String): String {
    val host = HubEndpoint.parse(raw)?.baseUrl?.host?.lowercase() ?: return RouteCandidate.KIND_OTHER
    if (host.endsWith(".ts.net")) return RouteCandidate.KIND_TAILSCALE
    val octets = host.split('.')
    if (octets.size != 4) return RouteCandidate.KIND_OTHER
    val parts = IntArray(4) { index -> octets[index].toIntOrNull() ?: return RouteCandidate.KIND_OTHER }
    if (parts.any { it !in 0..255 }) return RouteCandidate.KIND_OTHER
    return when {
        parts[0] == 10 -> RouteCandidate.KIND_LAN
        parts[0] == 172 && parts[1] in 16..31 -> RouteCandidate.KIND_LAN
        parts[0] == 192 && parts[1] == 168 -> RouteCandidate.KIND_LAN
        // 100.64.0.0/10: Tailscale's CGNAT range. A plain CGNAT listener is
        // classified the same way, which is only a label, never a decision.
        parts[0] == 100 && parts[1] in 64..127 -> RouteCandidate.KIND_TAILSCALE
        else -> RouteCandidate.KIND_OTHER
    }
}
