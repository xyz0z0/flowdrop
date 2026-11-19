package dev.flowdrop.android.data

import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.CookieJar
import okhttp3.OkHttpClient
import okhttp3.Request

/**
 * Credential-free identity probe for one candidate address of a paired Hub.
 *
 * GET /api/v1/health is public and returns the stable `hub_id`, which is the
 * only fact the app may learn from an address it has not verified yet.
 *
 * Hard rules (see docs/PAIRING.md):
 * - a brand-new client per probe, with **no** cookie jar and no authenticator,
 *   so no session cookie, CSRF token or device token can ever be attached;
 * - no redirects followed, so a pushy peer cannot bounce the request to
 *   another origin;
 * - short timeouts, so a dead LAN address cannot stall the selection;
 * - cancellation propagates ([CancellationException] is rethrown), so a
 *   cancelled caller stops probing instead of leaking a request.
 *
 * The probe returns `null` for anything that is not a healthy FlowDrop Hub
 * answering with a non-empty `hub_id`; it never throws for network problems.
 */
object HubProbe {

    private const val CONNECT_TIMEOUT_MILLIS = 1_200L
    private const val READ_TIMEOUT_MILLIS = 1_200L
    private const val CALL_TIMEOUT_MILLIS = 2_000L

    /** `hub_id` of the Hub answering [candidate], or `null` when unusable. */
    suspend fun hubId(candidate: RouteCandidate): String? = withContext(Dispatchers.IO) {
        val endpoint = HubEndpoint.parse(candidate.url) ?: return@withContext null
        val request = Request.Builder()
            .url(endpoint.url("/api/v1/health"))
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
            .get()
            .build()
        val client = newProbeClient()
        try {
            client.newCall(request).await().use { response ->
                if (!response.isSuccessful) return@withContext null
                val text = response.body?.string().orEmpty()
                if (text.isBlank()) return@withContext null
                val health = JsonProvider.instance.decodeFromString(HealthInfo.serializer(), text)
                health.hubId.takeIf { it.isNotBlank() }
            }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        } finally {
            // A per-probe client owns its own pool and dispatcher; release both
            // so repeated selections do not accumulate threads or sockets.
            client.connectionPool.evictAll()
            client.dispatcher.executorService.shutdown()
        }
    }

    private fun newProbeClient(): OkHttpClient = OkHttpClient.Builder()
        .cookieJar(CookieJar.NO_COOKIES)
        .retryOnConnectionFailure(false)
        .followRedirects(false)
        .followSslRedirects(false)
        .connectTimeout(CONNECT_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .readTimeout(READ_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .callTimeout(CALL_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .build()
}
