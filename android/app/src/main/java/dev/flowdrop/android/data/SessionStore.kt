package dev.flowdrop.android.data

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

@Serializable
data class StoredCookie(
    val name: String,
    val value: String,
    val host: String,
    val path: String = "/",
    val secure: Boolean = false,
    val httpOnly: Boolean = false,
    val expiresAt: Long = 0,
    val sameSite: String = "",
)

/**
 * Everything needed to resume a session after process death.
 *
 * Deliberately contains **no password** and never the pairing code. The Hub URL
 * is kept so the settings screen can show where the paired Hub was last
 * reached; the cookies are the actual request credential and are only ever
 * sent back to the same origin.
 *
 * Pairing fields ([hubId], [deviceId], [deviceToken]) are the durable identity
 * of one Hub and one device. [deviceToken] is only used to restore cookies and
 * CSRF at a *verified* origin; it is never attached to ordinary requests.
 * [candidates] are the Hub's saved addresses (LAN/Tailscale/other) used by
 * [HubRouteSelector]; they are also the allow-list for [SessionStore.switchToPaired].
 */
@Serializable
data class StoredSession(
    val baseUrl: String,
    val origin: String,
    val csrfToken: String = "",
    val cookies: List<StoredCookie> = emptyList(),
    val expiresAt: Long = 0,
    val hubId: String = "",
    val deviceId: String = "",
    val deviceToken: String = "",
    val candidates: List<HubEndpointInfo> = emptyList(),
)

/**
 * Persists the session blob through [SecureStorage] and enforces the "one Hub
 * at a time" isolation rule: pointing the app at a different address wipes the
 * previous cookies instead of handing them to a new server.
 */
class SessionStore(
    private val storage: SecureStorage,
    private val json: Json = Json { ignoreUnknownKeys = true; encodeDefaults = true },
) {
    @Volatile
    private var cached: StoredSession? = null

    @Synchronized
    fun load(): StoredSession? {
        cached?.let { return it }
        val raw = storage.read() ?: return null
        val parsed = try {
            json.decodeFromString<StoredSession>(raw)
        } catch (_: Exception) {
            storage.clear()
            return null
        }
        cached = parsed
        return parsed
    }

    @Synchronized
    fun save(session: StoredSession) {
        cached = session
        storage.write(json.encodeToString(StoredSession.serializer(), session))
    }

    @Synchronized
    fun clear() {
        cached = null
        storage.clear()
    }

    /** Returns the stored session only if it belongs to [origin]. */
    fun loadFor(origin: String): StoredSession? = load()?.takeIf { it.origin == origin }

    /**
     * Switches the active Hub. Any session for a different origin is destroyed
     * before the new address is used, so cookies can never leak across servers.
     *
     * Only use this for a *manually entered* address: a paired Hub must go
     * through [switchToPaired], which keeps the device pairing but still drops
     * cookies when the origin changes.
     */
    @Synchronized
    fun switchTo(endpoint: HubEndpoint) {
        val existing = load()
        if (existing != null && existing.origin != endpoint.origin) {
            // Different server: drop everything, then start a fresh empty record.
            clear()
            save(StoredSession(baseUrl = endpoint.origin, origin = endpoint.origin))
            return
        }
        val base = existing?.copy(baseUrl = endpoint.origin)
            ?: StoredSession(baseUrl = endpoint.origin, origin = endpoint.origin)
        save(base)
    }

    /**
     * Activates another saved address of the *same* paired Hub.
     *
     * Allowed only when the stored record belongs to [expectedHubId] and
     * [endpoint]'s origin is one of the saved [StoredSession.candidates]. Then
     * old cookies and CSRF are discarded (they are scoped to the previous
     * origin and must not travel to the new one) while the pairing itself —
     * Hub ID, device ID and device token — is kept.
     *
     * Returns `false` and changes nothing when the check fails. Nothing is
     * cleared in that case either: a caller that got the Hub ID wrong must not
     * be able to silently drop the user's pairing, and no credential is
     * propagated because [PersistentCookieJar] only serves the stored origin.
     */
    @Synchronized
    fun switchToPaired(endpoint: HubEndpoint, expectedHubId: String): Boolean {
        val existing = load() ?: return false
        if (existing.hubId.isEmpty() || expectedHubId.isEmpty()) return false
        if (existing.hubId != expectedHubId) return false
        val savedOrigin = existing.candidates.any { HubEndpoint.parse(it.url)?.origin == endpoint.origin }
        if (!savedOrigin) return false
        save(
            existing.copy(
                baseUrl = endpoint.origin,
                origin = endpoint.origin,
                csrfToken = "",
                cookies = emptyList(),
            ),
        )
        return true
    }

    /**
     * Forgets cookies and CSRF for [origin] but keeps the Hub/device pairing,
     * so a verified restore can rebuild the cookie session from the device token.
     */
    @Synchronized
    fun clearCookies(origin: String) {
        val existing = load() ?: return
        if (existing.origin != origin) return
        save(existing.copy(csrfToken = "", cookies = emptyList()))
    }
}

/**
 * Merges saved addresses with addresses reported by the Hub, normalising each
 * to its canonical origin and keeping the first (stored) kind for duplicates.
 * [extra] entries always join the result, so the address the user paired at is
 * remembered even when the Hub omits it.
 */
internal fun mergeCandidates(
    existing: List<HubEndpointInfo>,
    endpoints: List<HubEndpointInfo> = emptyList(),
    extra: List<HubEndpointInfo> = emptyList(),
): List<HubEndpointInfo> {
    val seen = LinkedHashMap<String, HubEndpointInfo>()
    for (candidate in existing + endpoints + extra) {
        val endpoint = HubEndpoint.parse(candidate.url) ?: continue
        seen.putIfAbsent(endpoint.origin, HubEndpointInfo(url = endpoint.origin, kind = normaliseKind(candidate.kind)))
    }
    return seen.values.toList()
}

internal fun normaliseKind(kind: String): String = when (kind.trim().lowercase()) {
    RouteCandidate.KIND_LAN -> RouteCandidate.KIND_LAN
    RouteCandidate.KIND_TAILSCALE -> RouteCandidate.KIND_TAILSCALE
    else -> RouteCandidate.KIND_OTHER
}
