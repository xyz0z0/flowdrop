package dev.flowdrop.android.data

import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.nio.file.Files
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * The offline outbox must survive process death without ever losing or
 * duplicating the user's content: entries and attachments come back on a fresh
 * instance, a send interrupted mid-flight is reported as unconfirmed instead of
 * being resent, a failed stage never publishes anything, and content is only
 * deleted when the caller removes it.
 */
class OutboxStoreTest {

    private lateinit var root: File
    private lateinit var outboxDir: File

    @Before
    fun setUp() {
        root = Files.createTempDirectory("flowdrop-outbox-").toFile()
        outboxDir = File(root, "outbox")
    }

    @After
    fun tearDown() {
        root.deleteRecursively()
    }

    /** A fresh store over the same directory, as after a process restart. */
    private fun reopen() = OutboxStore(outboxDir)

    private fun source(
        name: String,
        contentType: String,
        bytes: ByteArray,
        declaredSize: Long? = bytes.size.toLong(),
        open: () -> InputStream = { bytes.inputStream() },
    ) = UploadSource(name, declaredSize, contentType, open)

    private fun attachmentFile(id: String) = File(File(outboxDir, "attachments"), "$id.bin")

    private fun stagedFiles(): List<File> =
        File(outboxDir, "attachments").listFiles()?.toList().orEmpty()

    @Test
    fun `text entries survive a new store instance`() {
        val first = reopen()
        val text = first.enqueueText("hub-a", "phone", "https://example.com/very/long/url")
        val second = reopen().enqueueText("hub-a", "phone", "第二条离线消息")

        val loaded = reopen()
        val entries = loaded.list()
        assertEquals(listOf(text.id, second.id), entries.map { it.id })
        assertEquals("https://example.com/very/long/url", entries[0].text)
        assertEquals("https://example.com/very/long/url", entries[0].displayName)
        assertNull(entries[0].mimeType)
        assertNull(entries[0].fileName)
        assertNull(entries[0].failure)
        assertEquals(OutboxStatus.PENDING, entries[0].status)
        assertEquals("hub-a", entries[0].hubId)
        assertEquals("phone", entries[0].senderDevice)
    }

    @Test
    fun `sending recovered from disk becomes unconfirmed exactly once`() {
        val first = reopen()
        val entry = first.enqueueText("hub-a", "phone", "hello")
        assertTrue(first.setStatus(entry.id, OutboxStatus.SENDING))

        val restarted = reopen()
        assertEquals(1, restarted.recoverInterrupted())
        assertEquals(OutboxStatus.UNCONFIRMED, restarted.list().single().status)
        // The conversion is persisted: a second startup must not touch it again.
        assertEquals(0, reopen().recoverInterrupted())
        assertEquals(OutboxStatus.UNCONFIRMED, reopen().list().single().status)
    }

    @Test
    fun `recoverInterrupted leaves finished entries alone`() {
        val first = reopen()
        val pending = first.enqueueText("hub-a", "phone", "pending")
        val failed = first.enqueueText("hub-a", "phone", "failed")
        val confirmed = first.enqueueText("hub-a", "phone", "sent")
        assertTrue(first.setStatus(failed.id, OutboxStatus.FAILED, "连接已断开"))
        assertTrue(first.setStatus(confirmed.id, OutboxStatus.UNCONFIRMED))

        assertEquals(0, reopen().recoverInterrupted())
        val statuses = reopen().list().associate { it.id to it.status }
        assertEquals(OutboxStatus.PENDING, statuses[pending.id])
        assertEquals(OutboxStatus.FAILED, statuses[failed.id])
        assertEquals(OutboxStatus.UNCONFIRMED, statuses[confirmed.id])
    }

    @Test
    fun `entries of different hubs are kept apart`() {
        val first = reopen()
        val a = first.enqueueText("hub-a", "phone", "for a")
        val b = first.enqueueText("hub-b", "tablet", "for b")
        assertTrue(first.setStatus(b.id, OutboxStatus.FAILED, "无法连接「b」"))

        val entries = reopen().list()
        assertEquals(listOf("hub-a", "hub-b"), entries.map { it.hubId })
        assertEquals(OutboxStatus.PENDING, entries[0].status)
        assertEquals(OutboxStatus.FAILED, entries[1].status)
        assertEquals("无法连接「b」", entries[1].failure)

        // Removing one target must not touch the other's entry.
        assertTrue(first.remove(a.id))
        assertFalse(first.remove(a.id))
        assertEquals(listOf(b.id), reopen().list().map { it.id })
        assertFalse(first.setStatus(a.id, OutboxStatus.PENDING))
    }

