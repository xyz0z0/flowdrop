package dev.flowdrop.android.integration

import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.ApiException
import dev.flowdrop.android.data.CreateTextRequest
import dev.flowdrop.android.data.DownloadResult
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.HubProbe
import dev.flowdrop.android.data.HubRouteSelector
import dev.flowdrop.android.data.InMemorySecureStorage
import dev.flowdrop.android.data.Item
import dev.flowdrop.android.data.Page
import dev.flowdrop.android.data.PairRequest
import dev.flowdrop.android.data.PersistentCookieJar
import dev.flowdrop.android.data.RouteCandidate
import dev.flowdrop.android.data.SessionInfo
import dev.flowdrop.android.data.SessionStore
import dev.flowdrop.android.data.UploadSource
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import java.nio.file.Files
import java.security.MessageDigest
import java.util.Collections
import java.util.concurrent.TimeUnit
import java.util.zip.CRC32
import java.util.zip.Deflater
import java.util.zip.Inflater
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Live integration test: the real Android network layer against a real Hub.
 *
 * There is **no** MockWebServer here. The test runs the pinned `flowdrop` CLI
 * binary (SHA-256 pinned in [PINNED_BINARY_SHA256]) as its own child process
 * with a throw-away data directory and two concrete loopback listeners
 * (`127.0.0.1:<p>` and `127.0.0.2:<p2>`), because these are two distinct
 * origins that the Android stack really has to keep apart.
 *
 * Covered with the production classes only ([ApiClient], [SessionStore],
 * [InMemorySecureStorage], [PersistentCookieJar], [HubProbe],
 * [HubRouteSelector]):
 *
 * 1. one native pairing code -> POST /auth/pair(native=true) -> stored Hub ID,
 *    device token, CSRF value and **both** addresses the Hub actually bound;
 * 2. Android text / valid PNG / opaque file uploads, read back by a separate
 *    plain-HTTP "computer" client (browser-style cookie session) that compares
 *    the raw bytes and SHA-256;
 * 3. the reverse direction: the computer posts text and uploads a file, Android
 *    reads the item and downloads the bytes back with SHA-256 verification;
 * 4. cross-address recovery: [SessionStore.switchToPaired] then
 *    [ApiClient.restorePairedSession] rebuilds a real cookie/CSRF session at the
 *    second origin from `Authorization: Bearer` on GET /auth/session, with no
 *    write replayed and no cookie shared across origins;
 * 5. a real health mismatch: a *different* real Hub answers on that address, so
 *    the credential-free probe returns a foreign `hub_id` and the client refuses
 *    before sending any Bearer token, keeping the pairing record;
 * 6. revocation: [ApiClient.logout] kills the token at the second address
 *    (GET /auth/session -> authenticated:false, protected API -> 401).
 *
 * Deliberate limits, not hidden claims:
 * - loopback origins are classified `other` by the Hub, not `lan`/`tailscale`,
 *   so the LAN -> Tailscale failover is simulated by an *injected* probe that
 *   fails only the first candidate; there is no real Tailscale or LAN outage
 *   behind it (called out where it happens);
 * - `InMemorySecureStorage` stands in for the Android Keystore, so this says
 *   nothing about real-device key protection;
 * - no UI, emulator or instrumented test is involved.
 *
 * Enabled only through the environment variable `FLOWDROP_LIVE_TEST_BIN`; the
 * binary is executed in place and never copied or modified.
 */
class RealHubPairingTest {

