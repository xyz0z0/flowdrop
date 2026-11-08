package dev.flowdrop.android.ui

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import dev.flowdrop.android.data.ApiClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.util.concurrent.ConcurrentHashMap

/**
 * Loads protected image previews through the authenticated OkHttp client.
 *
 * A plain `AsyncImage`/URL cannot be used: `/preview` requires the session
 * cookie, so the bytes are fetched with the same client the API uses and
 * decoded in memory. Results are cached per item id and the decoded bitmaps
 * are bounded by a small LRU so a long inbox cannot exhaust memory.
 */
class AuthenticatedImageStore(private val clientProvider: () -> ApiClient?) {

    private val cache = object : LinkedHashMap<String, Bitmap>(24, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Bitmap>?): Boolean =
            size > MAX_ENTRIES
    }

    private val inFlight = ConcurrentHashMap<String, Unit>()

    fun cached(itemId: String): Bitmap? = synchronized(cache) { cache[itemId] }

    suspend fun load(itemId: String): Bitmap? {
        cached(itemId)?.let { return it }
        val api = clientProvider() ?: return null
        val request = Request.Builder()
            .url(api.previewUrl(itemId))
            .header("Origin", api.endpoint.origin)
            .get()
            .build()
        return withContext(Dispatchers.IO) {
            runCatching {
                inFlight[itemId] = Unit
                api.http.newCall(request).execute().use { response ->
                    if (!response.isSuccessful) return@use null
                    val bytes = response.body?.bytes() ?: return@use null
                    // Decode sampled to keep large screenshots from OOM-ing.
                    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                    BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
                    val sample = sampleSize(bounds.outWidth, bounds.outHeight)
                    val bitmap = BitmapFactory.decodeByteArray(
                        bytes,
                        0,
                        bytes.size,
                        BitmapFactory.Options().apply { inSampleSize = sample },
                    )
                    bitmap?.let { synchronized(cache) { cache[itemId] = it } }
                    bitmap
                }
            }.getOrNull().also { inFlight.remove(itemId) }
        }
    }

    fun clear() {
        synchronized(cache) { cache.clear() }
        inFlight.clear()
    }

    private fun sampleSize(width: Int, height: Int): Int {
        var sample = 1
        var w = width
        var h = height
        while (w / 2 >= MAX_DIMENSION && h / 2 >= MAX_DIMENSION) {
            w /= 2
            h /= 2
            sample *= 2
        }
        return sample
    }

    private companion object {
        const val MAX_ENTRIES = 12
        const val MAX_DIMENSION = 720
    }
}

/** Composable wrapper that resolves an authenticated preview, or null. */
@Composable
fun rememberAuthenticatedImage(store: AuthenticatedImageStore, itemId: String): ImageBitmap? {
    var image by remember(itemId) { mutableStateOf(store.cached(itemId)?.asImageBitmap()) }
    LaunchedEffect(itemId) {
        if (image == null) {
            val loaded = store.load(itemId)
            if (loaded != null) image = loaded.asImageBitmap()
        }
    }
    return image
}
