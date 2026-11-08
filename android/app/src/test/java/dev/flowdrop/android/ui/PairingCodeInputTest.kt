package dev.flowdrop.android.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PairingCodeInputTest {

    @Test
    fun `keeps leading zeros`() {
        assertEquals("0429", sanitizePairingCode("0429"))
        assertEquals("0000", sanitizePairingCode("0000"))
    }

    @Test
    fun `drops non digits`() {
        assertEquals("12", sanitizePairingCode("1a2 b"))
        assertEquals("", sanitizePairingCode("abc"))
        assertEquals("0429", sanitizePairingCode(" 0429 "))
        // Full-width digits from a CJK keyboard are not ASCII digits.
        assertEquals("", sanitizePairingCode("\uFF10\uFF11\uFF12\uFF13"))
    }

    @Test
    fun `keeps at most four digits`() {
        assertEquals("1234", sanitizePairingCode("12345"))
        assertEquals("1234", sanitizePairingCode("1234-5678"))
    }

    @Test
    fun `only four digits can be submitted`() {
        assertFalse(isPairingCodeComplete(""))
        assertFalse(isPairingCodeComplete("123"))
        assertFalse(isPairingCodeComplete("12345"))
        assertTrue(isPairingCodeComplete("1234"))
        assertTrue(isPairingCodeComplete("0007"))
    }

    @Test
    fun `sanitize then complete for padded codes`() {
        val typed = sanitizePairingCode("0")
        assertFalse(isPairingCodeComplete(typed))
        assertEquals("0", typed)
        val done = sanitizePairingCode("0012")
        assertTrue(isPairingCodeComplete(done))
        assertEquals("0012", done)
    }
}
