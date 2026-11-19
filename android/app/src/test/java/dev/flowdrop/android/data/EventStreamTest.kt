package dev.flowdrop.android.data

import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Real-socket test for the presence invalidation notice delivered over
 * `/api/v1/events`.
 *
 * The fake Hub upgrades the connection and pushes `hello`, an `item.created` and
 * a `presence.changed` frame. Only an actual WebSocket upgrade can carry them, so
 * this pins the dispatch in [EventStream] instead of mirroring it in a fake.
 */
class EventStreamTest {

    private lateinit var server: MockWebServer
    private lateinit var endpoint: HubEndpoint
    private lateinit var api: ApiClient
    private lateinit var scope: CoroutineScope
    private var stream: EventStream? = null

    /** Server end of the upgraded socket, closed before the server shuts down. */
    private val serverSocket = AtomicReference<WebSocket?>(null)
    private val serverClosed = CountDownLatch(1)

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        endpoint = HubEndpoint.parse("http://${server.hostName}:${server.port}")!!
        val store = SessionStore(InMemorySecureStorage())
        store.switchTo(endpoint)
        api = ApiClient(endpoint, store, PersistentCookieJar(store))
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After
    fun tearDown() {
        stream?.stop()
        // A live WebSocket keeps MockWebServer's queue alive, so the upgraded
        // connection must finish its close handshake first.
        if (serverSocket.get() != null) {
            serverSocket.get()?.close(NORMAL_CLOSURE, "test finished")
            serverClosed.await(5, TimeUnit.SECONDS)
        }
        scope.cancel()
        server.shutdown()
    }

    @Test
    fun `presence changed is delivered as an invalidating event without an id`() {
        val events = Collections.synchronizedList(mutableListOf<String>())
        val delivered = CountDownLatch(1)
        server.enqueue(
            MockResponse().withWebSocketUpgrade(
                object : WebSocketListener() {
                    override fun onOpen(webSocket: WebSocket, response: Response) {
                        serverSocket.set(webSocket)
                        webSocket.send("""{"type":"hello"}""")
                        webSocket.send("""{"type":"item.created","id":"i-1"}""")
                        // The Hub's invalidation notice: no list, no identity.
                        webSocket.send("""{"type":"presence.changed"}""")
                    }

                    override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                        webSocket.close(NORMAL_CLOSURE, null)
                    }

                    override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                        serverClosed.countDown()
                    }
                },
            ),
        )
        stream = EventStream(
            scope = scope,
            clientProvider = { api },
            onEvent = { type, id ->
                events.add("$type:$id")
                if (type == EVENT_PRESENCE_CHANGED) delivered.countDown()
            },
            onState = {},
            onUnauthorized = {},
        ).also { it.start() }

        assertTrue(
            "presence.changed must reach onEvent, got: $events",
            delivered.await(10, TimeUnit.SECONDS),
        )
        // The notice carries no id: the caller answers with a REST refetch.
        assertTrue("expected an empty id, got: $events", events.contains("presence.changed:"))
        // Existing item dispatch and the open-time resync hint are untouched.
        assertTrue("item events must still be delivered: $events", events.contains("item.created:i-1"))
        assertTrue("socket open must ask for a resync: $events", events.contains("resync:"))
        // hello stays internal bookkeeping.
        assertFalse("hello must not surface: $events", events.any { it.startsWith("hello") })
    }

    private companion object {
        const val NORMAL_CLOSURE = 1000
    }
}
