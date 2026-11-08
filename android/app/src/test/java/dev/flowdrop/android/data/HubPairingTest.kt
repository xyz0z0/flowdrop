package dev.flowdrop.android.data

import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
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
 * Pins the PA-05A pairing/restore contract:
 *
 * - the pairs request is `native=true`, the code is never persisted;
 * - switching between saved addresses of one Hub drops cookies but keeps the
 *   pairing, while a manual switch to an unknown Hub drops everything;
 * - the Hub ID probe runs without credentials and without redirects;
 * - the stored device token is only sent to a verified origin, never on
 *   ordinary requests, and a session response without `device_token` keeps it;
 * - nothing retries or follows redirects automatically.
 */
class HubPairingTest {

    private lateinit var server: MockWebServer
    private lateinit var storage: InMemorySecureStorage
    private lateinit var store: SessionStore
    private lateinit var jar: PersistentCookieJar
    private lateinit var endpoint: HubEndpoint
    private lateinit var client: ApiClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        endpoint = HubEndpoint.parse("http://${server.hostName}:${server.port}")!!
        storage = InMemorySecureStorage()
        store = SessionStore(storage)
        store.switchTo(endpoint)
        jar = PersistentCookieJar(store)
        client = ApiClient(endpoint, store, jar)
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun hubEndpoint(raw: String) = HubEndpoint.parse(raw)!!

    private fun pairResponse(endpoints: String) = MockResponse()
        .setResponseCode(200)
        .addHeader("Content-Type", "application/json")
        .addHeader("Set-Cookie", "flowdrop_session=sess-1; Path=/; HttpOnly; SameSite=Strict")
        .addHeader("Set-Cookie", "flowdrop_csrf=csrf-1; Path=/; SameSite=Strict")
        .setBody(
            """{"authenticated":true,"csrf_token":"csrf-1","expires_at":9999999999999,
               "limits":{"max_upload_bytes":1048576},
               "device_id":"dev-1","device_token":"tok-1",
               "hub":{"id":"hub-1","name":"Home","endpoints":$endpoints}}""",
        )

    private fun healthResponse(hubId: String) = MockResponse()
        .setResponseCode(200)
        .addHeader("Content-Type", "application/json")
        .setBody("""{"status":"ok","service":"flowdrop-hub","version":"test","hub_id":"$hubId"}""")

    /** Seeds a paired record at [origin] with a stale cookie + CSRF token. */
    private fun seedPaired(
        origin: HubEndpoint,
        hubId: String = "hub-1",
        candidates: List<HubEndpointInfo> = listOf(HubEndpointInfo(origin.origin, RouteCandidate.KIND_LAN)),
        cookies: List<StoredCookie> = listOf(
            StoredCookie("flowdrop_session", "stale", origin.baseUrl.host),
            StoredCookie("flowdrop_csrf", "stale-csrf", origin.baseUrl.host),
        ),
        csrfToken: String = "stale-csrf",
    ) {
        store.switchTo(origin)
        store.save(
            StoredSession(
                baseUrl = origin.origin,
                origin = origin.origin,
                csrfToken = csrfToken,
                cookies = cookies,
                hubId = hubId,
                deviceId = "dev-1",
                deviceToken = "tok-1",
                candidates = candidates,
            ),
        )
    }

    private fun RecordedRequest.cookieHeader(): String? = getHeader("Cookie")

    private fun RecordedRequest.authorization(): String? = getHeader("Authorization")

    // ---------- pairing ----------