    /**
     * The whole story runs serially on one Hub and one native pairing on
     * purpose: pairing codes are single use and a newly generated code voids
     * the previous one, so parallel cases would invalidate each other.
     */
    @Test(timeout = 300_000)
    fun `real Hub native pair, two-way transfer, cross-address restore and revoke`() {
        val binaryPath = System.getenv(ENV_LIVE_TEST_BIN)
        assumeTrue(
            "set $ENV_LIVE_TEST_BIN to the built flowdrop binary to run this live Hub test",
            !binaryPath.isNullOrBlank(),
        )
        val binary = File(binaryPath!!).absoluteFile
        assertTrue("binary not found: $binary", binary.isFile)
        assertEquals(
            "binary is not the pinned artefact: $binary",
            PINNED_BINARY_SHA256,
            sha256Hex(binary.readBytes()),
        )

        val workspace = RealHubWorkspace(realTempRoot(), binary)
        val wire = RealHubWireRecorder()
        try {
            val hub = workspace.startHub(workspace.dataDirA, workspace.logA)
            try {
                val hubId = hub.hubId()
                val endpointA = hub.endpointA
                val endpointB = hub.endpointB

                // The identity probe is the production HubProbe on a real socket.
                assertEquals(
                    "127.0.0.1 listener must report the Hub ID",
                    hubId,
                    runBlocking { HubProbe.hubId(RouteCandidate(endpointA.origin, RouteCandidate.KIND_OTHER)) },
                )
                assertEquals(
                    "127.0.0.2 listener must report the same Hub ID",
                    hubId,
                    runBlocking { HubProbe.hubId(RouteCandidate(endpointB.origin, RouteCandidate.KIND_OTHER)) },
                )
                evidence("hub", "origins=${endpointA.origin},${endpointB.origin} hub_id=$hubId")

                // ---------- 1. one native pairing ----------
                val store = SessionStore(InMemorySecureStorage())
                store.switchTo(endpointA)
                val jar = PersistentCookieJar(store)
                val clientA = ApiClient(endpointA, store, jar, wire.client)

                val pairCode = workspace.newPairingCode(workspace.dataDirA)
                val paired = runBlocking { clientA.pair(pairCode, "PA-08I Android") }
                assertTrue("pairing must authenticate", paired.authenticated)
                val deviceToken = paired.deviceToken
                assertNotNull("native pair must return a device token once", deviceToken)
                assertTrue("device token must not be blank", !deviceToken!!.isBlank())

                val record = store.load()!!
                assertEquals("stored Hub ID", hubId, record.hubId)
                assertEquals("stored device token", deviceToken, record.deviceToken)
                assertEquals("stored device id", paired.deviceId, record.deviceId)
                assertEquals("stored origin", endpointA.origin, record.origin)
                assertTrue("pairing must persist a CSRF value", record.csrfToken.isNotBlank())
                assertEquals(
                    "CSRF value must match the CSRF cookie",
                    record.csrfToken,
                    record.cookies.first { it.name == CSRF_COOKIE }.value,
                )
                assertTrue(
                    "pairing must persist the session cookie",
                    record.cookies.any { it.name == SESSION_COOKIE },
                )
                assertFalse(
                    "the pairing code must never be stored",
                    record.deviceToken == pairCode || record.csrfToken == pairCode,
                )

                // Metadata must name both addresses the Hub actually bound.
                assertEquals(
                    "saved candidates must be exactly the two real origins",
                    setOf(endpointA.origin, endpointB.origin),
                    record.candidates.map { HubEndpoint.parse(it.url)?.origin }.toSet(),
                )
                assertEquals("candidate count", 2L, record.candidates.size.toLong())
                for (candidate in record.candidates) {
                    assertTrue(
                        "kind must be a contract value: ${candidate.kind}",
                        candidate.kind in setOf(
                            RouteCandidate.KIND_LAN,
                            RouteCandidate.KIND_TAILSCALE,
                            RouteCandidate.KIND_OTHER,
                        ),
                    )
                }
                // Loopback is neither LAN nor Tailscale; saying so keeps the
                // failover simulation below honest.
                assertEquals(
                    setOf(RouteCandidate.KIND_OTHER),
                    record.candidates.map { it.kind }.toSet(),
                )

                val pairCalls = wire.records { it.path == "/api/v1/auth/pair" }
                assertEquals("exactly one native pair request", 1L, pairCalls.size.toLong())
                assertEquals("pair request origin", endpointA.origin, pairCalls.single().origin)
                assertFalse("pairing must not send a bearer token", pairCalls.single().hasBearer)
                assertFalse("pairing must not send a CSRF header", pairCalls.single().hasCsrfHeader)
                evidence(
                    "pair:android",
                    "origin=${endpointA.origin} device_id=${paired.deviceId} " +
                        "hub_id=${record.hubId} token_persisted=true " +
                        "candidates=${record.candidates.joinToString(",") { "${it.url}(${it.kind})" }}",
                )

                // ---------- 2. the paired session works over real HTTP ----------
                val sessionA = runBlocking { clientA.session() }
                assertTrue(sessionA.authenticated)
                assertEquals(hubId, sessionA.hub?.id)
                assertEquals(paired.deviceId, sessionA.deviceId)

                // A second, browser-style client stands in for "the computer":
                // it pairs with native=false, so it gets cookies and no token.
                val computerCode = workspace.newPairingCode(workspace.dataDirA)
                val computer = RealHubComputerClient(endpointA.origin, wire.client)
                val computerSession = computer.pair(computerCode, "PA-08I Computer")
                assertTrue(computerSession.authenticated)
                assertNull("a browser-style pair must not receive a device token", computerSession.deviceToken)
                assertEquals(
                    "computer session CSRF must come from the Hub",
                    computerSession.csrfToken,
                    computer.csrfToken(),
                )
                assertEquals(
                    "the computer must be a second device",
                    true,
                    computerSession.deviceId != paired.deviceId,
                )

                // ---------- 3. Android writes, the computer verifies bytes ----------
                val marker = "PA-08I android text 中文 $hubId"
                val androidText = runBlocking { clientA.createText("text", marker, "android-pa08i") }
                assertEquals(marker, androidText.text)

                val readByComputer = computer.getItem(androidText.id)
                assertEquals("computer must read the exact text", marker, readByComputer.text)
                assertEquals("android-pa08i", readByComputer.senderDevice)
                assertEquals(
                    "text bytes must be identical over the wire",
                    sha256Hex(marker.toByteArray(Charsets.UTF_8)),
                    sha256Hex(readByComputer.text!!.toByteArray(Charsets.UTF_8)),
                )
                evidence(
                    "text:android->hub->computer",
                    "item=${androidText.id} bytes=${marker.toByteArray(Charsets.UTF_8).size} " +
                        "sha=${sha256Hex(marker.toByteArray(Charsets.UTF_8))} origin=${endpointA.origin}",
                )

                val pngBytes = realPngBytes()
                val pngItem = runBlocking {
                    uploadFromAndroid(clientA, "pa08i-valid.png", "image/png", pngBytes)
                }
                assertEquals("Hub must classify the PNG as an image", "image", pngItem.type)
                assertTrue(pngItem.isImage)
                assertEquals("Hub-computed PNG SHA", sha256Hex(pngBytes), pngItem.blob!!.sha256)
                assertEquals(pngBytes.size.toLong(), pngItem.blob!!.sizeBytes)

                val pngReadBack = computer.download(pngItem.id)
                assertArrayEquals("PNG bytes must survive the Hub unchanged", pngBytes, pngReadBack.bytes)
                assertEquals("download header SHA", sha256Hex(pngBytes), pngReadBack.headerSha256)
                assertEquals(sha256Hex(pngBytes), sha256Hex(pngReadBack.bytes))
                evidence(
                    "png:android->hub->computer",
                    "item=${pngItem.id} bytes=${pngBytes.size} sha=${sha256Hex(pngBytes)} " +
                        "download=${endpointA.origin}/api/v1/items/${pngItem.id}/download",
                )

                val fileBytes = deterministicBytes(96 * 1024 + 13, seed = 0x50A08L)
                val fileItem = runBlocking {
                    uploadFromAndroid(clientA, "pa08i-opaque.bin", "application/octet-stream", fileBytes)
                }
                assertEquals("Hub must keep the opaque file opaque", "file", fileItem.type)
                assertEquals(sha256Hex(fileBytes), fileItem.blob!!.sha256)
                assertEquals(fileBytes.size.toLong(), fileItem.blob!!.sizeBytes)

                val fileReadBack = computer.download(fileItem.id)
                assertArrayEquals("file bytes must survive the Hub unchanged", fileBytes, fileReadBack.bytes)
                assertEquals(sha256Hex(fileBytes), fileReadBack.headerSha256)
                evidence(
                    "file:android->hub->computer",
                    "item=${fileItem.id} bytes=${fileBytes.size} sha=${sha256Hex(fileBytes)} " +
                        "download=${endpointA.origin}/api/v1/items/${fileItem.id}/download",
                )

                // The writes really carried Origin + CSRF; the Hub rejects them otherwise.
                val androidUploads = wire.records { it.method == "POST" && it.path == "/api/v1/items/upload" }
                assertEquals("both Android uploads must be on the wire", 2L, androidUploads.size.toLong())
                for (upload in androidUploads) {
                    assertEquals("upload origin", endpointA.origin, upload.origin)
                    assertTrue("upload must send the CSRF header", upload.hasCsrfHeader)
                    assertFalse("uploads must not send a bearer token", upload.hasBearer)
                }

                // ---------- 4. the computer writes, Android verifies bytes ----------
                val computerText = "PA-08I computer text 反向 $hubId"
                val computerItem = computer.postText(computerText, "computer-pa08i")
                assertEquals(computerText, computerItem.text)

                val androidPage: Page = runBlocking { clientA.list(limit = 50) }
                assertTrue(
                    "Android must see the item the computer created",
                    androidPage.items.any { it.id == computerItem.id },
                )
                assertEquals(computerText, runBlocking { clientA.getItem(computerItem.id) }.text)

                val computerBytes = deterministicBytes(64 * 1024 + 7, seed = 0x0BEEFL)
                val computerUpload = computer.upload("pa08i-from-computer.bin", computerBytes, "computer-pa08i")
                assertEquals(sha256Hex(computerBytes), computerUpload.blob!!.sha256)

                val sink = ByteArrayOutputStream()
                val androidDownload: DownloadResult = runBlocking {
                    clientA.download(computerUpload.id, sink) { _, _ -> }
                }
                assertArrayEquals(
                    "Android must download the computer's bytes unchanged",
                    computerBytes,
                    sink.toByteArray(),
                )
                assertEquals(sha256Hex(computerBytes), androidDownload.sha256)
                assertEquals(computerBytes.size.toLong(), androidDownload.bytes)
                evidence(
                    "file:computer->hub->android",
                    "item=${computerUpload.id} bytes=${computerBytes.size} " +
                        "sha=${sha256Hex(computerBytes)} verified_by=ApiClient.download",
                )

                val androidItemIds = runBlocking { clientA.list(limit = 50) }.items.map { it.id }.toSet()
                val computerItemIds = computer.list().items.map { it.id }.toSet()
                assertEquals("both clients must see the same inbox", computerItemIds, androidItemIds)

                // ---------- 5. cross-address: switch, then restore at origin B ----------
                assertTrue(
                    "switchToPaired must accept the saved second address",
                    store.switchToPaired(endpointB, hubId),
                )
                val switched = store.load()!!
                assertEquals("active origin after switch", endpointB.origin, switched.origin)
                assertEquals("pairing must survive the switch", hubId, switched.hubId)
                assertEquals("device token must survive the switch", deviceToken, switched.deviceToken)
                assertTrue("cookies must be dropped on switch", switched.cookies.isEmpty())
                assertTrue("CSRF must be dropped on switch", switched.csrfToken.isEmpty())

                val writesToBBefore = wire.writeCount(endpointB.origin)
                val recordsToBBefore = wire.records { it.origin == endpointB.origin }.size

                val clientB = ApiClient(endpointB, store, jar, wire.client)
                val restored: SessionInfo = runBlocking { clientB.restorePairedSession(hubId) }
                assertTrue("restore must authenticate", restored.authenticated)
                assertEquals("restore keeps the device identity", paired.deviceId, restored.deviceId)
                assertEquals("restore reports the same Hub", hubId, restored.hub?.id)
                assertNull("restore must not echo the device token", restored.deviceToken)

                val restoredRecord = store.load()!!
                assertEquals("restored origin", endpointB.origin, restoredRecord.origin)
                assertEquals("restored Hub ID", hubId, restoredRecord.hubId)
                assertEquals("restored token must be the stored one", deviceToken, restoredRecord.deviceToken)
                assertTrue("restore must rebuild the CSRF value", restoredRecord.csrfToken.isNotBlank())
                assertEquals(
                    "restored CSRF must match the restored cookie",
                    restoredRecord.csrfToken,
                    restoredRecord.cookies.first { it.name == CSRF_COOKIE }.value,
                )
                assertTrue(
                    "restore must rebuild the session cookie at the second origin",
                    restoredRecord.cookies.any { it.name == SESSION_COOKIE },
                )
                assertTrue(
                    "second-origin cookies must belong to the second origin",
                    restoredRecord.cookies.all { it.host == HOST_B },
                )

                val restoreRecords = wire.records { it.origin == endpointB.origin }.drop(recordsToBBefore)
                assertEquals("restore must be exactly one HTTP request", 1L, restoreRecords.size.toLong())
                assertEquals("GET", restoreRecords.single().method)
                assertEquals("/api/v1/auth/session", restoreRecords.single().path)
                assertTrue("restore must authenticate with Bearer", restoreRecords.single().hasBearer)
                assertEquals(
                    "the Bearer token must be the stored device token",
                    deviceToken,
                    restoreRecords.single().bearer,
                )
                assertEquals(
                    "restore must not replay any write",
                    writesToBBefore,
                    wire.writeCount(endpointB.origin),
                )
                assertEquals(
                    "a Bearer token must only ever be sent to the verified origin",
                    setOf(endpointB.origin),
                    wire.records { it.hasBearer }.map { it.origin }.toSet(),
                )
                evidence(
                    "restore:cross-address",
                    "from=${endpointA.origin} to=${endpointB.origin} " +
                        "requests=${restoreRecords.joinToString(",") { it.method + " " + it.path }} " +
                        "writes_replayed=${wire.writeCount(endpointB.origin) - writesToBBefore}",
                )

                // Same content served by the second address.
                val contentAtB = runBlocking { clientB.list(limit = 50) }
                assertEquals(
                    "the second address must serve the same inbox",
                    computerItemIds,
                    contentAtB.items.map { it.id }.toSet(),
                )
                assertEquals(marker, runBlocking { clientB.getItem(androidText.id) }.text)

                // ---------- 6. cross-address failover simulation ----------
                // Injected probe: only the first candidate fails. The second probe
                // is the real HubProbe against the real 127.0.0.2 listener. This
                // models "LAN unreachable, second address still up" without
                // touching the host network stack; it is NOT a real LAN outage and
                // NOT Tailscale.
                val candidates = restoredRecord.candidates.map { it.toRouteCandidate() }
                assertEquals(
                    "first candidate must be the primary address",
                    endpointA.origin,
                    candidates.first().url,
                )
                val injected = HubRouteSelector(probe = { candidate ->
                    if (candidate.url == endpointA.origin) null else HubProbe.hubId(candidate)
                })
                val selected = runBlocking { injected.select(hubId, candidates) }
                assertNotNull("selector must fall through to the second address", selected)
                assertEquals("failover target", endpointB.origin, selected!!.url)
                assertEquals(
                    "the surviving probe must report the real Hub ID",
                    hubId,
                    runBlocking {
                        HubProbe.hubId(RouteCandidate(endpointB.origin, RouteCandidate.KIND_TAILSCALE))
                    },
                )
                // A genuinely dead address (a free port that was never bound) is
                // unavailable to the real probe.
                val deadOrigin = "http://127.0.0.1:${workspace.unusedPortForDeadProbe()}"
                assertNull(
                    "a dead address must not report a Hub ID",
                    runBlocking { HubProbe.hubId(RouteCandidate(deadOrigin, RouteCandidate.KIND_LAN)) },
                )
                evidence(
                    "failover:simulated",
                    "injected_probe_failed=${endpointA.origin} real_probe_ok=${endpointB.origin} " +
                        "selected=${selected.url} dead_probe=$deadOrigin -> null " +
                        "(loopback simulation, not a real LAN/Tailscale outage)",
                )

                // ---------- 7. real health mismatch: a foreign Hub on that address ----------
                hub.stop()
                val impostor = workspace.startHub(workspace.dataDirB, workspace.logB)
                try {
                    val foreignHubId = impostor.hubId()
                    assertNotEquals("the impostor must be a different Hub", hubId, foreignHubId)
                    // Real, credential-free evidence: the address now answers with
                    // another Hub ID, so an "expected ID" probe genuinely mismatches.
                    assertEquals(
                        "the mismatched address really reports another Hub ID",
                        foreignHubId,
                        runBlocking {
                            HubProbe.hubId(RouteCandidate(endpointB.origin, RouteCandidate.KIND_OTHER))
                        },
                    )

                    val beforeMismatch = store.load()!!
                    val recordsBeforeMismatch = wire.records { it.origin == endpointB.origin }.size
                    val bearerBeforeMismatch = wire.records { it.hasBearer }.size
                    val mismatchClient = ApiClient(endpointB, store, jar, wire.client)
                    val failure = try {
                        runBlocking { mismatchClient.restorePairedSession(hubId) }
                        null
                    } catch (e: ApiException) {
                        e
                    }
                    assertNotNull("a foreign Hub ID must abort the restore", failure)
                    assertEquals("hub_mismatch", failure!!.code)

                    assertEquals(
                        "a mismatched Hub must not receive a single request carrying credentials",
                        recordsBeforeMismatch,
                        wire.records { it.origin == endpointB.origin }.size,
                    )
                    assertEquals(
                        "no additional bearer request may follow a Hub ID mismatch",
                        bearerBeforeMismatch,
                        wire.records { it.hasBearer }.size,
                    )
                    val keptRecord = store.load()!!
                    assertEquals("mismatch must keep the pairing", beforeMismatch, keptRecord)
                    assertEquals(hubId, keptRecord.hubId)
                    assertEquals(deviceToken, keptRecord.deviceToken)
                    assertEquals(endpointB.origin, keptRecord.origin)
                    evidence(
                        "hub-id-mismatch",
                        "expected=$hubId real_health=$foreignHubId -> ApiException ${failure.code}, " +
                            "requests_to_origin_after_attempt=0, record_preserved=true",
                    )
                } finally {
                    impostor.stop()
                }

                // Same Hub back on the same addresses: the pairing still restores.
                workspace.startHub(workspace.dataDirA, workspace.logA, reuse = hub)
                // A Hub restart kills the kept-alive socket. ApiClient sets
                // retryOnConnectionFailure(false) on purpose (a retried write
                // could replay), so the first request after a restart can fail
                // with IOException instead of being retried internally: record
                // that, then retry exactly like the UI does after "connection
                // failed" (a fresh process/pool).
                var stalePoolFailure: Throwable? = null
                try {
                    runBlocking { ApiClient(endpointB, store, jar, wire.client).restorePairedSession(hubId) }
                } catch (e: java.io.IOException) {
                    stalePoolFailure = e
                    wire.client.connectionPool.evictAll()
                }
                evidence(
                    "restart-stale-connection",
                    "stale_pool_ioexception=${stalePoolFailure != null} " +
                        "retried_after_pool_reset=true (ApiClient forbids internal retries by design)",
                )
                val clientBAgain = ApiClient(endpointB, store, jar, wire.client)
                val restoredAgain = runBlocking { clientBAgain.restorePairedSession(hubId) }
                assertTrue(restoredAgain.authenticated)
                assertEquals(hubId, runBlocking { clientBAgain.session() }.hub?.id)
                assertEquals(
                    "content must be intact after the refused cross-address attempt",
                    computerItemIds,
                    runBlocking { clientBAgain.list(limit = 50) }.items.map { it.id }.toSet(),
                )

                // ---------- 8. revocation through the Android logout ----------
                runBlocking { clientBAgain.logout() }
                assertNull("logout must drop the local record", store.load())

                val anonymous = anonymousClient()
                try {
                    val sessionAfterLogout = anonymous.get("${endpointB.origin}/api/v1/auth/session")
                    try {
                        assertEquals("session probe status", 200, sessionAfterLogout.code)
                        val parsed = SESSION_JSON.decodeFromString(
                            SessionInfo.serializer(),
                            sessionAfterLogout.body?.string().orEmpty(),
                        )
                        assertFalse("the revoked token must not authenticate", parsed.authenticated)
                    } finally {
                        sessionAfterLogout.close()
                    }

                    val protectedAfterLogout = anonymous.get(
                        "${endpointB.origin}/api/v1/items?limit=5",
                        "Authorization" to "Bearer $deviceToken",
                    )
                    try {
                        assertEquals("protected API must reject the revoked token", 401, protectedAfterLogout.code)
                    } finally {
                        protectedAfterLogout.close()
                    }
                } finally {
                    anonymous.connectionPool.evictAll()
                    anonymous.dispatcher.executorService.shutdown()
                }

                // The separately paired computer is untouched: revocation is per device.
                assertTrue(
                    "another paired device must keep working",
                    computer.sessionInfo().authenticated,
                )
                evidence(
                    "revocation",
                    "origin=${endpointB.origin} session_authenticated=false items=401 " +
                        "other_device_still_paired=true",
                )
            } finally {
                hub.stop()
                System.err.println("[PA-08I] cleanup: hub process alive=${!hub.isStopped()} " +
                    "log=${workspace.logA.name}")
            }
        } finally {
            wire.close()
            workspace.close()
            System.err.println("[PA-08I] cleanup: temp dir removed=${workspace.isRemoved()}")
        }
    }

