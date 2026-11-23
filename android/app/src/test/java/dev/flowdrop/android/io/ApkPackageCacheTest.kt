package dev.flowdrop.android.io

import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.io.File
import java.io.IOException
import java.nio.file.Files

/**
 * The APK delivered to the system installer must always be the whole package:
 * a cancelled or failed transfer may never be visible as complete, and a
 * finished package must be reused instead of downloaded again.
 */
class ApkPackageCacheTest {

    private lateinit var root: File
    private lateinit var cache: ApkPackageCache

    @Before
    fun setUp() {
        root = Files.createTempDirectory("flowdrop-apk-").toFile()
        cache = ApkPackageCache(File(root, "downloads/apk"))
    }

    @After
    fun tearDown() {
        root.deleteRecursively()
    }

    private val bytes = ByteArray(16) { it.toByte() }

    /** Publishes [content] as the package of one identity. */
    private fun publish(
        hubId: String = "hub-a",
        itemId: String = "item-1",
        sha256: String = "abcdef0123456789",
        content: ByteArray = bytes,
    ): File {
        val part = cache.start(hubId, itemId, sha256, content.size.toLong())
        part.file.writeBytes(content)
        return part.publish()
    }

    @Test
    fun `a staging file is not reusable until it is published`() {
        val part = cache.start("hub-a", "item-1", "abcdef", bytes.size.toLong())
        part.file.writeBytes(bytes)

        // The transfer is still running: nothing may look complete yet.
        assertNull(cache.complete("hub-a", "item-1", "abcdef", bytes.size.toLong()))
        assertFalse(part.file.name == "package.apk")

        val packageFile = part.publish()
        assertEquals(packageFile, cache.complete("hub-a", "item-1", "abcdef", bytes.size.toLong()))
        assertFalse(part.file.exists())
    }

    @Test
    fun `published package keeps a fixed safe name under the cache root`() {
        val file = publish()
        assertEquals("package.apk", file.name)
        assertTrue(file.absolutePath.startsWith(File(root, "downloads/apk").absolutePath))
    }

    @Test
    fun `hub supplied ids never escape the cache root`() {
        val file = publish(hubId = "../../../etc", itemId = "../../passwd")
        val rootPath = File(root, "downloads/apk").canonicalFile
        assertTrue(file.canonicalPath.startsWith(rootPath.path + File.separator))
    }

    @Test
    fun `a truncated cached package is removed instead of handed over`() {
        val file = publish(content = ByteArray(4096))
        file.writeBytes(ByteArray(1024))

        assertNull(cache.complete("hub-a", "item-1", "abcdef0123456789", 4096))
        assertFalse(file.exists())
    }

    @Test
    fun `a short transfer is rejected and leaves no package behind`() {
        val part = cache.start("hub-a", "item-1", "abcdef", 4096)
        part.file.writeBytes(ByteArray(1024))

        try {
            part.publish()
            fail("a short package must not be published")
        } catch (_: IOException) {
            // expected
        }
        assertFalse(part.file.exists())
        assertNull(cache.complete("hub-a", "item-1", "abcdef", 4096))
    }

    @Test
    fun `an empty transfer is rejected`() {
        val part = cache.start("hub-a", "item-1", "abcdef", 0)
        try {
            part.publish()
            fail("an empty package must not be published")
        } catch (_: IOException) {
            // expected
        }
        assertNull(cache.complete("hub-a", "item-1", "abcdef", 0))
    }

    @Test
    fun `an unknown expected size still requires a non-empty package`() {
        val file = publish(content = bytes)
        // Hub reported no size: the presence of bytes is the only check left.
        assertEquals(file, cache.complete("hub-a", "item-1", "abcdef0123456789", 0))
    }

    @Test
    fun `different hubs never share a package`() {
        val a = publish(hubId = "hub-a")
        val b = publish(hubId = "hub-b")

        assertNotEquals(a, b)
        assertEquals(a, cache.complete("hub-a", "item-1", "abcdef0123456789", bytes.size.toLong()))
        assertEquals(b, cache.complete("hub-b", "item-1", "abcdef0123456789", bytes.size.toLong()))
    }

    @Test
    fun `different items and revisions are isolated`() {
        val first = publish(itemId = "item-1")
        val second = publish(itemId = "item-2")
        val revision = publish(itemId = "item-1", sha256 = "ffffffffffffffff")

        assertNotEquals(first, second)
        assertNotEquals(first, revision)
    }

    @Test
    fun `discarding a transfer after publish keeps the package`() {
        val part = cache.start("hub-a", "item-1", "sha", bytes.size.toLong())
        part.file.writeBytes(bytes)
        val published = part.publish()
        part.discard()

        assertTrue(published.exists())
        assertEquals(published, cache.complete("hub-a", "item-1", "sha", bytes.size.toLong()))
    }

    @Test
    fun `a fresh download replaces the previous package of the same identity`() {
        val old = publish(content = ByteArray(8) { 1 })
        val replacement = ByteArray(8) { 2 }
        val fresh = publish(content = replacement)

        assertEquals(old, fresh)
        assertTrue(fresh.readBytes().contentEquals(replacement))
        assertNull(cache.complete("hub-a", "item-1", "abcdef0123456789", 16))
    }
}
