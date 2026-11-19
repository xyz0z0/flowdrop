package dev.flowdrop.android.io

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import dev.flowdrop.android.data.UploadSource
import java.io.File

/**
 * Converts a content `Uri` into a streamed [UploadSource].
 *
 * The URI is opened lazily at upload time, on the same call that reads the
 * bytes, so nothing is buffered in memory. Callers must have taken a
 * persistable/temporary read grant before uploading, which the activity does
 * with `takePersistableUriPermission` / by keeping the intent's ClipData alive.
 */
object UriSources {

    fun fromUri(resolver: ContentResolver, uri: Uri): UploadSource {
        val (name, size) = queryNameAndSize(resolver, uri)
        val type = resolver.getType(uri)
            ?: MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase())
            ?: "application/octet-stream"
        return UploadSource(
            name = name,
            size = size,
            contentType = type,
            open = {
                resolver.openInputStream(uri)
                    ?: throw java.io.IOException("cannot open $uri")
            },
        )
    }

    fun fromFile(file: File): UploadSource = UploadSource(
        name = file.name,
        size = file.length(),
        contentType = MimeTypeMap.getSingleton()
            .getMimeTypeFromExtension(file.extension.lowercase())
            ?: "application/octet-stream",
        open = { file.inputStream() },
    )

    private fun queryNameAndSize(resolver: ContentResolver, uri: Uri): Pair<String, Long?> {
        var name: String? = null
        var size: Long? = null
        runCatching {
            resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)
                ?.use { cursor ->
                    if (cursor.moveToFirst()) {
                        val nameIdx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                        val sizeIdx = cursor.getColumnIndex(OpenableColumns.SIZE)
                        if (nameIdx >= 0 && !cursor.isNull(nameIdx)) name = cursor.getString(nameIdx)
                        if (sizeIdx >= 0 && !cursor.isNull(sizeIdx)) size = cursor.getLong(sizeIdx)
                    }
                }
        }
        val fallback = uri.lastPathSegment?.substringAfterLast('/')?.takeIf { it.isNotBlank() }
        return (name ?: fallback ?: "file") to size
    }
}