    private suspend fun uploadFromAndroid(
        client: ApiClient,
        name: String,
        contentType: String,
        bytes: ByteArray,
    ): Item {
        val progress = ArrayList<Long>()
        val item = client.upload(
            UploadSource(name = name, size = bytes.size.toLong(), contentType = contentType) {
                ByteArrayInputStream(bytes)
            },
            senderDevice = "android-pa08i",
            onProgress = { sent, _ -> progress.add(sent) },
        )
        assertTrue("progress must be reported", progress.isNotEmpty())
        assertEquals("progress must end at the full size", bytes.size.toLong(), progress.last())
        return item
    }

    private companion object {
        const val ENV_LIVE_TEST_BIN = "FLOWDROP_LIVE_TEST_BIN"

        /** dist/flowdrop from the PA-09L release build. */
        const val PINNED_BINARY_SHA256 =
            "48eda3d93639a1f6720392d51ea1a2532e9ee6e693fa2b246a393985d76c50b6"

        const val SESSION_COOKIE = "flowdrop_session"
        const val CSRF_COOKIE = "flowdrop_csrf"
        const val HOST_B = "127.0.0.2"

        val SESSION_JSON = Json { ignoreUnknownKeys = true }

        fun evidence(tag: String, detail: String) {
            // No credential, token or pairing code is ever printed.
            println("[PA-08I][$tag] $detail")
        }
    }
}

