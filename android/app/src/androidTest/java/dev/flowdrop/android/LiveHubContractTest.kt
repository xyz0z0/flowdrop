package dev.flowdrop.android

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.ApiException
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.HubEndpointInfo
import dev.flowdrop.android.data.HubProbe
import dev.flowdrop.android.data.InMemorySecureStorage
import dev.flowdrop.android.data.PersistentCookieJar
import dev.flowdrop.android.data.RouteCandidate
import dev.flowdrop.android.data.SessionStore
import dev.flowdrop.android.data.StoredSession
import dev.flowdrop.android.io.UriSources
import kotlinx.coroutines.runBlocking
import okhttp3.Request
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.FixMethodOrder
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.runners.MethodSorters
import java.io.ByteArrayOutputStream
import java.io.File
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/**
 * Optional live-Hub contract test.
 *
 * Enabled with instrumentation arguments:
 *
 *   ./gradlew :app:connectedDebugAndroidTest \
 *     -Pandroid.testInstrumentationRunnerArguments.hubUrl=http://192.168.1.50:8080 \
 *     -Pandroid.testInstrumentationRunnerArguments.hubPairingCode=0123
 *
 * `hubPairingCode` is the four-digit code shown on the Hub's settings screen. A
 * code is single use, so this class pairs exactly once ([sharedCredentials]) and
 * both tests then rebuild their cookie session from the resulting device token
 * with [ApiClient.restorePairedSession] — the code is never consumed twice.
 *
 * Instead of a code, `hubDeviceToken` may carry the token of an already paired
 * device; the Hub ID is then read from the credential-free `/api/v1/health`
 * probe. Neither secret is logged, printed or committed.
 *
 * When the arguments are absent the tests are skipped, so CI without a Hub stays
 * green, and the class still has to be run by hand. It exercises the *real*
 * frozen protocol on a real device: cookie session, CSRF double submit, Origin
 * header, WS upgrade, upload/download bytes.
 *
 * The logout test revokes the shared device, so it must run after the WebSocket
 * test; that is why the methods are numbered and sorted by name.
 */
@RunWith(AndroidJUnit4::class)
@FixMethodOrder(MethodSorters.NAME_ASCENDING)
class LiveHubContractTest {

    /** Durable identity of one test device, established once per class. */
    private data class Credentials(
        val hubId: String,
        val deviceId: String,
        val deviceToken: String,
        val candidates: List<HubEndpointInfo>,
    )

    private lateinit var endpoint: HubEndpoint
    private lateinit var store: SessionStore
    private lateinit var client: ApiClient

    private val args get() = InstrumentationRegistry.getArguments()

    @Before
    fun setUp(): Unit = runBlocking {
        val hub = args.getString("hubUrl").orEmpty()
        assumeTrue("no hubUrl instrumentation argument", hub.isNotBlank())
        endpoint = HubEndpoint.parse(hub) ?: run {
            assumeTrue("invalid hubUrl", false)
            error("unreachable")
        }
        val credentials = sharedCredentials()
        store = SessionStore(InMemorySecureStorage())
        store.switchTo(endpoint)
        store.save(
            StoredSession(
                baseUrl = endpoint.origin,
                origin = endpoint.origin,
                hubId = credentials.hubId,
                deviceId = credentials.deviceId,
                deviceToken = credentials.deviceToken,
                candidates = credentials.candidates,
            ),
        )
        client = ApiClient(endpoint, store, PersistentCookieJar(store))
        // Rebuild cookies/CSRF at the verified origin; the token is only ever
        // sent as the per-request Bearer credential of this call.
        client.restorePairedSession(credentials.hubId)
        Unit
    }

