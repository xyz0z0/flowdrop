package dev.flowdrop.android.discovery

/**
 * Frozen FlowDrop LAN discovery contract (kept free of Android types so the
 * parsing and filtering rules can be unit tested on the JVM).
 *
 *   Service type : `_flowdrop._tcp` in the `local.` domain
 *   Instance     : `FlowDrop-<nickname>`
 *   SRV          : the Hub's real HTTP port
 *   TXT          : `v=1`, `scheme=http`, `path=/`
 *   Addresses    : explicit RFC1918 IPv4 only
 *
 * Nothing secret is ever advertised (no password, cookie, token or path).
 * A discovery result is only an address hint: it is not proof of identity and
 * must never be used to skip the user's password login.
 */
object DiscoveryProtocol {

    /** Service type with the trailing root label, as NsdManager expects it. */
    const val SERVICE_TYPE = "_flowdrop._tcp."

    /** Bare service type used for tolerant comparisons. */
    const val SERVICE_TYPE_BARE = "_flowdrop._tcp"

    const val INSTANCE_PREFIX = "FlowDrop-"

    const val TXT_VERSION = "v"
    const val TXT_SCHEME = "scheme"
    const val TXT_PATH = "path"

    const val REQUIRED_VERSION = "1"
    const val REQUIRED_SCHEME = "http"
    const val REQUIRED_PATH = "/"

    /**
     * Parses one resolved mDNS service into a usable Hub address.
     *
     * Returns null for anything that is not an explicitly published FlowDrop
     * Hub: wrong service type or instance name, missing/incompatible TXT,
     * invalid port, or a host that is not a private LAN IPv4 literal.
     */
    fun parse(raw: RawService): DiscoveredHub? {
        if (!matchesServiceType(raw.serviceType)) return null
        if (!raw.serviceName.startsWith(INSTANCE_PREFIX)) return null

        val displayName = raw.serviceName.removePrefix(INSTANCE_PREFIX).trim()
        if (displayName.isEmpty()) return null

        // DNS-SD TXT keys are case-insensitive; values are case-sensitive.
        val txt = raw.txt.entries.associate { (key, value) -> key.trim().lowercase() to value.trim() }
        if (txt[TXT_VERSION] != REQUIRED_VERSION) return null
        if (txt[TXT_SCHEME] != REQUIRED_SCHEME) return null
        if (txt[TXT_PATH] != REQUIRED_PATH) return null

        if (raw.port !in 1..65535) return null

        // Multiple A records are possible on API 34+; use the first address
        // that is an explicit RFC1918 IPv4. Never guess from hostnames.
        val host = raw.hostAddresses.firstOrNull { isRfc1918Ipv4(it) } ?: return null

        return DiscoveredHub(
            serviceName = raw.serviceName,
            displayName = displayName,
            host = host,
            port = raw.port,
        )
    }

    /** Accepts `_flowdrop._tcp` with or without the trailing dot. */
    fun matchesServiceType(serviceType: String): Boolean {
        val normalized = serviceType.trim().removeSuffix(".").lowercase()
        return normalized == SERVICE_TYPE_BARE
    }

    /**
     * Strict dotted-quad parser plus an RFC1918 range check.
     *
     * Rejected on purpose: IPv6 (including IPv4-mapped), loopback, link-local,
     * CGNAT/Tailscale (100.64.0.0/10), multicast, 0.0.0.0 and anything public.
     * Leading zeros are rejected because they are parsed as octal by some
     * resolvers, which would make the printed address ambiguous.
     */
    fun isRfc1918Ipv4(address: String): Boolean {
        val value = parseIpv4(address.trim()) ?: return false
        return when {
            value and 0xFF000000L == 0x0A000000L -> true // 10.0.0.0/8
            value and 0xFFF00000L == 0xAC100000L -> true // 172.16.0.0/12
            value and 0xFFFF0000L == 0xC0A80000L -> true // 192.168.0.0/16
            else -> false
        }
    }

    private fun parseIpv4(address: String): Long? {
        val parts = address.split('.')
        if (parts.size != 4) return null
        var value = 0L
        for (part in parts) {
            if (part.isEmpty() || part.length > 3) return null
            if (part.any { it !in '0'..'9' }) return null
            if (part.length > 1 && part[0] == '0') return null
            val octet = part.toInt()
            if (octet > 255) return null
            value = (value shl 8) or octet.toLong()
        }
        return value
    }
}

/**
 * Values extracted from one resolved [android.net.nsd.NsdServiceInfo].
 * Kept as plain data so validation does not depend on the Android framework.
 */
data class RawService(
    val serviceName: String,
    val serviceType: String = DiscoveryProtocol.SERVICE_TYPE,
    val hostAddresses: List<String> = emptyList(),
    val port: Int = 0,
    val txt: Map<String, String> = emptyMap(),
)

/** A validated nearby Hub; [baseUrl] is what the login form may be filled with. */
data class DiscoveredHub(
    val serviceName: String,
    val displayName: String,
    val host: String,
    val port: Int,
) {
    /** Always HTTP: TLS Hubs never advertise themselves. */
    val baseUrl: String get() = "http://$host:$port"
}

/**
 * Deduplicating list of nearby Hubs.
 *
 * One mDNS instance maps to one entry. Re-resolving the same instance updates
 * its address in place (not a second row), and an address can only belong to
 * one instance so a stale name with a recycled address disappears.
 */
class HubDiscoveryRegistry {

    private val byServiceName = LinkedHashMap<String, DiscoveredHub>()

    /** Returns the accepted Hub, or null when the record is incompatible. */
    fun found(raw: RawService): DiscoveredHub? {
        val hub = DiscoveryProtocol.parse(raw) ?: return null
        byServiceName[hub.serviceName] = hub

        // The same ip:port cannot be two different Hubs. Drop the older name
        // so the list never shows duplicate addresses.
        val staleNames = byServiceName.values
            .filter { it.serviceName != hub.serviceName && it.baseUrl == hub.baseUrl }
            .map { it.serviceName }
        staleNames.forEach { byServiceName.remove(it) }
        return hub
    }

    fun lost(serviceName: String) {
        byServiceName.remove(serviceName)
    }

    fun clear() {
        byServiceName.clear()
    }

    fun hubs(): List<DiscoveredHub> = byServiceName.values.sortedWith(
        compareBy({ it.displayName.lowercase() }, { it.baseUrl }),
    )
}
