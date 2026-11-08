package dev.flowdrop.android.data

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.io.OutputStream
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Thin client for the frozen FlowDrop HTTP contract.
 *
 * Every request carries the session cookies from [PersistentCookieJar] and an
 * `Origin` equal to the Hub's `scheme://host:port`, because the Hub rejects
 * state-changing requests whose Origin does not match its Host. Writes also
 * send `X-FlowDrop-CSRF` matching the `flowdrop_csrf` cookie (double submit).
 */
class ApiClient(
    val endpoint: HubEndpoint,
    private val store: SessionStore,
    private val cookieJar: PersistentCookieJar,
    okHttpClient: OkHttpClient? = null,
) {
    val http: OkHttpClient = (okHttpClient ?: defaultClient()).newBuilder()
        .cookieJar(cookieJar)
        .build()

    private val json = JsonProvider.instance

    /** Latest CSRF token; reissued by GET /auth/session when the cookie is lost. */
    @Volatile
    var csrfToken: String = store.loadFor(endpoint.origin)?.csrfToken.orEmpty()
        private set

    @Volatile
    var maxUploadBytes: Long = DEFAULT_MAX_UPLOAD_BYTES
        private set

    /** Called when any request observes a 401 so the UI can return to login. */
    @Volatile
    var onUnauthorized: (() -> Unit)? = null

    // ---------- auth ----------

    suspend fun session(): SessionInfo {
        val info = requestJson<SessionInfo>(Request.Builder().url(endpoint.url("/api/v1/auth/session")))
        applySessionInfo(info)
        return info
    }

    suspend fun login(username: String, password: String): SessionInfo {
        val body = json.encodeToString(
            LoginRequest.serializer(),
            LoginRequest(username = username, password = password),
        ).toRequestBody(JSON)
        val info = requestJson<SessionInfo>(
            Request.Builder()
                .url(endpoint.url("/api/v1/auth/login"))
                .post(body),
        )
        applySessionInfo(info)
        persistSession(username, info)
        return info
    }

    suspend fun logout() {
        runCatching {
            requestNoContent(
                Request.Builder()
                    .url(endpoint.url("/api/v1/auth/logout"))
                    .post(EMPTY_BODY),
            )
        }
        cookieJar.clearAll()
        csrfToken = ""
    }

    /**
     * Stores the cookies + token for this Hub. Never stores the password.
     * Called after login and whenever the session endpoint hands out a token.
     */
    fun persistSession(username: String, info: SessionInfo) {
        val existing = store.loadFor(endpoint.origin)
        store.save(
            StoredSession(
                // Store the canonical origin, not baseUrl: it is what cookies
                // and the Origin header are scoped to.
                baseUrl = endpoint.origin,
                origin = endpoint.origin,
                username = username.ifBlank { existing?.username.orEmpty() },
                csrfToken = info.csrfToken ?: csrfToken,
                cookies = cookieJar.snapshot(endpoint.origin),
                expiresAt = info.expiresAt ?: 0L,
            ),
        )
    }

    fun discardLocalSession() {
        cookieJar.clearAll()
        csrfToken = ""
    }

    private fun applySessionInfo(info: SessionInfo) {
        info.csrfToken?.let { csrfToken = it }
        info.limits?.maxUploadBytes?.takeIf { it > 0 }?.let { maxUploadBytes = it }
    }

    // ---------- items ----------

    suspend fun list(cursor: String? = null, limit: Int = PAGE_SIZE): Page {
        val query = buildMap {
            put("limit", limit.toString())
            if (!cursor.isNullOrEmpty()) put("cursor", cursor)
        }
        return requestJson(Request.Builder().url(endpoint.url("/api/v1/items", query)))
    }

    suspend fun getItem(id: String): Item =
        requestJson(Request.Builder().url(endpoint.url("/api/v1/items/${pathSegment(id)}")))

    suspend fun createText(type: String, text: String, senderDevice: String): Item {
        val body = json.encodeToString(
            CreateTextRequest.serializer(),
            CreateTextRequest(type = type, text = text, senderDevice = senderDevice),
        ).toRequestBody(JSON)
        return requestJson(
            Request.Builder().url(endpoint.url("/api/v1/items/text")).post(body),
        )
    }

    suspend fun delete(id: String) {
        requestNoContent(
            Request.Builder()
                .url(endpoint.url("/api/v1/items/${pathSegment(id)}"))
                .delete(),
        )
    }

    /**
     * Uploads one file as one item. Progress is reported as raw byte counts;
     * the body is streamed from [UploadSource] so memory use is constant.
     */
    suspend fun upload(
        source: UploadSource,
        senderDevice: String,
        onProgress: (bytes: Long, total: Long?) -> Unit,
    ): Item = withContext(Dispatchers.IO) {
        val requestBody = StreamingRequestBody(source) { onProgress(it, source.size) }
        val multipart = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("sender_device", senderDevice)
            .addFormDataPart("file", source.name, requestBody)
            .build()
        val request = Request.Builder()
            .url(endpoint.url("/api/v1/items/upload"))
            .post(multipart)
            .build()
        val response = http.newCall(applyCommonHeaders(request)).await()
        response.use {
            if (!it.isSuccessful) throw toApiError(it)
            val text = it.body?.string().orEmpty()
            json.decodeFromString(Item.serializer(), text)
        }
    }

    suspend fun downloadUrl(id: String): String = endpoint.url("/api/v1/items/${pathSegment(id)}/download").toString()

    suspend fun previewUrl(id: String): String = endpoint.url("/api/v1/items/${pathSegment(id)}/preview").toString()

    /**
     * Streams an attachment to [sink] and verifies the Hub's `X-Content-SHA256`
     * when present. Returns the byte count.
     *
     * The body copy runs on Dispatchers.IO: OkHttp resumes the caller on its
     * own dispatcher, but callers here are Main-bound, and reading a socket on
     * the main thread would raise NetworkOnMainThreadException.
     */
    suspend fun download(
        id: String,
        sink: OutputStream,
        onProgress: (bytes: Long, total: Long?) -> Unit,
    ): DownloadResult = withContext(Dispatchers.IO) {
        val request = Request.Builder()
            .url(endpoint.url("/api/v1/items/${pathSegment(id)}/download"))
            .get()
            .build()
        val response = http.newCall(applyCommonHeaders(request)).await()
        response.use { res ->
            if (!res.isSuccessful) throw toApiError(res)
            val body = res.body ?: throw ApiException(res.code, "empty_body", "empty response body")
            val total = body.contentLength().takeIf { it > 0 }
            val expected = res.header("X-Content-SHA256").orEmpty()
            val digest = MessageDigest.getInstance("SHA-256")
            var read = 0L
            body.byteStream().use { input ->
                val buffer = ByteArray(DOWNLOAD_BUFFER)
                while (true) {
                    val n = input.read(buffer)
                    if (n == -1) break
                    sink.write(buffer, 0, n)
                    digest.update(buffer, 0, n)
                    read += n
                    onProgress(read, total)
                }
            }
            sink.flush()
            val actual = digest.digest().joinToString("") { "%02x".format(it) }
            if (expected.isNotEmpty() && !expected.equals(actual, ignoreCase = true)) {
                throw ApiException(0, "checksum_mismatch", "download SHA-256 mismatch")
            }
            DownloadResult(bytes = read, sha256 = actual)
        }
    }

    // ---------- plumbing ----------

    private suspend inline fun <reified T> requestJson(builder: Request.Builder): T {
        val text = requestBody(builder)
        if (text.isBlank()) {
            throw ApiException(0, "empty_body", "empty response body")
        }
        return json.decodeFromString(text)
    }

    /**
     * Performs the request and returns the raw body.
     *
     * The read runs on Dispatchers.IO because callers are Main-bound and OkHttp
     * resumes the continuation on the caller's dispatcher.
     */
    private suspend fun requestBody(builder: Request.Builder): String = withContext(Dispatchers.IO) {
        // NOTE: use build(), not builder.get(): get() would overwrite the
        // method and drop the body of a POST.
        val response = http.newCall(applyCommonHeaders(builder.build())).await()
        response.use {
            if (!it.isSuccessful) throw toApiError(it)
            it.body?.string().orEmpty()
        }
    }

    private suspend fun requestNoContent(builder: Request.Builder) {
        withContext(Dispatchers.IO) {
            val response = http.newCall(applyCommonHeaders(builder.build())).await()
            response.use {
                if (!it.isSuccessful) throw toApiError(it)
            }
        }
    }

    private fun applyCommonHeaders(request: Request): Request {
        val builder = request.newBuilder()
            .header("Origin", endpoint.origin)
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
        val method = request.method
        if (method != "GET" && method != "HEAD" && method != "OPTIONS") {
            if (builder.build().header("X-FlowDrop-CSRF") == null && csrfToken.isNotEmpty()) {
                builder.header("X-FlowDrop-CSRF", csrfToken)
            }
        }
        return builder.build()
    }

    private fun toApiError(response: Response): ApiException {
        if (response.code == 401) onUnauthorized?.invoke()
        val bodyText = runCatching { response.body?.string().orEmpty() }.getOrDefault("")
        val parsed = runCatching { json.decodeFromString<ErrorBody>(bodyText) }.getOrNull()
        val detail = parsed?.error
        return ApiException(
            status = response.code,
            code = detail?.code?.ifBlank { "http_${response.code}" } ?: "http_${response.code}",
            message = detail?.message?.ifBlank { "HTTP ${response.code}" } ?: "HTTP ${response.code}",
        )
    }

    companion object {
        const val PAGE_SIZE = 30
        private const val DOWNLOAD_BUFFER = 64 * 1024
        private val JSON = "application/json; charset=utf-8".toMediaType()
        private val EMPTY_BODY = ByteArray(0).toRequestBody(null)

        fun defaultClient(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(0, TimeUnit.MILLISECONDS) // uploads may outlast a fixed write timeout
            .callTimeout(0, TimeUnit.MILLISECONDS)
            .retryOnConnectionFailure(true)
            .followRedirects(false)
            .build()
    }
}

data class DownloadResult(val bytes: Long, val sha256: String)

internal fun pathSegment(value: String): String =
    java.net.URLEncoder.encode(value, "UTF-8").replace("+", "%20")

internal object JsonProvider {
    val instance = kotlinx.serialization.json.Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
        explicitNulls = false
    }
}

internal fun IOException.toApiException(): ApiException =
    ApiException(0, "network", message ?: "network error", this)
