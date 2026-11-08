package dev.flowdrop.android.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.atomic.AtomicBoolean

enum class ConnState { CONNECTING, OPEN, CLOSED, PAUSED }

/**
 * Authenticated WebSocket subscription to `/api/v1/events`.
 *
 * The Hub uses it only as a hint channel; on every (re)open the ViewModel does
 * a REST resync so a dropped socket can never leave stale state behind. The
 * handshake carries the same session Cookie (via the shared CookieJar) and the
 * Hub-matching Origin header required by the server.
 *
 * A dropped socket is *not* an authorization failure: [onUnavailable] lets the
 * caller rate-limit a route reselection, while only a 401 handshake or a
 * `1008 policy violation` close (the Hub's device-revocation signal) calls
 * [onUnauthorized].
 */
class EventStream(
    private val scope: CoroutineScope,
    private val clientProvider: () -> ApiClient?,
    private val onEvent: (type: String, id: String) -> Unit,
    private val onState: (ConnState) -> Unit,
    private val onUnauthorized: () -> Unit,
    private val onUnavailable: () -> Unit = {},
) {
    private var socket: WebSocket? = null
    private var reconnectJob: Job? = null
    private var attempts = 0

    /** Incremented on every start/stop so late callbacks from an old socket
     *  cannot schedule a second connection. */
    private var generation = 0

    @Volatile
    private var running = false

    fun start() {
        running = true
        if (socket == null) connect(++generation)
    }

    fun restart() {
        running = false
        val stale = socket
        socket = null
        reconnectJob?.cancel()
        reconnectJob = null
        stale?.close(NORMAL_CLOSURE, "restart")
        start()
    }

    fun stop() {
        running = false
        val stale = socket
        socket = null
        reconnectJob?.cancel()
        reconnectJob = null
        generation++
        stale?.close(NORMAL_CLOSURE, "background")
        onState(ConnState.PAUSED)
    }

    private fun connect(gen: Int) {
        if (!running || gen != generation) return
        val api = clientProvider() ?: return
        onState(ConnState.CONNECTING)
        val request = Request.Builder()
            .url(api.endpoint.eventsUrl())
            .header("Origin", api.endpoint.origin)
            .build()
        socket = api.http.newWebSocket(request, Listener(gen))
    }

    private fun scheduleReconnect(gen: Int) {
        if (!running || gen != generation) return
        attempts = (attempts + 1).coerceAtMost(6)
        val backoff = (500L shl (attempts - 1)).coerceAtMost(15_000L)
        onState(ConnState.CLOSED)
        // One hint per failed link; the caller throttles the actual reselection.
        onUnavailable()
        reconnectJob?.cancel()
        reconnectJob = scope.launch {
            delay(backoff)
            if (running && gen == generation) connect(gen)
        }
    }

    private inner class Listener(private val gen: Int) : WebSocketListener() {
        private val settled = AtomicBoolean(false)

        /** Set by a `1008 policy violation` close: the Hub revoked this device. */
        private val revoked = AtomicBoolean(false)

        override fun onOpen(webSocket: WebSocket, response: Response) {
            if (gen != generation) {
                webSocket.close(NORMAL_CLOSURE, "stale")
                return
            }
            attempts = 0
            onState(ConnState.OPEN)
            // A REST resync on every open covers events missed while offline.
            onEvent("resync", "")
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            if (gen != generation) return
            val type = extractString(text, "type") ?: return
            if (type == "hello") return
            if (type.startsWith("item.")) {
                onEvent(type, extractString(text, "id").orEmpty())
            }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            if (code == POLICY_VIOLATION) revoked.set(true)
            webSocket.close(NORMAL_CLOSURE, null)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            if (gen != generation) return
            if (socket === webSocket) socket = null
            if (!settled.compareAndSet(false, true)) return
            if (revoked.get() || code == POLICY_VIOLATION) {
                onUnauthorized()
                onState(ConnState.CLOSED)
                return
            }
            if (running) scheduleReconnect(gen)
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            if (gen != generation) return
            if (socket === webSocket) socket = null
            if (!settled.compareAndSet(false, true)) return
            // Only an explicit authorization rejection tears the session down;
            // a plain network failure just asks for a (throttled) reselection.
            if (response?.code == 401 || revoked.get()) {
                onUnauthorized()
                onState(ConnState.CLOSED)
                return
            }
            if (running) scheduleReconnect(gen)
        }
    }

    /**
     * Minimal extraction that avoids a full JSON parser on the hot path; the
     * event payload is always `{"type":"...","id":"..."}`.
     */
    private fun extractString(json: String, key: String): String? {
        val needle = "\"$key\""
        val at = json.indexOf(needle)
        if (at < 0) return null
        val colon = json.indexOf(':', at + needle.length)
        if (colon < 0) return null
        val open = json.indexOf('"', colon + 1)
        if (open < 0) return null
        val close = json.indexOf('"', open + 1)
        if (close < 0) return null
        return json.substring(open + 1, close)
    }

    private companion object {
        const val NORMAL_CLOSURE = 1000

        /** RFC 6455 policy violation; the Hub uses it for device revocation. */
        const val POLICY_VIOLATION = 1008
    }
}
