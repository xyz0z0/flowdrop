package dev.flowdrop.android

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.ApiException
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.InMemorySecureStorage
import dev.flowdrop.android.data.PersistentCookieJar
import dev.flowdrop.android.data.SessionStore
import dev.flowdrop.android.io.UriSources
import kotlinx.coroutines.runBlocking
import okhttp3.Request
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
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
 *     -Pandroid.testInstrumentationRunnerArguments.hubUser=admin \
 *     -Pandroid.testInstrumentationRunnerArguments.hubPass=...
 *
 * When the arguments are absent the test is skipped, so CI without a Hub stays
 * green. It exercises the *real* frozen protocol on a real device: Cookie login,
 * CSRF double submit, Origin header, WS upgrade, upload/download bytes.
 */
@RunWith(AndroidJUnit4::class)
class LiveHubContractTest {

    private lateinit var endpoint: HubEndpoint
    private lateinit var store: SessionStore
    private lateinit var client: ApiClient
    private lateinit var password: String
    private lateinit var username: String

    private val args get() = InstrumentationRegistry.getArguments()

    @Before
    fun setUp() {
        val hub = args.getString("hubUrl").orEmpty()
        assumeTrue("no hubUrl instrumentation argument", hub.isNotBlank())
        endpoint = HubEndpoint.parse(hub) ?: run {
            assumeTrue("invalid hubUrl", false)
            error("unreachable")
        }
        username = args.getString("hubUser").orEmpty().ifBlank { "admin" }
        password = args.getString("hubPass").orEmpty()
        assumeTrue("no hubPass instrumentation argument", password.isNotBlank())
        store = SessionStore(InMemorySecureStorage())
        store.switchTo(endpoint)
        client = ApiClient(endpoint, store, PersistentCookieJar(store))
    }

    @After
    fun tearDown() {
        if (::client.isInitialized) {
            runCatching { runBlocking { client.logout() } }
        }
    }

    @Test
    fun loginSessionTextUploadDownloadLogout(): Unit = runBlocking {
        val info = client.login(username, password)
        assertTrue("login must authenticate", info.authenticated)
        assertTrue("login must return a CSRF token", client.csrfToken.isNotBlank())
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

        client.logout()
        try {
            client.list()
            error("expected 401 after logout")
        } catch (e: ApiException) {
            assertTrue(e.isUnauthorized)
        }
        file.delete()
    }

    @Test
    fun websocketReceivesItemCreatedWithSessionCookie(): Unit = runBlocking {
        client.login(username, password)
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

    private fun openSocket(listener: WebSocketListener): WebSocket {
        val request = Request.Builder()
            .url(endpoint.eventsUrl())
            .header("Origin", endpoint.origin)
            .build()
        return client.http.newWebSocket(request, listener)
    }
}