// ---------------------------------------------------------------------------
// test helpers (same "RealHub" prefix, test-only, real sockets only)
// ---------------------------------------------------------------------------

/** One real HTTP request as it left a test client. */
private data class RealHubWireCall(
    val method: String,
    val origin: String,
    val path: String,
    val hasBearer: Boolean,
    val bearer: String?,
    val hasCsrfHeader: Boolean,
    val originHeader: String?,
)

/** Records every request the production clients hand to OkHttp (real sockets). */
private class RealHubWireRecorder {
    private val calls = Collections.synchronizedList(ArrayList<RealHubWireCall>())

    val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .writeTimeout(20, TimeUnit.SECONDS)
        .callTimeout(30, TimeUnit.SECONDS)
        .retryOnConnectionFailure(false)
        .followRedirects(false)
        .followSslRedirects(false)
        .addInterceptor { chain ->
            val request = chain.request()
            val authorization = request.header("Authorization").orEmpty()
            val bearer = authorization.removePrefix("Bearer ")
            calls.add(
                RealHubWireCall(
                    method = request.method,
                    origin = HubEndpoint.originOf(request.url),
                    path = request.url.encodedPath,
                    hasBearer = authorization.startsWith("Bearer ") && bearer.isNotEmpty(),
                    bearer = bearer.takeIf { authorization.startsWith("Bearer ") },
                    hasCsrfHeader = !request.header("X-FlowDrop-CSRF").isNullOrEmpty(),
                    originHeader = request.header("Origin"),
                ),
            )
            chain.proceed(request)
        }
        .build()

