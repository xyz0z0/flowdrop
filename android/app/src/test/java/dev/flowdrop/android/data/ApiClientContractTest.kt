package dev.flowdrop.android.data

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.util.concurrent.TimeUnit

/**
 * Pins the frozen Hub contract from the client side: CSRF double submit,
 * matching Origin header, cookie session, pagination, streaming upload and
 * streaming download with SHA-256 verification.
 */
class ApiClientContractTest {

    private lateinit var server: MockWebServer
    private lateinit var store: SessionStore
    private lateinit var jar: PersistentCookieJar
    private lateinit var client: ApiClient
    private lateinit var endpoint: HubEndpoint

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        endpoint = HubEndpoint.parse("http://${server.hostName}:${server.port}")!!
        store = SessionStore(InMemorySecureStorage())
        store.switchTo(endpoint)
        jar = PersistentCookieJar(store)
        client = ApiClient(endpoint, store, jar)
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun loginResponse() = MockResponse()
        .setResponseCode(200)
        .addHeader("Content-Type", "application/json")
        .addHeader(
            "Set-Cookie",
            "flowdrop_session=sess-1; Path=/; HttpOnly; SameSite=Strict",
        )
        .addHeader(
            "Set-Cookie",
            "flowdrop_csrf=csrf-1; Path=/; SameSite=Strict",
        )
        .setBody(
            """{"authenticated":true,"csrf_token":"csrf-1","username":"admin",
               "expires_at":9999999999999,"limits":{"max_upload_bytes":1048576}}""",
        )

    private fun RecordedRequest.isWrite(method: String) = this.method == method && path?.startsWith("/api/") == true

    @Test
    fun `login stores csrf token and session cookies`() = runBlocking {
        server.enqueue(loginResponse())

        val info = client.login("admin", "hunter2")

        assertTrue(info.authenticated)
        assertEquals("csrf-1", client.csrfToken)
        assertEquals(1_048_576L, client.maxUploadBytes)

        val recorded = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/auth/login", recorded.path)
        assertEquals("POST", recorded.method)
        // The Hub requires the Origin to match its own scheme://host:port.
        assertEquals(endpoint.origin, recorded.getHeader("Origin"))
        val body = recorded.body.readUtf8()
        assertTrue(body.contains("\"username\":\"admin\""))
        assertTrue(body.contains("\"password\":\"hunter2\""))

        // Nothing but cookies/token is persisted; the password is gone.
        val stored = store.load()!!
        assertEquals("csrf-1", stored.csrfToken)
        assertEquals("admin", stored.username)
        assertTrue(stored.cookies.any { it.name == "flowdrop_session" })
        assertFalse(stored.toString().contains("hunter2"))
    }

    @Test
    fun `create text sends csrf header, origin and json body`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(
            MockResponse().setResponseCode(201)
                .addHeader("Content-Type", "application/json")
                .setBody(
                    """{"id":"item-1","type":"link","text":"https://example.com",
                       "sender_device":"Android","created_at":1700000000000}""",
                ),
        )

        val item = client.createText("link", "https://example.com", "Android")

        assertEquals("item-1", item.id)
        assertTrue(item.isLink)

