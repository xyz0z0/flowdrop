package dev.flowdrop.android.data

import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Isolation of several paired computers in one encrypted vault.
 *
 * These are the regressions that matter for "remember more than one computer":
 * a store handle of one Hub must never read or write another Hub's credentials,
 * removing or re-pairing a Hub must make its old handles inert, and a cookie jar
 * must stay scoped to its own origin.
 */
class HubVaultTest {

    private fun endpoint(raw: String) = HubEndpoint.parse(raw)!!

    private fun record(
        hubId: String,
        origin: String,
        token: String,
        name: String = hubId,
        cookies: List<StoredCookie> = emptyList(),
    ) = StoredSession(
        baseUrl = origin,
        origin = origin,
        csrfToken = "csrf-$hubId",
        cookies = cookies,
        hubId = hubId,
        hubName = name,
        deviceId = "device-$hubId",
        deviceToken = token,
        candidates = listOf(HubEndpointInfo(origin, RouteCandidate.KIND_LAN)),
    )

    @Test
    fun `each hub keeps its own record`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        val b = endpoint("http://10.0.0.7:8080")
        vault.save(record("hub-a", a.origin, "token-a"))
        vault.save(record("hub-b", b.origin, "token-b"))

        assertEquals(setOf("hub-a", "hub-b"), vault.all().map { it.hubId }.toSet())
        assertEquals("token-a", vault.load("hub-a")?.deviceToken)
        assertEquals("token-b", vault.load("hub-b")?.deviceToken)

