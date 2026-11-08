package dev.flowdrop.android

import android.content.Intent
import android.net.Uri
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * ACTION_SEND / ACTION_SEND_MULTIPLE parsing.
 *
 * These run on-device because [Intent] is part of the Android framework, but
 * the parser itself is a pure function so the behaviour is easy to pin.
 */
@RunWith(AndroidJUnit4::class)
class ShareIntentTest {

    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    private fun intent(action: String) = Intent(action).setPackage(context.packageName)

    @Test
    fun ignoresNonShareIntents() {
        assertNull(parseShareIntent(intent(Intent.ACTION_MAIN)))
        assertNull(parseShareIntent(null))
    }

    @Test
    fun parsesSharedText() {
        val payload = parseShareIntent(
            intent(Intent.ACTION_SEND).apply {
                type = "text/plain"
                putExtra(Intent.EXTRA_TEXT, "hello from another app")
            },
        )
        assertNotNull(payload)
        assertEquals(listOf("hello from another app"), payload!!.texts)
        assertTrue(payload.uris.isEmpty())
    }

    @Test
    fun parsesSingleContentUri() {
        val uri = Uri.parse("content://media/external/images/media/42")
        val payload = parseShareIntent(
            intent(Intent.ACTION_SEND).apply {
                type = "image/png"
                putExtra(Intent.EXTRA_STREAM, uri)
            },
        )
        assertNotNull(payload)
        assertEquals(listOf(uri), payload!!.uris)
        assertTrue(payload.texts.isEmpty())
    }

    @Test
    fun parsesMultipleUrisInOrder() {
        val first = Uri.parse("content://media/external/images/media/1")
        val second = Uri.parse("content://media/external/images/media/2")
        val payload = parseShareIntent(
            intent(Intent.ACTION_SEND_MULTIPLE).apply {
                type = "image/*"
                putParcelableArrayListExtra(Intent.EXTRA_STREAM, arrayListOf(first, second))
            },
        )
        assertNotNull(payload)
        assertEquals(listOf(first, second), payload!!.uris)
    }

    @Test
    fun deduplicatesClipDataAgainstExtraStream() {
        val uri = Uri.parse("content://media/external/images/media/7")
        val payload = parseShareIntent(
            intent(Intent.ACTION_SEND).apply {
                type = "image/png"
                putExtra(Intent.EXTRA_STREAM, uri)
                clipData = android.content.ClipData.newRawUri("image", uri)
            },
        )
        assertNotNull(payload)
        assertEquals(1, payload!!.uris.size)
    }

    @Test
    fun fallsBackToClipDataOnlyStreams() {
        val uri = Uri.parse("content://downloads/all_downloads/9")
        val payload = parseShareIntent(
            intent(Intent.ACTION_SEND).apply {
                type = "application/pdf"
                clipData = android.content.ClipData.newRawUri("file", uri)
            },
        )
        assertNotNull(payload)
        assertEquals(listOf(uri), payload!!.uris)
    }

    @Test
    fun emptyShareReturnsNull() {
        assertNull(parseShareIntent(intent(Intent.ACTION_SEND).apply { type = "text/plain" }))
    }
}
