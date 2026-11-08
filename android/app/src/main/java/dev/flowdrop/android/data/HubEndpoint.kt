package dev.flowdrop.android.data

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * A validated Hub address plus its canonical origin.
 *
 * The origin (`scheme://host:port`) is what goes into the `Origin` header and
 * what scopes stored cookies: the Hub checks Origin on every write and on the
 * WebSocket upgrade, and the app must never send cookies to another server.
 */
data class HubEndpoint(
    val baseUrl: HttpUrl,
    val origin: String,
) {
    val isHttps: Boolean get() = baseUrl.isHttps

    /**
     * WebSocket URL for /api/v1/events on this endpoint.
     *
     * OkHttp takes an http(s) URL for WebSocket upgrades and switches the
     * scheme itself, so the URL stays http/https here.
     */
    fun eventsUrl(): HttpUrl = baseUrl.newBuilder()
        .encodedPath("/api/v1/events")
        .query(null)
        .fragment(null)
        .build()

    fun url(path: String, query: Map<String, String> = emptyMap()): HttpUrl {
        val builder = baseUrl.newBuilder().encodedPath(path)
        query.forEach { (k, v) -> builder.addQueryParameter(k, v) }
        return builder.build()
    }

    companion object {
        /**
         * Accepts `192.168.1.50:8080`, `http://hub.lan:8080/`, `https://x`.
         * A path prefix is rejected: the Hub serves the API at the root, so
         * silently dropping a path would mislead the user.
         */
        fun parse(raw: String): HubEndpoint? {
            val trimmed = raw.trim()
            if (trimmed.isEmpty()) return null
            val withScheme = if (trimmed.contains("://")) trimmed else "http://$trimmed"
            val url = withScheme.toHttpUrlOrNull() ?: return null
            if (url.scheme != "http" && url.scheme != "https") return null
            if (url.host.isBlank()) return null
            val path = url.encodedPath
            if (path.isNotEmpty() && path != "/") return null
            val base = url.newBuilder()
                .encodedPath("/")
                .query(null)
                .fragment(null)
                .build()
            return HubEndpoint(baseUrl = base, origin = originOf(base))
        }

        fun originOf(url: HttpUrl): String {
            val scheme = url.scheme
            val defaultPort = (scheme == "http" && url.port == 80) || (scheme == "https" && url.port == 443)
            return if (defaultPort) {
                "$scheme://${url.host}"
            } else {
                "$scheme://${url.host}:${url.port}"
            }
        }
    }
}
