package dev.flowdrop.android.data

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * The whole encrypted blob: one [StoredSession] per paired Hub plus which Hub
 * was in use last.
 *
 * Keying by `hub_id` is what keeps the Hubs apart. Every Hub gets its own
 * [SessionStore] view over one entry in [hubs], so cookies, CSRF tokens, device
 * tokens and candidate addresses of one Hub are not even addressable from
 * another Hub's store handle.
 */
@Serializable
data class StoredHubs(
    val version: Int = 1,
    val activeHubId: String = "",
    val hubs: List<StoredSession> = emptyList(),
)

/**
 * Multi-Hub registry of paired sessions, stored as one encrypted blob.
 *
 * Rules:
 * - a record is identified by [StoredSession.hubId]; saving a record for an
 *   existing Hub replaces only that Hub's entry;
 * - [store] hands out a [SessionStore] whose `read`/`write`/`clear` touch one
 *   entry, so the cookie jar and the API client of a Hub can only ever read or
 *   write that Hub's credentials;
 * - removing a Hub (logout, revocation) leaves every other Hub untouched;
 * - [activeHubId] only records which Hub the user used last; clearing it never
 *   drops a pairing.
 *
 * A store handle also carries the *epoch* of the record it was opened on. When a
 * Hub is removed or re-paired, every older handle of that Hub becomes inert:
 * a late `Set-Cookie` or a late session write from the previous pairing can
 * neither resurrect the removed record nor overwrite the new one. Handles of
 * other Hubs are unaffected, and their epochs do not change.
 *
 * Records whose `hubId` is blank cannot be keyed by Hub and are ignored: the
 * Hub ID is the identity the whole isolation model is built on.
 */
class HubVault(
    private val storage: SecureStorage,
    private val json: Json = Json { ignoreUnknownKeys = true; encodeDefaults = true },
) {
    private val stores = HashMap<String, SessionStore>()
    private val epochs = HashMap<String, Long>()

    @Synchronized
    fun all(): List<StoredSession> = state().hubs

    @Synchronized
    fun load(hubId: String): StoredSession? =
        state().hubs.firstOrNull { it.hubId == hubId }

    /**
     * Stores a record that was produced outside a Hub's own store (a completed
     * pairing). Replacing a record opens a new epoch: clients of the previous
     * pairing of the same Hub ID can no longer write into this one.
     */
    @Synchronized
    fun save(session: StoredSession) {
        if (session.hubId.isBlank()) return
        upsert(session)
        invalidate(session.hubId)
    }

    @Synchronized
    fun remove(hubId: String) {
        removeEntry(hubId)
        invalidate(hubId)
    }

    @Synchronized
    fun activeHubId(): String? = state().activeHubId.takeIf { it.isNotBlank() }

    @Synchronized
    fun setActive(hubId: String?) {
        state().let { current -> write(current.copy(activeHubId = hubId.orEmpty())) }
    }

    /**
     * The only way to reach one Hub's credentials. The returned store is cached
     * so its in-memory copy and the blob stay one object per Hub.
     */
    @Synchronized
    fun store(hubId: String): SessionStore {
        require(hubId.isNotBlank()) { "hub id is required" }
        return stores.getOrPut(hubId) {
            SessionStore(HubEntryStorage(this, hubId, epochOf(hubId)))
        }
    }

    // ---------- blob access ----------

    /** Whether [epoch] is still the current record of [hubId]. */
    @Synchronized
    internal fun isCurrentEntry(hubId: String, epoch: Long): Boolean =
        epoch == epochOf(hubId)

    @Synchronized
    internal fun readEntry(hubId: String, epoch: Long): String? {
        if (epoch != epochOf(hubId)) return null
        return load(hubId)?.let { entry -> json.encodeToString(StoredSession.serializer(), entry) }
    }

    /**
     * Writes one Hub's entry from a store handle.
     *
     * A write from a superseded handle (removed or re-paired Hub) is dropped
     * instead of being written into the current record. A record that lost its
     * Hub ID is dropped as well: an unkeyed leftover could be neither selected
     * nor removed.
     */
    @Synchronized
    internal fun writeEntry(hubId: String, epoch: Long, value: String) {
        if (epoch != epochOf(hubId)) return
        val decoded = runCatching { json.decodeFromString(StoredSession.serializer(), value) }
            .getOrNull()
        if (decoded == null || decoded.hubId.isBlank() || decoded.hubId != hubId) {
            removeEntry(hubId)
            invalidate(hubId)
            return
        }
        upsert(decoded)
    }

    @Synchronized
    internal fun clearEntry(hubId: String, epoch: Long) {
        if (epoch != epochOf(hubId)) return
        removeEntry(hubId)
        invalidate(hubId)
    }

    // ---------- internals ----------

    private fun epochOf(hubId: String): Long = epochs[hubId] ?: 0L

    /**
     * Makes every handle opened so far for [hubId] inert.
     *
     * Deliberately does *not* call into the [SessionStore] it is dropping: a
     * store holds its own monitor while it calls into the vault (load/save), so
     * taking a store lock here would create an AB/BA deadlock. [SessionStore]
     * checks [SecureStorage.isCurrent] instead, which makes a stale handle stop
     * serving or writing its own cache without any cross-lock call.
     */
    private fun invalidate(hubId: String) {
        epochs[hubId] = epochOf(hubId) + 1
        stores.remove(hubId)
    }

    @Synchronized
    private fun upsert(session: StoredSession) {
        val current = state()
        val hubs = current.hubs.filterNot { it.hubId == session.hubId } + session
        write(current.copy(hubs = hubs))
    }

    @Synchronized
    private fun removeEntry(hubId: String) {
        val current = state()
        val hubs = current.hubs.filterNot { it.hubId == hubId }
        if (hubs.size == current.hubs.size) return
        write(
            current.copy(
                hubs = hubs,
                activeHubId = if (current.activeHubId == hubId) "" else current.activeHubId,
            ),
        )
    }

    @Synchronized
    private fun state(): StoredHubs {
        val raw = storage.read() ?: return StoredHubs()
        return try {
            json.decodeFromString(StoredHubs.serializer(), raw)
        } catch (_: Exception) {
            // Corrupted or unreadable blob: start empty instead of handing a
            // half-parsed record to a client.
            storage.clear()
            StoredHubs()
        }
    }

    @Synchronized
    private fun write(next: StoredHubs) {
        storage.write(json.encodeToString(StoredHubs.serializer(), next))
    }
}

/**
 * [SecureStorage] view over a single Hub entry of a [HubVault].
 *
 * [SessionStore] clears its storage when it cannot decode a record; scoping
 * that to one entry is what keeps a damaged record from wiping every other
 * paired computer. The captured [epoch] ties the handle to the record it was
 * opened on.
 */
private class HubEntryStorage(
    private val vault: HubVault,
    private val hubId: String,
    private val epoch: Long,
) : SecureStorage {
    override fun read(): String? = vault.readEntry(hubId, epoch)

    override fun write(value: String) = vault.writeEntry(hubId, epoch, value)

    override fun clear() = vault.clearEntry(hubId, epoch)

    override fun isCurrent(): Boolean = vault.isCurrentEntry(hubId, epoch)
}