    @Test
    fun `pair sends a native code request and stores the token without the code`() = runBlocking {
        server.enqueue(
            pairResponse(
                """[{"url":"http://192.168.1.50:8080","kind":"lan"},
                    {"url":"http://hub.tailnet.ts.net:8080","kind":"tailscale"},
                    {"url":"http://192.168.1.50:8080/","kind":"LAN"}]""",
            ),
        )

        val info = client.pair("0123", "Pixel 9")

        assertTrue(info.authenticated)
        assertEquals("hub-1", info.hub?.id)
        assertEquals("tok-1", info.deviceToken)

        val request = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("POST", request.method)
        assertEquals("/api/v1/auth/pair", request.path)
        assertEquals(endpoint.origin, request.getHeader("Origin"))
        val body = request.body.readUtf8()
        assertTrue(body.contains("\"code\":\"0123\""))
        assertTrue(body.contains("\"device_name\":\"Pixel 9\""))
        assertTrue(body.contains("\"native\":true"))

        val stored = store.load()!!
        assertEquals(endpoint.origin, stored.origin)
        assertEquals("hub-1", stored.hubId)
        assertEquals("dev-1", stored.deviceId)
        assertEquals("tok-1", stored.deviceToken)
        assertEquals("csrf-1", stored.csrfToken)
        assertTrue(stored.cookies.any { it.name == "flowdrop_session" })
        // LAN + Tailscale from the Hub, and the paired origin itself as "other".
        assertEquals(
            listOf(
                HubEndpointInfo("http://192.168.1.50:8080", RouteCandidate.KIND_LAN),
                HubEndpointInfo("http://hub.tailnet.ts.net:8080", RouteCandidate.KIND_TAILSCALE),
                HubEndpointInfo(endpoint.origin, RouteCandidate.KIND_OTHER),
            ),
            stored.candidates,
        )

        val raw = storage.read().orEmpty()
        assertFalse("pairing code must never be stored", raw.contains("0123"))
        assertFalse("pairing code field must never be stored", raw.contains("\"code\""))
    }

    @Test
    fun `a wrong pair code is a pairing failure and does not clear other state`() = runBlocking {
        seedPaired(endpoint)
        // A client built while a stale session exists would otherwise attach
        // that session's CSRF token; pairing must not carry it.
        val pairedClient = ApiClient(endpoint, store, jar)
        assertEquals("stale-csrf", pairedClient.csrfToken)
        var unauthorized = 0
        pairedClient.onUnauthorized = { unauthorized++ }
        server.enqueue(
            MockResponse()
                .setResponseCode(401)
                .setBody("""{"error":{"code":"pairing_invalid","message":"invalid code"}}"""),
        )

        try {
            pairedClient.pair("9999", "Pixel 9")
            fail("a rejected pairing must throw")
        } catch (e: ApiException) {
            assertEquals(401, e.status)
            assertEquals("pairing_invalid", e.code)
        }

        val request = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/auth/pair", request.path)
        assertNull("a pre-pair CSRF token must not be sent", request.getHeader("X-FlowDrop-CSRF"))
        assertEquals("pair 401 must not trigger the global unauthorized path", 0, unauthorized)
        val stored = store.load()!!
        assertEquals("hub-1", stored.hubId)
        assertEquals("tok-1", stored.deviceToken)
    }

    // ---------- switching addresses ----------

    @Test
    fun `switching to another saved address keeps pairing but drops cookies`() = runBlocking {
        val lan = endpoint
        val ts = hubEndpoint("http://hub.tailnet.ts.net:8080")
        seedPaired(
            lan,
            candidates = listOf(
                HubEndpointInfo(lan.origin, RouteCandidate.KIND_LAN),
                HubEndpointInfo(ts.origin, RouteCandidate.KIND_TAILSCALE),
            ),
        )
        assertEquals(2, jar.loadForRequest(lan.baseUrl).size)

        assertTrue(store.switchToPaired(ts, "hub-1"))

        val switched = store.load()!!
        assertEquals(ts.origin, switched.origin)
        assertEquals("", switched.csrfToken)
        assertTrue("cookies are scoped to the old origin", switched.cookies.isEmpty())
        assertEquals("hub-1", switched.hubId)
        assertEquals("dev-1", switched.deviceId)
        assertEquals("tok-1", switched.deviceToken)
        assertEquals(2, switched.candidates.size)
        assertTrue("cookie jar must not serve the old session", jar.loadForRequest(lan.baseUrl).isEmpty())
    }