    fun records(predicate: (RealHubWireCall) -> Boolean): List<RealHubWireCall> =
        synchronized(calls) { calls.filter(predicate) }

    fun writeCount(origin: String): Int =
        records { it.origin == origin && it.method in setOf("POST", "PUT", "PATCH", "DELETE") }.size

    fun close() {
        client.connectionPool.evictAll()
        client.dispatcher.executorService.shutdown()
    }
}

/**
 * The "computer": plain OkHttp, a cookie jar and no Android code — exactly what
 * a browser does on the frozen contract (pair with native=false, talk with
 * cookies plus `X-FlowDrop-CSRF`, download bytes).
 */
private class RealHubComputerClient(
    private val origin: String,
    base: OkHttpClient,
) {
    private val jar = RealHubCookieJar()
    private val http = base.newBuilder().cookieJar(jar).build()
    private val json = Json { ignoreUnknownKeys = true }
    private var csrf: String = ""

    fun csrfToken(): String = csrf

    fun pair(code: String, deviceName: String): SessionInfo {
        val payload = json.encodeToString(
            PairRequest.serializer(),
            PairRequest(code = code, deviceName = deviceName, native = false),
        )
        val request = Request.Builder()
            .url("$origin/api/v1/auth/pair")
            .header("Origin", origin)
            .header("Content-Type", "application/json")
            .post(payload.toRequestBody(JSON_MEDIA))
            .build()
        http.newCall(request).execute().use { response ->
            val text = response.body?.string().orEmpty()
            if (!response.isSuccessful) fail("computer pairing failed: ${response.code} $text")
            val info = json.decodeFromString(SessionInfo.serializer(), text)
            info.csrfToken?.let { csrf = it }
            if (csrf.isEmpty()) csrf = jar.cookieValue(CSRF_COOKIE_NAME).orEmpty()
            return info
        }
    }

    fun sessionInfo(): SessionInfo = http.newCall(
        Request.Builder().url("$origin/api/v1/auth/session").get().build(),
    ).execute().use { response ->
        json.decodeFromString(SessionInfo.serializer(), response.body?.string().orEmpty())
    }

    fun list(): Page = http.newCall(
        Request.Builder().url("$origin/api/v1/items?limit=50").get().build(),
    ).execute().use { response ->
        require(response.isSuccessful) { "computer list failed: ${response.code}" }
        json.decodeFromString(Page.serializer(), response.body?.string().orEmpty())
    }

    fun getItem(id: String): Item = http.newCall(
        Request.Builder().url("$origin/api/v1/items/$id").get().build(),
    ).execute().use { response ->
        require(response.isSuccessful) { "computer get item failed: ${response.code}" }
        json.decodeFromString(Item.serializer(), response.body?.string().orEmpty())
    }

    fun postText(text: String, senderDevice: String): Item {
        val payload = json.encodeToString(
            CreateTextRequest.serializer(),
            CreateTextRequest(type = "text", text = text, senderDevice = senderDevice),
        )
        val request = Request.Builder()
            .url("$origin/api/v1/items/text")
            .header("Origin", origin)
            .header("X-FlowDrop-CSRF", csrf)
            .post(payload.toRequestBody(JSON_MEDIA))
            .build()
        http.newCall(request).execute().use { response ->
            require(response.isSuccessful) { "computer post text failed: ${response.code}" }
            return json.decodeFromString(Item.serializer(), response.body?.string().orEmpty())
        }
    }

    fun upload(name: String, bytes: ByteArray, senderDevice: String): Item {
        val body = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("sender_device", senderDevice)
            .addFormDataPart("file", name, bytes.toRequestBody(OCTET_MEDIA))
            .build()
        val request = Request.Builder()
            .url("$origin/api/v1/items/upload")
            .header("Origin", origin)
            .header("X-FlowDrop-CSRF", csrf)
            .post(body)
            .build()
        http.newCall(request).execute().use { response ->
            require(response.isSuccessful) { "computer upload failed: ${response.code}" }
            return json.decodeFromString(Item.serializer(), response.body?.string().orEmpty())
        }
    }

    fun download(id: String): RealHubDownload = http.newCall(
        Request.Builder().url("$origin/api/v1/items/$id/download").get().build(),
    ).execute().use { response ->
        require(response.isSuccessful) { "computer download failed: ${response.code}" }
        RealHubDownload(
            bytes = response.body?.bytes() ?: ByteArray(0),
            headerSha256 = response.header("X-Content-SHA256").orEmpty(),
        )
    }

    private companion object {
        const val CSRF_COOKIE_NAME = "flowdrop_csrf"
        val JSON_MEDIA = "application/json; charset=utf-8".toMediaType()
        val OCTET_MEDIA = "application/octet-stream".toMediaType()
    }
}