        server.takeRequest(1, TimeUnit.SECONDS) // login
        val write = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/items/text", write.path)
        assertEquals("csrf-1", write.getHeader("X-FlowDrop-CSRF"))
        assertEquals(endpoint.origin, write.getHeader("Origin"))
        assertTrue(write.getHeader("Content-Type")!!.startsWith("application/json"))
        val body = write.body.readUtf8()
        assertTrue(body.contains("\"type\":\"link\""))
        assertTrue(body.contains("\"sender_device\":\"Android\""))
    }

    @Test
    fun `list sends cursor and limit and parses next_cursor`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(
            MockResponse().setResponseCode(200)
                .addHeader("Content-Type", "application/json")
                .setBody(
                    """{"items":[{"id":"a","type":"text","text":"hi","sender_device":"PC","created_at":1}],
                       "next_cursor":"Y3Vyc29y"}""",
                ),
        )

        val page = client.list(cursor = "prev", limit = 30)
        assertEquals(1, page.items.size)
        assertEquals("Y3Vyc29y", page.nextCursor)

        server.takeRequest(1, TimeUnit.SECONDS)
        val list = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertTrue(list.path!!.startsWith("/api/v1/items?"))
        assertTrue(list.path!!.contains("limit=30"))
        assertTrue(list.path!!.contains("cursor=prev"))
        // Reads never carry the CSRF header (the Hub only requires it on writes).
        assertEquals(null, list.getHeader("X-FlowDrop-CSRF"))
        assertEquals(endpoint.origin, list.getHeader("Origin"))
    }

    @Test
    fun `upload streams the file with multipart and csrf`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(
            MockResponse().setResponseCode(201)
                .addHeader("Content-Type", "application/json")
                .setBody(
                    """{"id":"item-2","type":"file","sender_device":"Android","created_at":2,
                       "blob":{"id":"b1","name":"app.apk","content_type":"application/octet-stream",
                       "size_bytes":11,"sha256":"deadbeef","created_at":2}}""",
                ),
        )
        val payload = "hello world".toByteArray()
        var progress = 0L
        val source = UploadSource(
            name = "app.apk",
            size = payload.size.toLong(),
            contentType = "application/vnd.android.package-archive",
            open = { payload.inputStream() },
        )

        val item = client.upload(source, "Android") { loaded, _ -> progress = loaded }

        assertEquals("item-2", item.id)
        assertTrue(item.isApk)
        assertEquals(payload.size.toLong(), progress)

        server.takeRequest(1, TimeUnit.SECONDS)
        val upload = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/items/upload", upload.path)
        assertEquals("csrf-1", upload.getHeader("X-FlowDrop-CSRF"))
        assertEquals(endpoint.origin, upload.getHeader("Origin"))
        assertTrue(upload.getHeader("Content-Type")!!.startsWith("multipart/form-data"))
        val raw = upload.body.readByteArray()
        val text = String(raw, Charsets.ISO_8859_1)
        assertTrue(text.contains("name=\"sender_device\""))
        assertTrue(text.contains("name=\"file\"; filename=\"app.apk\""))
        assertTrue(text.contains("hello world"))
    }

    @Test
    fun `download streams bytes and verifies sha256 header`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")

        val content = "attachment-bytes".toByteArray()
        val digest = java.security.MessageDigest.getInstance("SHA-256")
            .digest(content).joinToString("") { "%02x".format(it) }
        server.enqueue(
            MockResponse().setResponseCode(200)
                .addHeader("Content-Type", "application/octet-stream")
                .addHeader("Content-Disposition", "attachment; filename=\"a.bin\"")
                .addHeader("X-Content-SHA256", digest)
                .setBody(okio.Buffer().write(content)),
        )

        val out = ByteArrayOutputStream()
        var seen = 0L
        val result = client.download("item-2", out) { loaded, _ -> seen = loaded }

        assertTrue(out.toByteArray().contentEquals(content))
        assertEquals(content.size.toLong(), result.bytes)
        assertEquals(digest, result.sha256)
        assertEquals(content.size.toLong(), seen)

        server.takeRequest(1, TimeUnit.SECONDS)
        val download = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/items/item-2/download", download.path)
        assertEquals(endpoint.origin, download.getHeader("Origin"))
    }

    @Test
    fun `checksum mismatch fails the download`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(
            MockResponse().setResponseCode(200)
                .addHeader("Content-Type", "application/octet-stream")
                .addHeader("X-Content-SHA256", "0000000000000000000000000000000000000000000000000000000000000000")
                .setBody("tampered"),
        )
        try {
            client.download("item-2", ByteArrayOutputStream()) { _, _ -> }
            fail("expected checksum mismatch")
        } catch (e: ApiException) {
            assertEquals("checksum_mismatch", e.code)
        }
    }

    @Test
    fun `401 triggers the unauthorized callback and carries the server code`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        var unauthorized = false
        client.onUnauthorized = { unauthorized = true }
        server.enqueue(
            MockResponse().setResponseCode(401)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"unauthenticated","message":"login required"}}"""),
        )
        try {
            client.list()
            fail("expected 401")
        } catch (e: ApiException) {
            assertTrue(e.isUnauthorized)
            assertEquals("unauthenticated", e.code)
        }
        assertTrue(unauthorized)
    }

    @Test
    fun `session restore reissues csrf when the cookie was lost`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(
            MockResponse().setResponseCode(200)
                .addHeader("Content-Type", "application/json")
                .addHeader("Set-Cookie", "flowdrop_csrf=csrf-2; Path=/; SameSite=Strict")
                .setBody("""{"authenticated":true,"csrf_token":"csrf-2"}"""),
        )

        val info = client.session()

        assertTrue(info.authenticated)
        assertEquals("csrf-2", client.csrfToken)

        server.takeRequest(1, TimeUnit.SECONDS)
        val restore = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/auth/session", restore.path)
        // The stored session cookie is sent so the Hub can recognize us.
        val cookieHeader = restore.getHeader("Cookie").orEmpty()
        assertTrue("expected session cookie, got: $cookieHeader", cookieHeader.contains("flowdrop_session=sess-1"))
    }

    @Test
    fun `logout clears local cookies and token`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(MockResponse().setResponseCode(204))

        client.logout()

        assertTrue(store.load() == null)
        assertEquals("", client.csrfToken)
        assertTrue(jar.loadForRequest(endpoint.baseUrl).isEmpty())

        server.takeRequest(1, TimeUnit.SECONDS)
        val logout = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/auth/logout", logout.path)
        assertEquals("csrf-1", logout.getHeader("X-FlowDrop-CSRF"))
    }

    @Test
    fun `not_initialized error is surfaced from the login response`() = runBlocking {
        server.enqueue(
            MockResponse().setResponseCode(403)
                .addHeader("Content-Type", "application/json")
                .setBody(
                    """{"error":{"code":"not_initialized","message":"no admin password set"}}""",
                ),
        )
        try {
            client.login("admin", "pw")
            fail("expected ApiException")
        } catch (e: ApiException) {
            assertTrue(e.isNotInitialized)
            assertEquals(403, e.status)
        }
    }

    @Test
    fun `upload 413 maps to too large`() = runBlocking {
        server.enqueue(loginResponse())
        client.login("admin", "pw")
        server.enqueue(
            MockResponse().setResponseCode(413)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"too_large","message":"file exceeds the 512 MiB limit"}}"""),
        )
        val source = UploadSource("big.bin", 4, "application/octet-stream", { ByteArray(4).inputStream() })
        try {
            client.upload(source, "Android") { _, _ -> }
            fail("expected 413")
        } catch (e: ApiException) {
            assertTrue(e.isTooLarge)
        }
    }
}