    @Test
    fun `switching to an unknown address or hub is refused without leaking cookies`() {
        val lan = endpoint
        val ts = hubEndpoint("http://hub.tailnet.ts.net:8080")
        seedPaired(
            lan,
            candidates = listOf(
                HubEndpointInfo(lan.origin, RouteCandidate.KIND_LAN),
                HubEndpointInfo(ts.origin, RouteCandidate.KIND_TAILSCALE),
            ),
        )

        assertFalse("unknown origin is refused", store.switchToPaired(hubEndpoint("http://evil.example:8080"), "hub-1"))
        assertFalse("wrong hub id is refused", store.switchToPaired(lan, "other-hub"))

        val stored = store.load()!!
        assertEquals("hub-1", stored.hubId)
        assertEquals("tok-1", stored.deviceToken)
        assertEquals(lan.origin, stored.origin)
        assertEquals(2, stored.cookies.size)
        assertTrue(
            "cookies must never be served to an unverified origin",
            jar.loadForRequest(hubEndpoint("http://evil.example:8080").baseUrl).isEmpty(),
        )
    }

    @Test
    fun `a manual switch to an unknown hub clears the pairing`() {
        seedPaired(endpoint)

        store.switchTo(hubEndpoint("http://other.example:9090"))

        val stored = store.load()!!
        assertEquals("", stored.hubId)
        assertEquals("", stored.deviceToken)
        assertTrue(stored.cookies.isEmpty())
        assertEquals("", stored.csrfToken)
    }

    // ---------- credential-free probe ----------

    @Test
    fun `probe sends no credentials and reads only hub id`() = runBlocking {
        seedPaired(endpoint)
        server.enqueue(healthResponse("hub-1"))

        val hubId = HubProbe.hubId(RouteCandidate(endpoint.origin, RouteCandidate.KIND_LAN))

        assertEquals("hub-1", hubId)
        val request = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("GET", request.method)
        assertEquals("/api/v1/health", request.path)
        assertNull("probe must not send session cookies", request.cookieHeader())
        assertNull("probe must not send a bearer token", request.authorization())
    }

    @Test
    fun `probe does not follow redirects`() = runBlocking {
        val target = MockWebServer()
        target.start()
        try {
            server.enqueue(
                MockResponse()
                    .setResponseCode(302)
                    .addHeader("Location", "http://${target.hostName}:${target.port}/api/v1/health"),
            )
            target.enqueue(healthResponse("hub-1"))

            val hubId = HubProbe.hubId(RouteCandidate(endpoint.origin, RouteCandidate.KIND_OTHER))

            assertNull("a redirect is not a usable health answer", hubId)
            assertEquals("redirect target must never be contacted", 0, target.requestCount)
        } finally {
            target.shutdown()
        }
    }

    // ---------- restoring a paired session ----------

    @Test
    fun `restore refuses a different hub id without sending the token`() = runBlocking {
        seedPaired(endpoint)

        try {
            client.restorePairedSession("hub-2")
            fail("mismatched hub id must be refused")
        } catch (e: ApiException) {
            assertEquals("hub_mismatch", e.code)
        }

        assertEquals("no request may leave before verification", 0, server.requestCount)
        assertEquals("tok-1", store.load()!!.deviceToken)
    }

    @Test
    fun `restore refuses an address that is not a saved candidate`() = runBlocking {
        seedPaired(
            endpoint,
            candidates = listOf(HubEndpointInfo("http://192.168.1.50:8080", RouteCandidate.KIND_LAN)),
        )

        try {
            client.restorePairedSession("hub-1")
            fail("unsaved origin must be refused")
        } catch (e: ApiException) {
            assertEquals("unpaired_endpoint", e.code)
        }

        assertEquals(0, server.requestCount)
        assertEquals("tok-1", store.load()!!.deviceToken)
    }