private data class RealHubDownload(val bytes: ByteArray, val headerSha256: String)

/** Minimal browser-equivalent cookie jar: per-origin, never persisted. */
private class RealHubCookieJar : CookieJar {
    private val cookies = ArrayList<Cookie>()

    @Synchronized
    override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        for (cookie in cookies) {
            this.cookies.removeAll {
                it.name == cookie.name && it.domain == cookie.domain && it.path == cookie.path
            }
            if (cookie.expiresAt > System.currentTimeMillis()) this.cookies.add(cookie)
        }
    }

    @Synchronized
    override fun loadForRequest(url: HttpUrl): List<Cookie> {
        val now = System.currentTimeMillis()
        cookies.removeAll { it.expiresAt <= now }
        return cookies.filter { it.matches(url) }
    }

    @Synchronized
    fun cookieValue(name: String): String? = cookies.firstOrNull { it.name == name }?.value
}

/** A real `flowdrop` child process plus the two loopback origins it binds. */
private class RealHubProcess(
    private val binary: File,
    private val configDir: File,
    private val env: Map<String, String>,
    private val logFile: File,
    private val primaryPort: Int,
    private val secondaryPort: Int,
) {
    val endpointA: HubEndpoint = HubEndpoint.parse("http://127.0.0.1:$primaryPort")!!
    val endpointB: HubEndpoint = HubEndpoint.parse("http://127.0.0.2:$secondaryPort")!!

    private var process: Process? = null
    private var hubIdValue: String = ""

    fun hubId(): String = hubIdValue

    /** Starts `flowdrop serve` and waits (deadline bound) until both origins are healthy. */
    fun start(): RealHubProcess {
        check(process == null) { "hub already running" }
        var attempts = 0
        while (true) {
            attempts++
            val child = launch()
            val deadline = System.currentTimeMillis() + HEALTH_DEADLINE_MS
            var lastSeen = "no hub_id"
            while (System.currentTimeMillis() < deadline) {
                if (!child.isAlive) break
                val reportedA = runBlocking {
                    HubProbe.hubId(RouteCandidate(endpointA.origin, RouteCandidate.KIND_OTHER))
                }
                if (reportedA != null) {
                    val reportedB = runBlocking {
                        HubProbe.hubId(RouteCandidate(endpointB.origin, RouteCandidate.KIND_OTHER))
                    }
                    if (reportedB == reportedA) {
                        hubIdValue = reportedA
                        return this
                    }
                    lastSeen = "primary=$reportedA secondary=$reportedB"
                }
                Thread.sleep(100)
            }
            val exited = !child.isAlive
            val exitCode = if (exited) child.exitValue() else null
            release(child)
            if (exited && attempts < START_ATTEMPTS) {
                // A port can briefly refuse a rebind right after the previous Hub
                // instance released it; retrying is cheap and deterministic.
                Thread.sleep(200)
                continue
            }
            throw AssertionError(
                "hub did not become healthy (attempts=$attempts exited=$exited code=$exitCode " +
                    "seen=$lastSeen); log: ${logTail()}",
            )
        }
    }

    fun stop() {
        val child = process ?: return
        process = null
        release(child)
    }

    fun isStopped(): Boolean = process?.isAlive != true

    private fun launch(): Process {
        val builder = ProcessBuilder(
            binary.absolutePath,
            "serve",
            "-config",
            File(configDir, "absent-config.json").absolutePath,
        )
        builder.redirectErrorStream(true)
        builder.redirectOutput(ProcessBuilder.Redirect.appendTo(logFile))
        builder.environment().apply {
            clear()
            putAll(env)
        }
        val child = builder.start()
        process = child
        return child
    }

    private fun release(child: Process) {
        child.destroy()
        if (!child.waitFor(5, TimeUnit.SECONDS)) {
            child.destroyForcibly()
            child.waitFor(5, TimeUnit.SECONDS)
        }
    }

    private fun logTail(): String = runCatching {
        logFile.readLines().takeLast(12).joinToString(" | ")
    }.getOrDefault("<no log>")

    private companion object {
        const val HEALTH_DEADLINE_MS = 10_000L
        const val START_ATTEMPTS = 3
    }
}

