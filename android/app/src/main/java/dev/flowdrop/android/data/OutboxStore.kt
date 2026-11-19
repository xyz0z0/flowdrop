package dev.flowdrop.android.data

import kotlinx.serialization.Serializable
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.IOException
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.UUID

/** Delivery state of one queued item. */
enum class OutboxStatus {
    /** Queued, nothing sent yet. */
    PENDING,

    /** A send was started; whether the Hub saw it is not known until confirmed. */
    SENDING,

    /** A send started but the connection dropped before the result was known. */
    UNCONFIRMED,

    /** The last attempt failed in a way the user has to look at. */
    FAILED,
}

/**
 * One offline-queued item: either text (a message or a link, classified only at
 * send time) or one attachment staged in app-private storage.
 *
 * [displayName] is only a UI label; a queued text uses the text itself and the
 * UI limits it to a few lines. For attachments [fileName] keeps the original
 * name so an upload retried after process death still reports it, and
 * [sizeBytes] is the byte count actually staged on disk (not what the provider
 * claimed).
 */
@Serializable
data class OutboxEntry(
    val id: String,
    val hubId: String,
    val senderDevice: String,
    val displayName: String,
    val text: String? = null,
    val mimeType: String? = null,
    val fileName: String? = null,
    val sizeBytes: Long = 0,
    val status: OutboxStatus = OutboxStatus.PENDING,
    val failure: String? = null,
)

/** Base type of every failure [OutboxStore] reports to its caller. */
open class OutboxException(message: String, cause: Throwable? = null) : IOException(message, cause)

/** The index or a staged file could not be read or written; disk state is unchanged. */
class OutboxStoreException(message: String, cause: Throwable? = null) : OutboxException(message, cause)

/** The staged attachment behind an entry is gone (or truncated); it must be re-added. */
class OutboxAttachmentMissingException(message: String) : OutboxException(message)

/** The provider produced more bytes than the Hub accepts; nothing was queued. */
class OutboxAttachmentTooLargeException(val maxBytes: Long) :
    OutboxException("附件超过上传上限（$maxBytes 字节）")

@Serializable
private data class OutboxIndex(
    val version: Int = 1,
    val entries: List<OutboxEntry> = emptyList(),
)

/**
 * Durable queue of items the user sent while the Hub was unreachable.
 *
 * Contract:
 * - every public method is synchronized, so the store is safe to use from
 *   several threads (the app calls it from `Dispatchers.IO`);
 * - an entry is published only after it is on disk; a failed write throws and
 *   leaves the previously stored entries untouched;
 * - attachments are streamed into `directory/attachments` while the real byte
 *   count is enforced against `maxBytes`, so a lying provider cannot overshoot;
 *   an overflow or a read error removes the partial file and publishes nothing;
 * - [SENDING] is written to disk before a send starts, so after process death
 *   [recoverInterrupted] can turn it into [UNCONFIRMED] (delivery unknown)
 *   instead of silently resending it;
 * - content is deleted only on [remove] (user clears it, or the caller saw a
 *   definite success); a broken or empty index is reported, never wiped;
 * - [recoverInterrupted] (called once per process, before the queue is used)
 *   also drops staged files that the verified index does not reference, which
 *   are left when the process dies between staging and committing an entry.
 *
 * The directory must be app-private, persistent storage (the app passes a
 * subdirectory of `Context.noBackupFilesDir`), never a cache dir or a temporary
 * content URI.
 */
class OutboxStore(private val directory: File) {

    private val indexFile = File(directory, INDEX_FILE)
    private val attachmentsDir = File(directory, ATTACHMENTS_DIR)
    private val json = JsonProvider.instance

    /** Parsed index; `null` until read. Only touched under the instance lock. */
    private var cache: MutableList<OutboxEntry>? = null

    /** All queued entries in enqueue order, oldest first. */
    @Synchronized
    fun list(): List<OutboxEntry> = entries().toList()

    /** Queues [text] (a message or a link; the send step classifies it). */
    @Synchronized
    fun enqueueText(hubId: String, senderDevice: String, text: String): OutboxEntry {
        require(hubId.isNotBlank()) { "hubId must not be blank" }
        val entry = OutboxEntry(
            id = newId(),
            hubId = hubId,
            senderDevice = senderDevice,
            displayName = text,
            text = text,
            status = OutboxStatus.PENDING,
        )
        commit(entries() + entry)
        return entry
    }

