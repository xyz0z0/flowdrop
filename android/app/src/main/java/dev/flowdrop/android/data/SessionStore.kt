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
 * Deliberately contains **no password**. The username and Hub URL are kept so
 * the login form can be prefilled after the session expires; the cookies are
 * the actual credential and are only ever sent back to the same origin.
 */
@Serializable
data class StoredSession(
    val baseUrl: String,
    val origin: String,
    val username: String = "",
    val csrfToken: String = "",
    val cookies: List<StoredCookie> = emptyList(),
    val expiresAt: Long = 0,
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
}