/**
 * Owns the throw-away directories, the port choice, the child environment and
 * the cleanup of one live-Hub run. Nothing here touches the user's data
 * directory, configuration, port 8080 or the host network configuration.
 */
private class RealHubWorkspace(private val root: File, private val binary: File) {
    val dataDirA: File = File(root, "data-a")
    val dataDirB: File = File(root, "data-b")
    val logA: File = File(root, "hub-a.log")
    val logB: File = File(root, "hub-b.log")

    private val primaryPort = reservePort("127.0.0.1")
    private val secondaryPort = reservePort("127.0.0.2")

    init {
        check(primaryPort != secondaryPort) { "port reservation collided" }
        require(dataDirA.mkdirs() || dataDirA.isDirectory) { "cannot create $dataDirA" }
        require(dataDirB.mkdirs() || dataDirB.isDirectory) { "cannot create $dataDirB" }
    }

    fun startHub(dataDir: File, logFile: File, reuse: RealHubProcess? = null): RealHubProcess {
        val hub = reuse ?: RealHubProcess(
            binary = binary,
            configDir = root,
            env = childEnvironment(dataDir),
            logFile = logFile,
            primaryPort = primaryPort,
            secondaryPort = secondaryPort,
        )
        return hub.start()
    }

    /** Runs `flowdrop pair` against the same config and parses the code in memory. */
    fun newPairingCode(dataDir: File): String {
        val builder = ProcessBuilder(
            binary.absolutePath,
            "pair",
            "-config",
            File(root, "absent-config.json").absolutePath,
        )
        builder.redirectErrorStream(true)
        builder.environment().apply {
            clear()
            putAll(childEnvironment(dataDir))
        }
        val child = builder.start()
        try {
            if (!child.waitFor(CLI_TIMEOUT_SECONDS, TimeUnit.SECONDS)) {
                throw AssertionError("flowdrop pair did not finish within ${CLI_TIMEOUT_SECONDS}s")
            }
            val output = child.inputStream.readBytes().toString(Charsets.UTF_8)
            val code = Regex("(?m)^Pairing code: (\\d{4})\\s*$").find(output)?.groupValues?.get(1)
            assertNotNull(
                "flowdrop pair did not print a four-digit code (output withheld: it may hold a credential)",
                code,
            )
            return code!!
        } finally {
            if (child.isAlive) {
                child.destroy()
                child.waitFor(5, TimeUnit.SECONDS)
            }
        }
    }

    /** A loopback port that was free a moment ago and is deliberately never bound. */
    fun unusedPortForDeadProbe(): Int = reservePort("127.0.0.1")

    fun close() {
        removeRecursively(root)
    }

    fun isRemoved(): Boolean = !root.exists()

