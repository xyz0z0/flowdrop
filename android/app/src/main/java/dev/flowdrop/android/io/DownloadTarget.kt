package dev.flowdrop.android.io

import android.content.ContentResolver
import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.core.content.FileProvider
import java.io.File
import java.io.OutputStream

/**
 * Destination for a streamed download.
 *
 * - On API 29+ files go to the public Downloads collection through MediaStore,
 *   so no storage permission is needed.
 * - On API 26–28 the app writes into its own external files dir and exposes it
 *   to the system installer/other apps through a FileProvider. Using the app
 *   directory avoids requesting `WRITE_EXTERNAL_STORAGE`, which the app
 *   deliberately does not hold.
 */
sealed interface DownloadTarget {
    val displayName: String
    fun openSink(): OutputStream
    /** Human-readable location shown in the toast. */
    val location: String

    class MediaStoreTarget(
        private val resolver: ContentResolver,
        override val displayName: String,
        private val mimeType: String,
    ) : DownloadTarget {
        private lateinit var uri: Uri

        override val location: String get() = "Downloads/$displayName"

        @androidx.annotation.RequiresApi(Build.VERSION_CODES.Q)
        override fun openSink(): OutputStream {
            val values = ContentValues().apply {
                put(MediaStore.Downloads.DISPLAY_NAME, displayName)
                put(MediaStore.Downloads.MIME_TYPE, mimeType)
                put(MediaStore.Downloads.IS_PENDING, 1)
            }
            val collection = MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
            val inserted = resolver.insert(collection, values)
                ?: throw java.io.IOException("cannot create Downloads entry")
            uri = inserted
            return resolver.openOutputStream(inserted)
                ?: throw java.io.IOException("cannot open Downloads output")
        }

        fun finish(success: Boolean) {
            if (!::uri.isInitialized) return
            val values = ContentValues().apply {
                put(MediaStore.Downloads.IS_PENDING, if (success) 0 else 1)
            }
            resolver.update(uri, values, null, null)
            if (!success) runCatching { resolver.delete(uri, null, null) }
        }
    }

    class AppFileTarget(
        private val filesDir: File?,
        override val displayName: String,
    ) : DownloadTarget {
        private var file: File? = null

        override val location: String get() = file?.absolutePath.orEmpty()

        override fun openSink(): OutputStream {
            val dir = File(filesDir, "exports").apply { mkdirs() }
            val out = File(dir, sanitize(displayName))
            file = out
            return out.outputStream()
        }

        fun file(): File? = file
    }

    companion object {
        /**
         * The Hub sniffs content types from bytes, so an APK (a ZIP container)
         * is reported as `application/zip`. MediaStore derives the visible
         * filename's extension from the MIME type, which would rename
         * `app.apk` to `app.apk.zip` and break the system installer. Normalize
         * by extension for the few cases where the name is authoritative.
         */
        fun resolveMimeType(displayName: String, reported: String): String {
            val lower = displayName.lowercase()
            return when {
                lower.endsWith(".apk") -> "application/vnd.android.package-archive"
                lower.endsWith(".apks") || lower.endsWith(".xapk") -> "application/octet-stream"
                else -> reported.ifBlank { "application/octet-stream" }
            }
        }

        fun create(context: Context, displayName: String, mimeType: String): DownloadTarget {
            val safe = sanitize(displayName)
            val mime = resolveMimeType(safe, mimeType)
            return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                MediaStoreTarget(context.contentResolver, safe, mime)
            } else {
                AppFileTarget(context.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS), safe)
            }
        }

        fun sanitize(name: String): String {
            val cleaned = name.replace('/', '_').replace('\\', '_').trim().trimStart('.')
            return cleaned.ifEmpty { "file" }.take(200)
        }
    }
}

/**
 * Hands a downloaded APK to the system package installer. The app never
 * installs anything itself: it grants a one-shot read URI to the installer
 * activity and lets Android show its own confirmation UI.
 */
object ApkInstaller {
    fun contentUriFor(context: Context, file: File): Uri =
        FileProvider.getUriForFile(context, "${context.packageName}.files", file)
}