    @Test
    fun `file entry restores original name mime and bytes after restart`() {
        val bytes = ByteArray(200_000) { (it % 251).toByte() }
        val entry = reopen().enqueueFile(
            "hub-a",
            "phone",
            source("季度报告.pdf", "application/pdf", bytes),
            maxBytes = 1024 * 1024,
        )
        assertNull(entry.text)
        assertEquals("季度报告.pdf", entry.displayName)
        assertEquals("季度报告.pdf", entry.fileName)
        assertEquals("application/pdf", entry.mimeType)
        assertEquals(bytes.size.toLong(), entry.sizeBytes)
        assertEquals(OutboxStatus.PENDING, entry.status)

        val restored = reopen().uploadSource(entry)
        assertEquals("季度报告.pdf", restored.name)
        assertEquals("application/pdf", restored.contentType)
        assertEquals(bytes.size.toLong(), restored.size)
        assertArrayEquals(bytes, restored.open().use { it.readBytes() })
    }

    @Test
    fun `remove deletes the staged attachment and the entry`() {
        val bytes = ByteArray(4096) { 7 }
        val store = reopen()
        val entry = store.enqueueFile(
            "hub-a",
            "phone",
            source("photo.jpg", "image/jpeg", bytes),
            maxBytes = 1024 * 1024,
        )
        assertTrue(attachmentFile(entry.id).isFile)

        assertTrue(store.remove(entry.id))
        assertFalse(attachmentFile(entry.id).exists())
        assertTrue(store.list().isEmpty())
        assertTrue(reopen().list().isEmpty())
        assertTrue(stagedFiles().isEmpty())
    }

    @Test
    fun `declared size over the limit is rejected before reading`() {
        val store = reopen()
        val tooLarge = UploadSource(
            name = "huge.bin",
            size = 10_000,
            contentType = "application/octet-stream",
            open = { throw AssertionError("the provider must not be read") },
        )
        try {
            store.enqueueFile("hub-a", "phone", tooLarge, maxBytes = 1000)
            fail("expected the size limit to reject the upload")
        } catch (e: OutboxAttachmentTooLargeException) {
            assertEquals(1000L, e.maxBytes)
        }
        assertTrue(reopen().list().isEmpty())
        assertTrue(stagedFiles().isEmpty())
    }

    @Test
    fun `streamed bytes over the limit leave no entry and no file`() {
        val store = reopen()
        val kept = store.enqueueText("hub-a", "phone", "keep me")
        // The provider under-reports its size, so only the copied bytes can stop it.
        val lying = source(
            name = "big.bin",
            contentType = "application/octet-stream",
            bytes = ByteArray(9000),
            declaredSize = null,
        )
        try {
            store.enqueueFile("hub-a", "phone", lying, maxBytes = 4096)
            fail("expected the byte limit to reject the upload")
        } catch (e: OutboxAttachmentTooLargeException) {
            assertEquals(4096L, e.maxBytes)
        }

        assertEquals(listOf(kept.id), store.list().map { it.id })
        assertEquals(listOf(kept.id), reopen().list().map { it.id })
        assertTrue(stagedFiles().isEmpty())
    }

    @Test
    fun `a failing provider publishes nothing and cleans up`() {
        val store = reopen()
        val kept = store.enqueueText("hub-a", "phone", "keep me")
        val broken = UploadSource(
            name = "broken.bin",
            size = null,
            contentType = "application/octet-stream",
            open = {
                object : InputStream() {
                    private var served = 0

                    override fun read(): Int {
                        if (served++ < 1024) return 1
                        throw IOException("content URI revoked")
                    }
                }
            },
        )
        try {
            store.enqueueFile("hub-a", "phone", broken, maxBytes = 1024 * 1024)
            fail("expected the provider failure to propagate")
        } catch (e: IOException) {
            assertEquals("content URI revoked", e.message)
        }

        assertEquals(listOf(kept.id), store.list().map { it.id })
        assertEquals(listOf(kept.id), reopen().list().map { it.id })
        assertTrue(stagedFiles().isEmpty())
    }

    @Test
    fun `a failed index write keeps the previous entries on disk`() {
        val store = reopen()
        val kept = store.enqueueText("hub-a", "phone", "first")
        // Make the atomic-write staging path unusable without touching index.json.
        val blocker = File(outboxDir, "index.json.tmp")
        assertTrue(blocker.mkdirs())
        assertTrue(File(blocker, "block").createNewFile())

        try {
            store.enqueueText("hub-a", "phone", "second")
            fail("expected the write failure to be reported")
        } catch (e: OutboxStoreException) {
            assertEquals("待发送记录无法保存", e.message)
        }
        assertEquals(listOf(kept.id), store.list().map { it.id })
        assertEquals(listOf(kept.id), reopen().list().map { it.id })
    }

