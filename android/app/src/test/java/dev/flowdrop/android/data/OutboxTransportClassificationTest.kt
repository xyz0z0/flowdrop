package dev.flowdrop.android.data

import java.io.ByteArrayInputStream
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test

/**
 * Pins the replay decision the text/attachment outbox needs from
 * [ApiClient.createText] and [ApiClient.upload].
 *
 * [OutboxTransportException.mayHaveReachedHub] must be `false` only when the
 * failure is proven to happen before OkHttp's network layer was entered, i.e.
 * when nothing could have been sent. Anything the Hub may already have stored —
 * including a lost answer to a request it clearly received — must be `true`, so
 * the outbox keeps it pending instead of replaying it into a duplicate item.
 *
 * Hub responses are never reclassified: 401/413 stay [ApiException] with the
 * existing unauthorized callback.
 */
class OutboxTransportClassificationTest {

    private lateinit var server: MockWebServer
    private lateinit var endpoint: HubEndpoint
    private lateinit var store: SessionStore
    private lateinit var jar: PersistentCookieJar
    private lateinit var client: ApiClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        endpoint = endpointFor(server.port)
        store = SessionStore(InMemorySecureStorage())
        store.switchTo(endpoint)
        jar = PersistentCookieJar(store)
        client = ApiClient(endpoint, store, jar)
    }

    @After
    fun tearDown() {
        runCatching { server.shutdown() }
    }

    // ---------- helpers ----------

    private fun endpointFor(port: Int): HubEndpoint =
        HubEndpoint.parse("http://127.0.0.1:$port")!!

    /** A port nothing listens on after the server behind it was shut down. */
    private fun deadEndpoint(): HubEndpoint {
        val port = server.port
        server.shutdown()
        return endpointFor(port)
    }

    /**
     * A client whose address refuses connections: its store and jar are distinct
     * so the live [server] above is never involved.
     */
    private fun clientFor(target: HubEndpoint): ApiClient {
        val deadStore = SessionStore(InMemorySecureStorage())
        deadStore.switchTo(target)
        return ApiClient(target, deadStore, PersistentCookieJar(deadStore))
    }

    private fun textItemJson(id: String = "item-1") =
        """{"id":"$id","type":"text","category":"text","text":"hi",
           "sender_device":"Pixel 9","created_at":1}"""

    private fun fileItemJson(id: String = "item-2") =
        """{"id":"$id","type":"file","category":"other","sender_device":"Pixel 9","created_at":2,
           "blob":{"id":"b1","name":"a.bin","content_type":"application/octet-stream",
                   "size_bytes":3,"sha256":"00"}}"""

    private fun jsonResponse(body: String) = MockResponse()
        .setResponseCode(200)
        .addHeader("Content-Type", "application/json")
        .setBody(body)

    private fun uploadSource(bytes: ByteArray = byteArrayOf(1, 2, 3)) = UploadSource(
        name = "a.bin",
        size = bytes.size.toLong(),
        contentType = "application/octet-stream",
    ) { ByteArrayInputStream(bytes) }

    private suspend fun expectOutboxFailure(block: suspend () -> Unit): OutboxTransportException {
        try {
            block()
        } catch (e: OutboxTransportException) {
            return e
        } catch (e: Throwable) {
            throw AssertionError(
                "expected OutboxTransportException, got ${e::class.java.name}: ${e.message}",
                e,
            )
        }
        throw AssertionError("expected OutboxTransportException, the call succeeded")
    }

    // ---------- success paths ----------

    @Test
    fun `createText success returns the item and sends one origin-tagged write`() = runBlocking {
        server.enqueue(jsonResponse(textItemJson()))

        val item = client.createText("text", "hi", "Pixel 9")

        assertEquals("item-1", item.id)
        assertEquals("hi", item.text)
        assertEquals("text", item.type)
        assertEquals(1, server.requestCount)
        val recorded = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("POST", recorded.method)
        assertEquals("/api/v1/items/text", recorded.path)
        assertEquals(endpoint.origin, recorded.getHeader("Origin"))
    }

    @Test
    fun `upload success returns the item, reports progress and sends one write`() = runBlocking {
        server.enqueue(jsonResponse(fileItemJson()))
        var progress = 0L

        val item = client.upload(uploadSource(), "Pixel 9") { loaded, _ -> progress = loaded }

        assertEquals("item-2", item.id)
        assertEquals(3L, progress)
        assertEquals(1, server.requestCount)
        val recorded = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("POST", recorded.method)
        assertEquals("/api/v1/items/upload", recorded.path)
    }

    // ---------- proven not delivered: replays are safe ----------

    @Test
    fun `connection refused is proven not to have reached the hub`() = runBlocking {
        val refused = clientFor(deadEndpoint())

        val failure = expectOutboxFailure { refused.createText("text", "hi", "Pixel 9") }

        assertFalse("no socket ever existed, so nothing was sent", failure.mayHaveReachedHub)
        assertTrue("the underlying IOException must stay available", failure.cause is IOException)
    }

    @Test
    fun `connection refused on upload is proven not to have reached the hub`() = runBlocking {
        val source = uploadSource()
        val refused = clientFor(deadEndpoint())

        val failure = expectOutboxFailure { refused.upload(source, "Pixel 9") { _, _ -> } }

        assertFalse(failure.mayHaveReachedHub)
        assertTrue(failure.cause is IOException)
    }

    // ---------- maybe delivered: must stay pending ----------

    @Test
    fun `server that closed the socket before reading is treated as maybe delivered`() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))

        val failure = expectOutboxFailure { client.createText("text", "hi", "Pixel 9") }

        // A TCP connection did exist, so a conservative caller must not replay.
        assertTrue(failure.mayHaveReachedHub)
    }

    @Test
    fun `server that received the request but never answered is treated as maybe delivered`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST)
                .setResponseCode(201)
                .setBody(textItemJson()),
        )

        val failure = expectOutboxFailure { client.createText("text", "hi", "Pixel 9") }

        assertTrue("the request is on the Hub, only the answer is missing", failure.mayHaveReachedHub)
        // Proves the Hub side really consumed the write.
        assertEquals(1, server.requestCount)
        assertEquals("/api/v1/items/text", server.takeRequest(1, TimeUnit.SECONDS)?.path)
    }

    @Test
    fun `truncated success body is treated as maybe delivered`() = runBlocking {
        // The header promises more than the body carries, so OkHttp answers the
        // call and then fails while reading it: the item may already exist.
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .addHeader("Content-Type", "application/json")
                .setBody(textItemJson())
                .setHeader("Content-Length", "4096")
                .setSocketPolicy(SocketPolicy.DISCONNECT_AT_END),
        )

        val failure = expectOutboxFailure { client.createText("text", "hi", "Pixel 9") }

        assertTrue("a delivered write whose answer was lost must not be replayed", failure.mayHaveReachedHub)
        assertEquals(1, server.requestCount)
    }

    // ---------- OkHttp's own follow-ups are switched off too ----------

    @Test
    fun `503 asking for an immediate retry is not resent for createText`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setResponseCode(503)
                .addHeader("Content-Type", "application/json")
                .addHeader("Retry-After", "0")
                .setBody("""{"error":{"code":"unavailable","message":"try later"}}"""),
        )
        // A follow-up would consume this and make the write look successful.
        server.enqueue(jsonResponse(textItemJson()))

        try {
            client.createText("text", "hi", "Pixel 9")
            fail("expected 503")
        } catch (e: ApiException) {
            assertEquals(503, e.status)
        }

        assertEquals("the write must reach the Hub exactly once", 1L, server.requestCount.toLong())
        assertNotNull(server.takeRequest(250, TimeUnit.MILLISECONDS))
        assertNull("no follow-up request may be sent", server.takeRequest(250, TimeUnit.MILLISECONDS))
    }

    @Test
    fun `503 asking for an immediate retry is not resent for upload`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setResponseCode(503)
                .addHeader("Content-Type", "application/json")
                .addHeader("Retry-After", "0")
                .setBody("""{"error":{"code":"unavailable","message":"try later"}}"""),
        )
        server.enqueue(jsonResponse(fileItemJson()))

        try {
            client.upload(uploadSource(), "Pixel 9") { _, _ -> }
            fail("expected 503")
        } catch (e: ApiException) {
            assertEquals(503, e.status)
        }

        assertEquals("the upload must reach the Hub exactly once", 1L, server.requestCount.toLong())
        assertNotNull(server.takeRequest(250, TimeUnit.MILLISECONDS))
        assertNull("no follow-up request may be sent", server.takeRequest(250, TimeUnit.MILLISECONDS))
    }

    // ---------- Hub answers keep their existing types ----------

    @Test
    fun `401 on createText stays an ApiException and still fires the unauthorized callback`() = runBlocking {
        var unauthorized = 0
        client.onUnauthorized = { unauthorized++ }
        server.enqueue(
            MockResponse()
                .setResponseCode(401)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"unauthenticated","message":"pairing required"}}"""),
        )

        try {
            client.createText("text", "hi", "Pixel 9")
            fail("expected 401")
        } catch (e: ApiException) {
            assertEquals(401, e.status)
            assertEquals("unauthenticated", e.code)
            assertTrue(e.isUnauthorized)
        }
        assertEquals(1, unauthorized)
        assertEquals(1, server.requestCount)
    }

    @Test
    fun `413 on upload stays an ApiException and is not replayed`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setResponseCode(413)
                .addHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"too_large","message":"file too large"}}"""),
        )

        try {
            client.upload(uploadSource(), "Pixel 9") { _, _ -> }
            fail("expected 413")
        } catch (e: ApiException) {
            assertEquals(413, e.status)
            assertTrue(e.isTooLarge)
        }
        assertEquals(1, server.requestCount)
    }

    // ---------- no widening beyond the two outbox writes ----------

    @Test
    fun `the client used for writes never replays a connection failure on its own`() {
        // The outbox classification only makes sense on top of this: OkHttp's
        // built-in retry would silently resend a write that may have landed.
        assertFalse(client.http.retryOnConnectionFailure)
        assertFalse(client.http.followRedirects)
    }

    @Test
    fun `untagged reads still throw the plain IOException`() = runBlocking {
        val refused = clientFor(deadEndpoint())

        try {
            refused.list()
            fail("expected a network failure")
        } catch (e: OutboxTransportException) {
            fail("only createText and upload are classified, got $e")
        } catch (e: IOException) {
            // Unchanged behaviour for every other operation.
        }
    }
}