    @Test
    fun `restore never sends the token when the probe reports another hub`() = runBlocking {
        seedPaired(endpoint)
        server.enqueue(healthResponse("some-other-hub"))

        try {
            client.restorePairedSession("hub-1")
            fail("probe mismatch must abort the restore")
        } catch (e: ApiException) {
            assertEquals("hub_mismatch", e.code)
        }

        val probe = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/health", probe.path)
        assertNull(probe.authorization())
        assertNull(probe.cookieHeader())
        assertEquals("only the probe may run", 1, server.requestCount)
        assertEquals("pairing must survive", "tok-1", store.load()!!.deviceToken)
    }

    @Test
    fun `restore rebuilds cookies csrf and limits and keeps the stored token`() = runBlocking {
        seedPaired(endpoint)
        server.enqueue(healthResponse("hub-1"))
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .addHeader("Content-Type", "application/json")
                .addHeader("Set-Cookie", "flowdrop_session=fresh; Path=/; HttpOnly; SameSite=Strict")
                .addHeader("Set-Cookie", "flowdrop_csrf=csrf-2; Path=/; SameSite=Strict")
                .setBody(
                    """{"authenticated":true,"csrf_token":"csrf-2","expires_at":9999999999999,
                       "limits":{"max_upload_bytes":2048},"device_id":"dev-1",
                       "hub":{"id":"hub-1","name":"Home",
                       "endpoints":[{"url":"http://192.168.1.50:8080","kind":"lan"},
                                    {"url":"http://hub.tailnet.ts.net:8080","kind":"tailscale"}]}}""",
                ),
        )

        val info = client.restorePairedSession("hub-1")

        assertTrue(info.authenticated)
        assertEquals("csrf-2", client.csrfToken)
        assertEquals(2_048L, client.maxUploadBytes)

        val probe = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("/api/v1/health", probe.path)
        assertNull(probe.cookieHeader())
        assertNull(probe.authorization())

        val session = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("GET", session.method)
        assertEquals("/api/v1/auth/session", session.path)
        assertEquals("Bearer tok-1", session.authorization())
        assertEquals(endpoint.origin, session.getHeader("Origin"))
        assertNull("stale cookies must be dropped before restoring", session.cookieHeader())
        assertNull("the token never goes into a header other than Authorization", session.getHeader("X-FlowDrop-CSRF"))

        val stored = store.load()!!
        assertEquals("csrf-2", stored.csrfToken)
        assertEquals("hub-1", stored.hubId)
        assertEquals("dev-1", stored.deviceId)
        assertEquals("GET session must not erase the device token", "tok-1", stored.deviceToken)
        assertTrue(stored.cookies.any { it.name == "flowdrop_session" && it.value == "fresh" })
        assertTrue(stored.candidates.any { it.url == "http://192.168.1.50:8080" && it.kind == "lan" })
        assertTrue(stored.candidates.any { it.url == endpoint.origin })
    }

    @Test
    fun `a session payload without device token keeps the stored one`() {
        seedPaired(endpoint)

        client.persistSession(
            SessionInfo(
                authenticated = true,
                csrfToken = "csrf-3",
                expiresAt = 42L,
                hub = HubInfo(id = "hub-1", name = "Home"),
            ),
        )

        val stored = store.load()!!
        assertEquals("tok-1", stored.deviceToken)
        assertEquals("dev-1", stored.deviceId)
        assertEquals("hub-1", stored.hubId)
        assertEquals("csrf-3", stored.csrfToken)
        assertEquals(42L, stored.expiresAt)
        assertTrue(stored.candidates.any { it.url == endpoint.origin })
    }

    // ---------- transport configuration ----------

    @Test
    fun `client never retries or follows redirects`() {
        assertFalse(client.http.retryOnConnectionFailure)
        assertFalse(client.http.followRedirects)
        assertFalse(client.http.followSslRedirects)
        assertNotNull(client.http.cookieJar)
    }
}