    @Test
    fun `a failed index write discards the staged attachment`() {
        val store = reopen()
        val blocker = File(outboxDir, "index.json.tmp")
        assertTrue(blocker.mkdirs())
        assertTrue(File(blocker, "block").createNewFile())

        try {
            store.enqueueFile(
                "hub-a",
                "phone",
                source("a.bin", "application/octet-stream", ByteArray(1024)),
                maxBytes = 4096,
            )
            fail("expected the write failure to be reported")
        } catch (e: OutboxStoreException) {
            assertEquals("待发送记录无法保存", e.message)
        }
        assertTrue(store.list().isEmpty())
        assertTrue(reopen().list().isEmpty())
        assertTrue(stagedFiles().isEmpty())
    }

    @Test
    fun `a missing attachment is reported instead of uploaded`() {
        val entry = reopen().enqueueFile(
            "hub-a",
            "phone",
            source("gone.txt", "text/plain", "hello".toByteArray()),
            maxBytes = 1024,
        )
        assertTrue(attachmentFile(entry.id).delete())

        try {
            reopen().uploadSource(entry)
            fail("expected the missing file to be reported")
        } catch (e: OutboxAttachmentMissingException) {
            assertTrue(e.message!!.contains("gone.txt"))
        }
    }

    @Test
    fun `recoverInterrupted drops staged files the index does not reference`() {
        val entry = reopen().enqueueFile(
            "hub-a",
            "phone",
            source("kept.bin", "application/octet-stream", ByteArray(2048)),
            maxBytes = 8192,
        )
        assertTrue(reopen().setStatus(entry.id, OutboxStatus.SENDING))
        val attachments = File(outboxDir, "attachments")
        // A crash during the copy and a crash between staging and publishing.
        val orphanPart = File(attachments, "${UUID.randomUUID()}.part").apply { writeText("partial") }
        val orphanBin = File(attachments, "${UUID.randomUUID()}.bin").apply { writeText("staged") }
        // Files that do not follow this store's naming scheme are not ours.
        val foreign = File(attachments, "notes.txt").apply { writeText("user data") }
        val foreignBs = File(attachments, "report.bin").apply { writeText("user data") }

        val restarted = reopen()
        assertEquals(1, restarted.recoverInterrupted())

        assertFalse(orphanPart.exists())
        assertFalse(orphanBin.exists())
        assertTrue(attachmentFile(entry.id).isFile)
        assertTrue(foreign.exists())
        assertTrue(foreignBs.exists())
        // The referenced attachment is still usable after recovery.
        assertEquals(entry.sizeBytes, restarted.uploadSource(entry).size)
        assertEquals(OutboxStatus.UNCONFIRMED, reopen().list().single().status)
    }

    @Test
    fun `an empty index is reported as corrupt and kept`() {
        val store = reopen()
        store.enqueueText("hub-a", "phone", "hello")
        val indexFile = File(outboxDir, "index.json")
        indexFile.writeText("")
        val attachments = File(outboxDir, "attachments")
        assertTrue(attachments.mkdirs())
        val orphan = File(attachments, "${UUID.randomUUID()}.part")
            .apply { writeText("half copied") }

        try {
            reopen().recoverInterrupted()
            fail("expected an empty index to be reported")
        } catch (e: OutboxStoreException) {
            assertEquals("待发送记录已损坏", e.message)
        }
        assertTrue(indexFile.exists())
        assertEquals(0L, indexFile.length())
        // Nothing is cleaned up before the index is known to be valid.
        assertTrue(orphan.exists())
    }

    @Test
    fun `a corrupt index is reported and never wiped`() {
        val store = reopen()
        store.enqueueText("hub-a", "phone", "hello")
        val indexFile = File(outboxDir, "index.json")
        indexFile.writeText("{ not an index", Charsets.UTF_8)

        try {
            reopen().list()
            fail("expected the corrupt index to be reported")
        } catch (e: OutboxStoreException) {
            assertEquals("待发送记录已损坏", e.message)
        }
        assertEquals("{ not an index", indexFile.readText(Charsets.UTF_8))
    }

    @Test
    fun `concurrent enqueues are serialized`() {
        val store = reopen()
        val threads = 8
        val perThread = 5
        val ready = CountDownLatch(threads)
        val go = CountDownLatch(1)
        val workers = (0 until threads).map { worker ->
            Thread {
                ready.countDown()
                go.await()
                repeat(perThread) { index ->
                    store.enqueueText("hub-$worker", "phone-$worker", "message $worker/$index")
                }
            }.also { it.start() }
        }
        assertTrue(ready.await(10, TimeUnit.SECONDS))
        go.countDown()
        workers.forEach { it.join(10_000) }
        workers.forEach { assertFalse("worker still running", it.isAlive) }

        val expected = threads * perThread
        assertEquals(expected, store.list().size)
        assertEquals(expected, reopen().list().size)
    }
}