    /**
     * Queues one attachment, streaming [source] into private storage.
     *
     * The copy happens before the entry exists, so a provider that fails or
     * exceeds [maxBytes] leaves neither an entry nor a partial file. [maxBytes]
     * is the Hub's limit; pass a positive value.
     */
    @Synchronized
    fun enqueueFile(
        hubId: String,
        senderDevice: String,
        source: UploadSource,
        maxBytes: Long,
    ): OutboxEntry {
        require(hubId.isNotBlank()) { "hubId must not be blank" }
        require(maxBytes > 0) { "maxBytes must be positive" }
        val declared = source.size
        if (declared != null && declared > maxBytes) throw OutboxAttachmentTooLargeException(maxBytes)

        val all = entries()
        val id = newId()
        val name = source.name.ifBlank { "$id.bin" }
        val mime = source.contentType.ifBlank { DEFAULT_MIME }

        ensureAttachmentsDir()
        val partial = File(attachmentsDir, "$id.$PART_SUFFIX")
        var staged: File? = null
        var copied = 0L
        try {
            source.open().use { input ->
                FileOutputStream(partial).use { out ->
                    val buffer = ByteArray(COPY_BUFFER)
                    while (true) {
                        val read = input.read(buffer)
                        if (read == -1) break
                        if (read == 0) continue
                        copied += read
                        if (copied > maxBytes) throw OutboxAttachmentTooLargeException(maxBytes)
                        out.write(buffer, 0, read)
                    }
                    out.flush()
                    out.fd.sync()
                }
            }
            val target = File(attachmentsDir, attachmentName(id))
            moveIntoPlace(partial, target, "附件无法保存")
            staged = target

            val entry = OutboxEntry(
                id = id,
                hubId = hubId,
                senderDevice = senderDevice,
                displayName = name,
                mimeType = mime,
                fileName = name,
                sizeBytes = copied,
                status = OutboxStatus.PENDING,
            )
            commit(all + entry)
            return entry
        } catch (e: Throwable) {
            // The entry was never published, so nothing may keep the bytes.
            staged?.delete()
            partial.delete()
            if (e is IOException || e is Error) throw e
            throw OutboxStoreException("附件无法读取", e)
        }
    }

    /**
     * Rebuilds the [UploadSource] of a queued attachment: original file name,
     * original MIME type and the bytes actually staged.
     *
     * Throws [OutboxAttachmentMissingException] when the file is gone, so the
     * caller can ask the user to re-add it instead of sending a broken upload.
     */
    @Synchronized
    fun uploadSource(entry: OutboxEntry): UploadSource {
        val name = entry.fileName ?: throw IllegalArgumentException("entry ${entry.id} has no attachment")
        val mime = entry.mimeType ?: throw IllegalArgumentException("entry ${entry.id} has no attachment")
        val file = attachmentFile(entry.id)
        if (!file.isFile) {
            throw OutboxAttachmentMissingException("附件已丢失：$name")
        }
        val size = file.length()
        if (size != entry.sizeBytes) {
            throw OutboxAttachmentMissingException("附件已损坏：$name")
        }
        return UploadSource(
            name = name,
            size = size,
            contentType = mime,
            open = {
                if (!file.isFile) throw OutboxAttachmentMissingException("附件已丢失：$name")
                FileInputStream(file)
            },
        )
    }

    /**
     * Records the delivery state of one entry. [failure] is the user-readable
     * reason shown for [OutboxStatus.FAILED].
     *
     * Returns `false` when no entry has [id] (it was already removed).
     */
    @Synchronized
    fun setStatus(id: String, status: OutboxStatus, failure: String? = null): Boolean {
        val all = entries()
        val index = all.indexOfFirst { it.id == id }
        if (index < 0) return false
        val updated = all.toMutableList()
        updated[index] = all[index].copy(status = status, failure = failure)
        commit(updated)
        return true
    }

    /**
     * Drops the entry and its staged bytes. This is the only way content is
     * deleted: call it after a definite success or when the user clears an item.
     *
     * Returns `false` when no entry has [id].
     */
    @Synchronized
    fun remove(id: String): Boolean {
        val all = entries()
        if (all.none { it.id == id }) return false
        commit(all.filterNot { it.id == id })
        attachmentFile(id).delete()
        return true
    }

    /**
     * Called once at startup, before the queue is used, after process death.
     *
     * Two things happen, both only after the index was read and validated:
     * - staged files that the index does not reference are deleted. Unreferenced
     *   files exist when the process died mid-copy (`<id>.part`) or between
     *   staging and committing the entry (`<id>.bin`); only names of this
     *   store's own scheme are touched, and only inside the attachments dir.
     * - every entry left in [SENDING] becomes [UNCONFIRMED] — the request may or
     *   may not have reached the Hub. That conversion is persisted, so a second
     *   call returns 0.
     *
     * Returns the number of entries that were recovered.
     */
    @Synchronized
    fun recoverInterrupted(): Int {
        val all = entries()
        cleanupUnreferencedAttachments(all.mapTo(HashSet()) { it.id })
        val interrupted = all.count { it.status == OutboxStatus.SENDING }
        if (interrupted == 0) return 0
        val updated = all.map {
            if (it.status == OutboxStatus.SENDING) it.copy(status = OutboxStatus.UNCONFIRMED) else it
        }
        commit(updated)
        return interrupted
    }