    /**
     * The child environment: every inherited `FLOWDROP_*` variable is removed
     * (so a stray variable of the user or another agent cannot leak in) and the
     * two explicit loopback listeners, the temporary data directory, plaintext
     * cookies and disabled discovery are set on purpose.
     */
    private fun childEnvironment(dataDir: File): Map<String, String> = buildMap {
        for ((key, value) in System.getenv()) {
            if (key.startsWith("FLOWDROP_")) continue
            put(key, value)
        }
        put("FLOWDROP_DATA_DIR", dataDir.absolutePath)
        put("FLOWDROP_LISTEN", "127.0.0.1:$primaryPort")
        put("FLOWDROP_ADDITIONAL_LISTEN", "127.0.0.2:$secondaryPort")
        put("FLOWDROP_COOKIE_SECURE", "false")
        put("FLOWDROP_DISCOVERY_ENABLED", "false")
        // Only a few KiB move through the Hub here; a 1 GiB free-space floor
        // would let an unrelated host condition decide the result.
        put("FLOWDROP_MIN_FREE_BYTES", "0")
        put("FLOWDROP_ALLOWED_HOSTS", "127.0.0.1,127.0.0.2")
    }

    private companion object {
        const val CLI_TIMEOUT_SECONDS = 10L

        fun reservePort(host: String): Int {
            val address = InetAddress.getByName(host)
            ServerSocket(0, 1, address).use { return it.localPort }
        }
    }
}

// ---------------------------------------------------------------------------
// small test utilities
// ---------------------------------------------------------------------------

private fun realTempRoot(): File =
    Files.createTempDirectory("flowdrop-pa08i-").toFile().also { it.mkdirs() }

private fun OkHttpClient.get(url: String, vararg headers: Pair<String, String>): Response {
    val builder = Request.Builder().url(url).get()
    for ((name, value) in headers) builder.header(name, value)
    return newCall(builder.build()).execute()
}

private fun anonymousClient(): OkHttpClient = OkHttpClient.Builder()
    .retryOnConnectionFailure(false)
    .followRedirects(false)
    .followSslRedirects(false)
    .callTimeout(15, TimeUnit.SECONDS)
    .build()

private fun sha256Hex(bytes: ByteArray): String =
    MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

private fun deterministicBytes(size: Int, seed: Long): ByteArray {
    val random = java.util.Random(seed)
    val out = ByteArray(size)
    random.nextBytes(out)
    return out
}

/**
 * A real, well-formed 4x3 RGB PNG (type 2, 8 bit) built here without any
 * image library: signature, IHDR, one zlib IDAT and IEND with correct CRCs.
 * The bytes are then re-parsed and the IDAT decompressed, so the fixture cannot
 * silently degenerate into something that merely starts with a PNG signature.
 */
private fun realPngBytes(): ByteArray {
    val width = 4
    val height = 3
    val out = ByteArrayOutputStream()
    out.write(byteArrayOf(-119, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A))

    fun chunk(type: String, data: ByteArray) {
        val typeBytes = type.toByteArray(Charsets.US_ASCII)
        out.write(intBytes(data.size))
        out.write(typeBytes)
        out.write(data)
        val crc = CRC32()
        crc.update(typeBytes)
        crc.update(data)
        out.write(intBytes(crc.value.toInt()))
    }

    val ihdr = ByteArrayOutputStream().apply {
        write(intBytes(width))
        write(intBytes(height))
        write(8) // bit depth
        write(2) // colour type: truecolour RGB
        write(0) // compression
        write(0) // filter
        write(0) // interlace
    }.toByteArray()
    chunk("IHDR", ihdr)

    val raw = ByteArrayOutputStream()
    for (y in 0 until height) {
        raw.write(0) // filter type none
        for (x in 0 until width) {
            raw.write(x * 60 + 10)
            raw.write(y * 80 + 20)
            raw.write(((x + y) * 30 + 40).coerceAtMost(255))
        }
    }
    val compressed = Deflater().let { deflater ->
        deflater.setInput(raw.toByteArray())
        deflater.finish()
        val buffer = ByteArray(256)
        val idat = ByteArrayOutputStream()
        while (!deflater.finished()) {
            val n = deflater.deflate(buffer)
            idat.write(buffer, 0, n)
        }
        deflater.end()
        idat.toByteArray()
    }
    chunk("IDAT", compressed)
    chunk("IEND", ByteArray(0))
    val bytes = out.toByteArray()

    // Self-check: signature, IHDR dimensions, and an IDAT that inflates to the
    // exact raw scanline length of the declared image.
    assertTrue("PNG signature", bytes.size > 8 && bytes[1] == 0x50.toByte() && bytes[2] == 0x4E.toByte())
    assertEquals("IHDR must be the first chunk", "IHDR", String(bytes, 12, 4, Charsets.US_ASCII))
    assertEquals("declared width", width, readInt(bytes, 16))
    assertEquals("declared height", height, readInt(bytes, 20))
    val expectedRaw = height * (1 + width * 3)
    val inflater = Inflater()
    inflater.setInput(compressed)
    val inflated = ByteArray(expectedRaw)
    var read = 0
    while (read < expectedRaw) {
        val n = inflater.inflate(inflated, read, expectedRaw - read)
        if (n <= 0) break
        read += n
    }
    inflater.end()
    assertEquals("IDAT must inflate to the scanline length", expectedRaw, read)
    assertTrue("PNG must end with IEND", String(bytes, bytes.size - 8, 4, Charsets.US_ASCII) == "IEND")
    return bytes
}

private fun intBytes(value: Int): ByteArray = byteArrayOf(
    (value ushr 24).toByte(),
    (value ushr 16).toByte(),
    (value ushr 8).toByte(),
    value.toByte(),
)

private fun readInt(bytes: ByteArray, offset: Int): Int =
    (bytes[offset].toInt() and 0xFF shl 24) or
        (bytes[offset + 1].toInt() and 0xFF shl 16) or
        (bytes[offset + 2].toInt() and 0xFF shl 8) or
        (bytes[offset + 3].toInt() and 0xFF)

private fun removeRecursively(file: File) {
    val children = file.listFiles()
    if (children != null) for (child in children) removeRecursively(child)
    file.delete()
}