    /**
     * Pairs the single-use code once (or adopts `hubDeviceToken`) and remembers
     * the result so the second test does not need another code.
     */
    private suspend fun sharedCredentials(): Credentials = cachedCredentials ?: run {
        val presetToken = args.getString("hubDeviceToken").orEmpty()
        val code = args.getString("hubPairingCode").orEmpty()
        val established = if (presetToken.isNotBlank()) {
            val probed = HubProbe.hubId(RouteCandidate(endpoint.origin, RouteCandidate.KIND_LAN))
            assumeTrue("Hub /health did not expose hub_id", !probed.isNullOrBlank())
            Credentials(
                hubId = probed!!,
                deviceId = "",
                deviceToken = presetToken,
                candidates = listOf(HubEndpointInfo(endpoint.origin, RouteCandidate.KIND_LAN)),
            )
        } else {
            assumeTrue("needs hubPairingCode (one-time code) or hubDeviceToken", code.isNotBlank())
            val pairingStore = SessionStore(InMemorySecureStorage())
            pairingStore.switchTo(endpoint)
            val pairingClient = ApiClient(endpoint, pairingStore, PersistentCookieJar(pairingStore))
            pairingClient.pair(code, "AndroidTest")
            val paired = pairingStore.load() ?: error("pairing did not persist a record")
            check(paired.hubId.isNotBlank()) { "pair response did not return a Hub ID" }
            check(paired.deviceToken.isNotBlank()) { "pair response did not return a device token" }
            Credentials(
                hubId = paired.hubId,
                deviceId = paired.deviceId,
                deviceToken = paired.deviceToken,
                candidates = paired.candidates,
            )
        }
        cachedCredentials = established
        established
    }

    @Test
    fun test1WebsocketReceivesItemCreatedWithSessionCookie(): Unit = runBlocking {
        val received = AtomicReference<String>()
        val hello = CountDownLatch(1)
        val created = CountDownLatch(1)

        val socket = openSocket(object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                if (text.contains("\"hello\"")) hello.countDown()
                if (text.contains("item.created")) {
                    received.set(text)
                    created.countDown()
                }
            }
        })
        try {
            assertTrue("no hello frame", hello.await(10, TimeUnit.SECONDS))
            val marker = "ws-contract-" + System.currentTimeMillis()
            client.createText("text", marker, "AndroidTest")
            assertTrue("no item.created event", created.await(15, TimeUnit.SECONDS))
            assertTrue(received.get().contains("item.created"))
        } finally {
            socket.close(1000, "done")
        }
    }

    @Test
    fun test2SessionTextUploadDownloadLogout(): Unit = runBlocking {
        assertTrue("restore must produce a CSRF token", client.csrfToken.isNotBlank())
        assertTrue("Hub must report an upload limit", client.maxUploadBytes > 0)

        // Session restore uses the same cookie and returns the same token.
        val restored = client.session()
        assertTrue(restored.authenticated)
        assertEquals(client.csrfToken, restored.csrfToken)

        val marker = "android-contract-" + System.currentTimeMillis()
        val created = client.createText("text", marker, "AndroidTest")
        assertEquals("text", created.type)
        assertEquals(marker, created.text)

        // The item comes back through the list with the web-compatible shape.
        val page = client.list(limit = 30)
        assertTrue(page.items.any { it.id == created.id })

        // Upload a real file through the production streaming path.
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val bytes = ByteArray(200_000) { (it % 251).toByte() }
        val file = File(context.cacheDir, "contract-$marker.bin").apply { writeBytes(bytes) }
        val digest = MessageDigest.getInstance("SHA-256").digest(bytes)
            .joinToString("") { "%02x".format(it) }
        var uploadProgress = 0L
        val uploaded = client.upload(
            UriSources.fromFile(file),
            "AndroidTest",
        ) { loaded, _ -> uploadProgress = loaded }

        assertNotNull(uploaded.blob)
        assertEquals(bytes.size.toLong(), uploaded.blob!!.sizeBytes)
        assertEquals(digest, uploaded.blob!!.sha256)
        assertEquals(bytes.size.toLong(), uploadProgress)

        // Stream the same bytes back and verify integrity.
        val out = ByteArrayOutputStream()
        val result = client.download(uploaded.id, out) { _, _ -> }
        assertTrue("downloaded bytes must match", out.toByteArray().contentEquals(bytes))
        assertEquals(digest, result.sha256)

        // Deletion removes the item from the list.
        client.delete(created.id)
        val after = client.list(limit = 30)
        assertTrue(after.items.none { it.id == created.id })

        // Revokes the shared device (this test runs last), so the old cookie is
        // worthless: protected requests must now answer 401.
        client.logout()
        try {
            client.list()
            error("expected 401 after logout")
        } catch (e: ApiException) {
            assertTrue(e.isUnauthorized)
        }
        file.delete()
    }

    private fun openSocket(listener: WebSocketListener): WebSocket {
        val request = Request.Builder()
            .url(endpoint.eventsUrl())
            .header("Origin", endpoint.origin)
            .build()
        return client.http.newWebSocket(request, listener)
    }

    private companion object {
        /** Set once per instrumentation run; holds the single paired device. */
        @Volatile
        private var cachedCredentials: Credentials? = null
    }
}
