package dev.flowdrop.android.data

import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SessionIsolationTest {

    private fun endpoint(raw: String) = HubEndpoint.parse(raw)!!

    @Test
    fun `changing hub address destroys the previous session`() {
        val storage = InMemorySecureStorage()
        val store = SessionStore(storage)

        val first = endpoint("http://192.168.1.50:8080")
        store.switchTo(first)
        store.save(
            StoredSession(
                baseUrl = first.baseUrl.toString(),
                origin = first.origin,
                username = "admin",
                csrfToken = "token-a",
                cookies = listOf(StoredCookie("flowdrop_session", "secret-a", "192.168.1.50")),
            ),
        )

        val second = endpoint("http://10.0.0.7:8080")
        store.switchTo(second)

        assertNull("session must be cleared when the origin changes", store.load()?.cookies?.takeIf { it.isNotEmpty() })
        assertNull(store.loadFor(first.origin))
        assertEquals(second.origin, store.load()?.origin)
    }

    @Test
    fun `same origin keeps the session`() {
        val store = SessionStore(InMemorySecureStorage())
        val ep = endpoint("http://192.168.1.50:8080")
        store.switchTo(ep)
        store.save(StoredSession(ep.baseUrl.toString(), ep.origin, csrfToken = "t"))
        store.switchTo(ep)
        assertEquals("t", store.load()?.csrfToken)
    }

    @Test
    fun `stored session never contains a password`() {
        val storage = InMemorySecureStorage()
        val store = SessionStore(storage)
        val ep = endpoint("http://hub.lan:8080")
        store.switchTo(ep)
        store.save(StoredSession(ep.baseUrl.toString(), ep.origin, username = "admin", csrfToken = "t"))
        val raw = storage.read().orEmpty()
        assertTrue(raw.contains("csrfToken"))
        assertTrue("password must not be a field", !raw.contains("password", ignoreCase = true))
    }

    @Test
    fun `cookie jar never returns cookies to a different origin`() {
        val store = SessionStore(InMemorySecureStorage())
        val hub = endpoint("http://192.168.1.50:8080")
        store.switchTo(hub)
        val jar = PersistentCookieJar(store)

        jar.saveFromResponse(
            hub.baseUrl,
            listOf(
                okhttp3.Cookie.Builder()
                    .name("flowdrop_session")
                    .value("abc")
                    .hostOnlyDomain("192.168.1.50")
                    .path("/")
                    .expiresAt(System.currentTimeMillis() + 60_000)
                    .build(),
            ),
        )
        assertEquals(1, jar.loadForRequest(hub.baseUrl).size)

        val other = "http://evil.example".toHttpUrl()
        assertTrue("cookies must not leak to another host", jar.loadForRequest(other).isEmpty())
        assertEquals(0, jar.snapshot("http://evil.example").size)
    }

    @Test
    fun `expired cookies are not returned`() {
        val store = SessionStore(InMemorySecureStorage())
        val hub = endpoint("http://hub.lan:8080")
        store.switchTo(hub)
        val jar = PersistentCookieJar(store)
        jar.saveFromResponse(
            hub.baseUrl,
            listOf(
                okhttp3.Cookie.Builder()
                    .name("flowdrop_session")
                    .value("abc")
                    .hostOnlyDomain("hub.lan")
                    .path("/")
                    .expiresAt(System.currentTimeMillis() - 1000)
                    .build(),
            ),
        )
        assertTrue(jar.loadForRequest(hub.baseUrl).isEmpty())
    }
}
