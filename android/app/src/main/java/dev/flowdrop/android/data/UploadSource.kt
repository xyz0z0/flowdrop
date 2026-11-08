package dev.flowdrop.android.data

import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody
import okio.BufferedSink
import java.io.IOException
import java.io.InputStream

/**
 * One attachment to upload. `open()` is called at most once per request and
 * streams straight from the provider, so a 500 MB file is never buffered in
 * memory.
 */
data class UploadSource(
    val name: String,
    val size: Long?,
    val contentType: String,
    val open: () -> InputStream,
)

/** Streams an [UploadSource] into an OkHttp multipart body, reporting bytes. */
internal class StreamingRequestBody(
    private val source: UploadSource,
    private val onProgress: (bytes: Long) -> Unit,
) : RequestBody() {

    override fun contentType() = source.contentType.toMediaTypeOrNull()

    override fun contentLength(): Long = source.size ?: -1L

    // The InputStream cannot be replayed, so OkHttp must not silently retry.
    override fun isOneShot(): Boolean = true

    override fun writeTo(sink: BufferedSink) {
        try {
            source.open().use { input ->
                val buffer = ByteArray(BUFFER_SIZE)
                var written = 0L
                while (true) {
                    val read = input.read(buffer)
                    if (read == -1) break
                    sink.write(buffer, 0, read)
                    written += read
                    onProgress(written)
                }
            }
        } catch (e: IOException) {
            throw e
        } catch (e: Exception) {
            // OkHttp only tolerates IOException from a request body. A
            // SecurityException (revoked content-URI grant) or any other
            // RuntimeException would escape on the dispatcher thread and
            // crash the process, so normalize it into a failed call instead.
            throw IOException("upload source could not be read", e)
        }
    }

    private companion object {
        const val BUFFER_SIZE = 64 * 1024
    }
}
