package dev.flowdrop.android.data

import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl

/**
 * Cookie jar backed by [SessionStore].
 *
 * Cookies are filtered by exact origin in [loadForCookie] as well as by the
 * standard domain/path rules, so a session cookie is only ever returned to the
 * Hub the user logged into. This is what keeps "change the address" safe.
 */
class PersistentCookieJar(private val store: SessionStore) : CookieJar {

    @Synchronized
    override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        val origin = HubEndpoint.originOf(url)
        val current = store.load() ?: StoredSession(baseUrl = url.toString(), origin = origin)
        if (current.origin != origin) return
        val merged = current.cookies.associateBy { it.name to it.path }.toMutableMap()
        for (cookie in cookies) {
            val key = cookie.name to cookie.path
            if (cookie.expiresAt <= System.currentTimeMillis() || cookie.value.isEmpty()) {
                merged.remove(key)
            } else {
                merged[key] = cookie.toStored()
            }
        }
        store.save(current.copy(cookies = merged.values.toList()))
    }

    @Synchronized
    override fun loadForRequest(url: HttpUrl): List<Cookie> {
        val origin = HubEndpoint.originOf(url)
        val current = store.load() ?: return emptyList()
        if (current.origin != origin) return emptyList()
        val now = System.currentTimeMillis()
        return current.cookies
            .filter { it.expiresAt == 0L || it.expiresAt > now }
            .mapNotNull { it.toCookie(url) }
    }

    @Synchronized
    fun snapshot(origin: String): List<StoredCookie> =
        store.load()?.takeIf { it.origin == origin }?.cookies.orEmpty()

    @Synchronized
    fun clearAll() {
        store.clear()
    }

    private fun Cookie.toStored() = StoredCookie(
        name = name,
        value = value,
        host = domain,
        path = path,
        secure = secure,
        httpOnly = httpOnly,
        expiresAt = expiresAt,
    )

    private fun StoredCookie.toCookie(url: HttpUrl): Cookie? = try {
        Cookie.Builder()
            .name(name)
            .value(value)
            .path(path)
            .apply {
                if (host.startsWith(".")) domain(host.removePrefix(".")) else hostOnlyDomain(url.host)
                if (secure) secure()
                if (httpOnly) httpOnly()
                if (expiresAt > 0) expiresAt(expiresAt)
            }
            .build()
            .takeIf { it.matches(url) }
    } catch (_: IllegalArgumentException) {
        null
    }
}
