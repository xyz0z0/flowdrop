package dev.flowdrop.android.io

import org.junit.Assert.assertEquals
import org.junit.Test

class DownloadTargetTest {

    @Test
    fun `apk extension wins over the sniffed zip type`() {
        assertEquals(
            "application/vnd.android.package-archive",
            DownloadTarget.resolveMimeType("app-debug.apk", "application/zip"),
        )
    }

    @Test
    fun `apk extension is case insensitive`() {
        assertEquals(
            "application/vnd.android.package-archive",
            DownloadTarget.resolveMimeType("MyApp.APK", "application/octet-stream"),
        )
    }

    @Test
    fun `other names keep the reported type`() {
        assertEquals(
            "image/png",
            DownloadTarget.resolveMimeType("shot.png", "image/png"),
        )
        assertEquals(
            "application/octet-stream",
            DownloadTarget.resolveMimeType("file.bin", ""),
        )
    }

    @Test
    fun `sanitize strips path separators and leading dots`() {
        assertEquals("passwd", DownloadTarget.sanitize("../etc/passwd".substringAfterLast('/')))
        assertEquals("a_b.txt", DownloadTarget.sanitize("a/b.txt"))
        assertEquals("file", DownloadTarget.sanitize("   "))
        assertEquals("x".repeat(200), DownloadTarget.sanitize("x".repeat(500)))
    }
}
