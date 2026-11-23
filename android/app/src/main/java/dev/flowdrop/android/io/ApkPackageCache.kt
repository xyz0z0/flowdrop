package dev.flowdrop.android.io

import java.io.File
import java.io.IOException
import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.util.UUID

/**
 * Reusable local copy of a downloaded APK, shared by the save and install
 * actions of one Hub item.
 *
 * Saving to Downloads and handing an APK to the system installer used to
 * download the same bytes separately, and a failed install deleted a package
 * that was already complete. Both actions now resolve the package here first:
 * a package downloaded for either one is reused by the other, and a repeated
 * install after a cancelled or failed installer prompt does not download it
 * again.
 *
 * Layout: `<root>/hub-<hash>/<itemHash>-<sha>/package.apk`. Hub IDs, item IDs
 * and the Hub's SHA-256 are hashed, and the file name is fixed, so no
 * Hub-supplied string reaches the path: no separator can escape [root], and a
 * long or non-ASCII display name cannot push the name into a truncated state
 * (the installer gets a content URI whose MIME type the intent sets, so the
 * missing extension costs nothing).
 *
 * A transfer writes into a unique `.part` file in the destination folder and is
 * published with one same-directory rename. A file visible as `package.apk` is
 * therefore always complete: cancelling or failing deletes only the staging
 * file, and a publish that cannot rename fails instead of exposing a partial
 * package under the final name.
 */
class ApkPackageCache(private val root: File) {

    /**
     * The already complete package for this identity, or null when there is
     * none (never downloaded, cleared from the cache, or truncated).
     *
     * A file whose length no longer matches the Hub's size is removed instead
     * of being reused, so a partial or externally damaged copy is re-downloaded.
     */
    fun complete(hubId: String, itemId: String, sha256: String, expectedBytes: Long): File? {
        val file = destination(hubId, itemId, sha256)
        if (!file.isFile) return null
        val length = file.length()
        if (length <= 0L || (expectedBytes > 0L && length != expectedBytes)) {
            file.delete()
            return null
        }
        return file
    }

    /** Opens a staging file for a new transfer of this identity. */
    fun start(hubId: String, itemId: String, sha256: String, expectedBytes: Long): Part {
        val target = destination(hubId, itemId, sha256)
        val dir = target.parentFile ?: throw IOException("no APK cache directory")
        if (!dir.isDirectory && !dir.mkdirs() && !dir.isDirectory) {
            throw IOException("cannot create APK cache directory")
        }
        val staging = File(dir, NAME + "." + UUID.randomUUID() + PART_SUFFIX)
        return Part(staging, target, expectedBytes)
    }

    /**
     * Final path for this identity. The folder name carries the whole identity;
     * the file name stays fixed so its length is independent of the Hub's
     * display name.
     */
    private fun destination(hubId: String, itemId: String, sha256: String): File {
        val hubDir = File(root, "hub-" + hash(hubId))
        val sha = sha256.lowercase().filter { it in '0'..'9' || it in 'a'..'f' }.take(16)
        val identity = hash(itemId).take(16) + "-" + sha.ifEmpty { "nosha" }
        return File(File(hubDir, identity), NAME)
    }

    private fun hash(value: String): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(value.toByteArray(StandardCharsets.UTF_8))
        return digest.joinToString("") { "%02x".format(it) }
    }

    /**
     * One in-flight transfer. [file] is where the caller streams bytes; nothing
     * is visible under the final name until [publish] succeeds.
     */
    class Part internal constructor(
        val file: File,
        private val destination: File,
        private val expectedBytes: Long,
    ) {
        /**
         * Validates the staged bytes and publishes them atomically.
         *
         * Rejects an empty or short package before it can be reported as
         * complete, and leaves the staging file deleted in that case.
         */
        fun publish(): File {
            val length = file.length()
            if (length <= 0L) {
                file.delete()
                throw IOException("empty APK download")
            }
            if (expectedBytes > 0L && length != expectedBytes) {
                file.delete()
                throw IOException("APK size mismatch: expected $expectedBytes, got $length")
            }
            // Same-directory rename is atomic on every filesystem Android uses.
            // A previous package of the same identity is removed first, so the
            // rename cannot fail on platforms that refuse to replace a file; the
            // new bytes were already validated above and nothing partial is
            // exposed. When the rename still fails, the transfer fails and the
            // staging file is removed instead of being copied under the final
            // name.
            if (destination.exists()) destination.delete()
            if (!file.renameTo(destination)) {
                file.delete()
                throw IOException("cannot publish APK package")
            }
            return destination
        }

        /** Deletes the staging file; safe after a successful [publish]. */
        fun discard() {
            if (file.exists()) file.delete()
        }
    }

    private companion object {
        /** Fixed final name: the identity lives in the parent folder. */
        const val NAME = "package.apk"

        const val PART_SUFFIX = ".part"
    }
}