    // ---- disk -----------------------------------------------------------------

    private fun entries(): MutableList<OutboxEntry> {
        cache?.let { return it }
        val loaded = readIndex().toMutableList()
        cache = loaded
        return loaded
    }

    private fun readIndex(): List<OutboxEntry> {
        if (!indexFile.exists()) return emptyList()
        val raw = try {
            indexFile.readText(Charsets.UTF_8)
        } catch (e: IOException) {
            throw OutboxStoreException("待发送记录无法读取", e)
        }
        if (raw.isBlank()) {
            // An empty index is not an empty queue: something truncated it, and
            // overwriting it would silently drop every entry the user still has.
            throw OutboxStoreException("待发送记录已损坏")
        }
        return try {
            json.decodeFromString(OutboxIndex.serializer(), raw).entries
        } catch (e: Exception) {
            // Never wipe an index that does not parse: report it and keep the file.
            throw OutboxStoreException("待发送记录已损坏", e)
        }
    }

    /** Persists [entries] first, then makes them the in-memory state. */
    private fun commit(entries: List<OutboxEntry>) {
        ensureDir(directory)
        val payload = try {
            json.encodeToString(OutboxIndex.serializer(), OutboxIndex(entries = entries))
        } catch (e: Exception) {
            throw OutboxStoreException("待发送记录无法保存", e)
        }
        val partial = File(directory, "$INDEX_FILE.tmp")
        try {
            FileOutputStream(partial).use { out ->
                out.write(payload.toByteArray(Charsets.UTF_8))
                out.flush()
                out.fd.sync()
            }
            moveIntoPlace(partial, indexFile, "待发送记录无法保存")
        } catch (e: IOException) {
            partial.delete()
            throw if (e is OutboxException) e else OutboxStoreException("待发送记录无法保存", e)
        }
        cache = entries.toMutableList()
    }

    private fun moveIntoPlace(from: File, to: File, failure: String) {
        try {
            try {
                Files.move(
                    from.toPath(),
                    to.toPath(),
                    StandardCopyOption.REPLACE_EXISTING,
                    StandardCopyOption.ATOMIC_MOVE,
                )
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(from.toPath(), to.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        } catch (e: IOException) {
            from.delete()
            throw OutboxStoreException(failure, e)
        } catch (e: SecurityException) {
            from.delete()
            throw OutboxStoreException(failure, e)
        }
    }

    private fun attachmentFile(id: String) = File(attachmentsDir, attachmentName(id))

    private fun attachmentName(id: String) = "$id.$ATTACHMENT_SUFFIX"

    /** Best effort: a staged file that cannot be deleted is simply retried next startup. */
    private fun cleanupUnreferencedAttachments(referenced: Set<String>) {
        val files = attachmentsDir.listFiles() ?: return
        for (file in files) {
            if (!file.isFile) continue
            val suffix = file.name.substringAfterLast('.', "")
            if (suffix != PART_SUFFIX && suffix != ATTACHMENT_SUFFIX) continue
            val id = file.name.substringBeforeLast('.')
            // Only this store's own names are candidates; anything else in the
            // directory is left untouched.
            if (!isEntryId(id)) continue
            if (suffix == ATTACHMENT_SUFFIX && id in referenced) continue
            file.delete()
        }
    }

    private fun isEntryId(value: String): Boolean {
        // UUID.fromString also accepts short forms such as "1-1-1-1-1"; generated
        // ids are always the canonical 36 characters.
        if (value.length != 36) return false
        return try {
            UUID.fromString(value)
            true
        } catch (_: IllegalArgumentException) {
            false
        }
    }

    private fun ensureAttachmentsDir() = ensureDir(attachmentsDir)

    private fun ensureDir(dir: File) {
        if (dir.isDirectory) return
        if (dir.mkdirs() || dir.isDirectory) return
        throw OutboxStoreException("无法创建待发送目录：${dir.name}")
    }

    private fun newId() = UUID.randomUUID().toString()

    private companion object {
        const val INDEX_FILE = "index.json"
        const val ATTACHMENTS_DIR = "attachments"
        const val PART_SUFFIX = "part"
        const val ATTACHMENT_SUFFIX = "bin"
        const val COPY_BUFFER = 64 * 1024
        const val DEFAULT_MIME = "application/octet-stream"
    }
}