        // Writing through one Hub's store handle must not touch the other one.
        vault.store("hub-a").save(record("hub-a", a.origin, "token-a2"))
        assertEquals("token-a2", vault.load("hub-a")?.deviceToken)
        assertEquals("token-b", vault.load("hub-b")?.deviceToken)
    }

    @Test
    fun `a store handle cannot read or clear another hub`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        val b = endpoint("http://10.0.0.7:8080")
        vault.save(record("hub-a", a.origin, "token-a"))
        vault.save(record("hub-b", b.origin, "token-b"))

        val storeA = vault.store("hub-a")
        assertEquals("hub-a", storeA.load()?.hubId)
        assertEquals("token-a", storeA.load()?.deviceToken)

        // SessionStore.clear() (the logout path of the old code) is scoped to
        // the entry its handle was opened on.
        storeA.clear()
        assertNull(vault.load("hub-a"))
        assertEquals("token-b", vault.load("hub-b")?.deviceToken)
        assertEquals("hub-b", vault.store("hub-b").load()?.hubId)
    }

    @Test
    fun `removing one hub keeps the others and the active pointer`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        val b = endpoint("http://10.0.0.7:8080")
        vault.save(record("hub-a", a.origin, "token-a"))
        vault.save(record("hub-b", b.origin, "token-b"))
        vault.setActive("hub-b")
        assertEquals("hub-b", vault.activeHubId())

        vault.remove("hub-a")
        assertEquals(listOf("hub-b"), vault.all().map { it.hubId })
        assertEquals("hub-b", vault.activeHubId())

        vault.remove("hub-b")
        assertNull(vault.activeHubId())
        assertTrue(vault.all().isEmpty())
    }

    @Test
    fun `a stale handle cannot resurrect a removed or re-paired hub`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        vault.save(record("hub-a", a.origin, "old-token"))
        val stale = vault.store("hub-a")
        assertEquals("old-token", stale.load()?.deviceToken)

        // The device is revoked, then paired again: same Hub ID, new token.
        vault.remove("hub-a")
        vault.save(record("hub-a", a.origin, "new-token"))

        // A late write through the old handle (e.g. a Set-Cookie from a request
        // that was already in flight) must not reach the new record.
        stale.save(record("hub-a", a.origin, "stale-token"))
        assertEquals("new-token", vault.load("hub-a")?.deviceToken)
        assertEquals("new-token", vault.load("hub-a")?.deviceToken)
        // The old handle cannot even read the replacement.
        assertNull(stale.load()?.deviceToken)

        // A fresh handle of the same Hub does work.
        assertEquals("new-token", vault.store("hub-a").load()?.deviceToken)
    }

    @Test
    fun `a cookie jar of one hub never returns another hub's cookies`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        val b = endpoint("http://10.0.0.7:8080")
        vault.save(record("hub-a", a.origin, "token-a"))
        vault.save(record("hub-b", b.origin, "token-b"))

        val jarA = PersistentCookieJar(vault.store("hub-a"))
        val jarB = PersistentCookieJar(vault.store("hub-b"))
        jarA.saveFromResponse(
            a.baseUrl,
            listOf(
                okhttp3.Cookie.Builder()
                    .name("flowdrop_session")
                    .value("secret-a")
                    .hostOnlyDomain("192.168.1.50")
                    .path("/")
                    .expiresAt(System.currentTimeMillis() + 60_000)
                    .build(),
            ),
        )

        assertEquals(1, jarA.loadForRequest(a.baseUrl).size)
        assertTrue("hub B must not see hub A's cookies", jarB.loadForRequest(a.baseUrl).isEmpty())
        assertEquals(0, jarB.snapshot(a.origin).size)
        assertEquals("token-b", vault.load("hub-b")?.deviceToken)
        assertEquals("token-a", vault.load("hub-a")?.deviceToken)

        // Hub B's own jar cannot write into hub A's record either.
        jarB.saveFromResponse(
            b.baseUrl,
            listOf(
                okhttp3.Cookie.Builder()
                    .name("flowdrop_session")
                    .value("secret-b")
                    .hostOnlyDomain("10.0.0.7")
                    .path("/")
                    .expiresAt(System.currentTimeMillis() + 60_000)
                    .build(),
            ),
        )
        assertEquals(
            "secret-a",
            vault.load("hub-a")?.cookies?.singleOrNull { it.name == "flowdrop_session" }?.value,
        )
        assertEquals(
            "secret-b",
            vault.load("hub-b")?.cookies?.singleOrNull { it.name == "flowdrop_session" }?.value,
        )
        assertTrue(jarA.loadForRequest(b.baseUrl).isEmpty())
    }

    @Test
    fun `cookies of a stale origin are not served at a new address`() {
        val vault = HubVault(InMemorySecureStorage())
        val lan = endpoint("http://192.168.1.50:8080")
        val tailscale = endpoint("http://100.64.0.5:8080")
        vault.save(
            record("hub-a", lan.origin, "token-a").copy(
                candidates = listOf(
                    HubEndpointInfo(lan.origin, RouteCandidate.KIND_LAN),
                    HubEndpointInfo(tailscale.origin, RouteCandidate.KIND_TAILSCALE),
                ),
            ),
        )
        val store = vault.store("hub-a")
        val jar = PersistentCookieJar(store)
        jar.saveFromResponse(
            lan.baseUrl,
            listOf(
                okhttp3.Cookie.Builder()
                    .name("flowdrop_session")
                    .value("secret-lan")
                    .hostOnlyDomain("192.168.1.50")
                    .path("/")
                    .expiresAt(System.currentTimeMillis() + 60_000)
                    .build(),
            ),
        )
        assertEquals(1, jar.loadForRequest(lan.baseUrl).size)

        // A second saved address of the same Hub must be verified and start
        // from a clean jar; the pairing is kept.
        assertTrue(store.switchToPaired(tailscale, "hub-a"))
        assertTrue(jar.loadForRequest(lan.baseUrl).isEmpty())
        assertEquals("token-a", vault.load("hub-a")?.deviceToken)
        assertTrue(store.load()?.cookies.orEmpty().isEmpty())
    }

    /**
     * Lock-order regression: the vault must never take a [SessionStore] monitor
     * while holding its own lock.
     *
     * `SessionStore.load/save` hold the store monitor and then call into the
     * vault; an invalidate path that called back into the store (as the first
     * implementation did) deadlocked AB/BA as soon as one thread loaded a record
     * while another removed or re-saved the same Hub. This test drives exactly
     * that pair of operations from two threads and requires both to finish.
     */
    @Test
    fun `vault and store locking never deadlock`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        vault.save(record("hub-a", a.origin, "token-a"))
        val store = vault.store("hub-a")
        val stop = java.util.concurrent.atomic.AtomicBoolean(false)
        val failure = java.util.concurrent.atomic.AtomicReference<Throwable?>(null)

        val reader = Thread {
            try {
                while (!stop.get()) store.load()
            } catch (t: Throwable) {
                failure.set(t)
            }
        }
        val writer = Thread {
            try {
                var index = 0
                while (!stop.get()) {
                    vault.save(record("hub-a", a.origin, "token-$index"))
                    index++
                    if (index % 32 == 0) vault.remove("hub-a")
                    if (index % 64 == 0) vault.save(record("hub-a", a.origin, "token-$index"))
                }
            } catch (t: Throwable) {
                failure.set(t)
            }
        }
        reader.start()
        writer.start()
        // Hammer both paths briefly, then let them stop. A deadlock keeps them
        // stuck past the (generous) join timeout instead of finishing.
        Thread.sleep(300)
        stop.set(true)
        reader.join(10_000)
        writer.join(10_000)
        val alive = reader.isAlive || writer.isAlive
        if (alive) {
            reader.interrupt()
            writer.interrupt()
        }
        assertTrue("vault/store locks must not deadlock", !alive)
        failure.get()?.let { throw AssertionError("lock stress failed", it) }
        // The vault is still usable afterwards.
        vault.save(record("hub-a", a.origin, "token-final"))
        assertEquals("token-final", vault.load("hub-a")?.deviceToken)
    }

    @Test
    fun `a rebuilt client keeps the stored upload limit`() {
        val vault = HubVault(InMemorySecureStorage())
        val a = endpoint("http://192.168.1.50:8080")
        vault.save(record("hub-a", a.origin, "token-a").copy(maxUploadBytes = 12_345))
        val store = vault.store("hub-a")
        val client = ApiClient(a, store, PersistentCookieJar(store))
        assertEquals(12_345L, client.maxUploadBytes)
    }

    @Test
    fun `records without a hub id are ignored`() {
        val vault = HubVault(InMemorySecureStorage())
        vault.save(record("", "http://hub.lan:8080", "token"))
        assertTrue(vault.all().isEmpty())
        assertNull(vault.load(""))
    }
}
