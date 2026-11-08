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
 * Pins the frozen Hub contract from the client side: native pairing, CSRF double
 * submit, matching Origin header, cookie session, pagination, streaming upload
 * and streaming download with SHA-256 verification.
 *
 * The session is always established through POST /auth/pair (`native=true`)
 * with a fixture shaped like the real frozen payload: `hub` (id/name/endpoints),
 * `device_id`, the once-only `device_token` and the session/CSRF cookies.
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

    private fun pairResponse() = MockResponse()
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
            """{"authenticated":true,"csrf_token":"csrf-1",
               "expires_at":9999999999999,"limits":{"max_upload_bytes":1048576},
               "device_id":"dev-1","device_token":"tok-1",
               "hub":{"id":"hub-1","name":"Home",
                      "endpoints":[{"url":"${endpoint.origin}","kind":"lan"}]}}""",
        )

    /** Pairs the fixture device; the pair request itself is the first recorded one. */
    private suspend fun pairDevice(code: String = "0123") = client.pair(code, "Pixel 9")

    private fun RecordedRequest.isWrite(method: String) = this.method == method && path?.startsWith("/api/") == true

    @Test
    fun `pair stores csrf token, cookies and the device pairing`() = runBlocking {
        server.enqueue(pairResponse())

        val info = pairDevice()

        assertTrue(info.authenticated)
        assertEquals("csrf-1", client.csrfToken)
        assertEquals(1_048_576L, client.maxUploadBytes)

        val recorded = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/auth/pair", recorded.path)
        assertEquals("POST", recorded.method)
        // The Hub requires the Origin to match its own scheme://host:port.
        assertEquals(endpoint.origin, recorded.getHeader("Origin"))
        val body = recorded.body.readUtf8()
        assertTrue(body.contains("\"code\":\"0123\""))
        assertTrue(body.contains("\"device_name\":\"Pixel 9\""))
        assertTrue(body.contains("\"native\":true"))

        // Nothing but cookies/token/pairing is persisted; the code is gone.
        val stored = store.load()!!
        assertEquals("csrf-1", stored.csrfToken)
        assertEquals("hub-1", stored.hubId)
        assertEquals("dev-1", stored.deviceId)
        assertEquals("tok-1", stored.deviceToken)
        assertTrue(stored.cookies.any { it.name == "flowdrop_session" })
        assertFalse(stored.toString().contains("0123"))
    }

    @Test
    fun `pairing errors surface pairing_invalid and pairing_locked without dropping the pairing`() = runBlocking {
        server.enqueue(pairResponse())
        pairDevice()
        val paired = store.load()!!
        var unauthorized = 0
        client.onUnauthorized = { unauthorized++ }

        server.enqueue(
            MockResponse().setResponseCode(401)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"pairing_invalid","message":"invalid code"}}"""),
        )
        try {
            pairDevice("0000")
            fail("expected a rejected pairing to throw")
        } catch (e: ApiException) {
            assertEquals(401, e.status)
            assertEquals("pairing_invalid", e.code)
        }

        server.enqueue(
            MockResponse().setResponseCode(429)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"pairing_locked","message":"too many attempts"}}"""),
        )
        try {
            pairDevice("0000")
            fail("expected a locked pairing to throw")
        } catch (e: ApiException) {
            assertEquals(429, e.status)
            assertEquals("pairing_locked", e.code)
        }

        // A mistyped code is not an expired session: no global logout, and the
        // existing pairing (Hub ID + device token) survives.
        assertEquals("pairing failures must not trigger the global unauthorized path", 0, unauthorized)
        val after = store.load()!!
        assertEquals(paired.hubId, after.hubId)
        assertEquals(paired.deviceToken, after.deviceToken)
    }

    @Test
    fun `create text sends csrf header, origin and json body`() = runBlocking {
        server.enqueue(pairResponse())
        pairDevice()
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

        server.takeRequest(1, TimeUnit.SECONDS) // pairing
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
        server.enqueue(pairResponse())
        pairDevice()
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
        // The device token is a restore credential only: it never rides along.
        assertEquals(null, list.getHeader("Authorization"))
        assertEquals(endpoint.origin, list.getHeader("Origin"))
    }

    @Test
    fun `upload streams the file with multipart and csrf`() = runBlocking {
        server.enqueue(pairResponse())
        pairDevice()
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
        server.enqueue(pairResponse())
        pairDevice()

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
        server.enqueue(pairResponse())
        pairDevice()
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
    fun `401 on a protected request triggers the unauthorized callback and carries the server code`() = runBlocking {
        server.enqueue(pairResponse())
        pairDevice()
        var unauthorized = false
        client.onUnauthorized = { unauthorized = true }
        server.enqueue(
            MockResponse().setResponseCode(401)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"unauthenticated","message":"pairing required"}}"""),
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
        server.enqueue(pairResponse())
        pairDevice()
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
        // GET /auth/session never echoes the token, so the stored one must survive.
        assertEquals("tok-1", store.load()!!.deviceToken)
    }

    @Test
    fun `logout clears local cookies and token`() = runBlocking {
        server.enqueue(pairResponse())
        pairDevice()
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
    fun `upload 413 maps to too large`() = runBlocking {
        server.enqueue(pairResponse())
        pairDevice()
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
