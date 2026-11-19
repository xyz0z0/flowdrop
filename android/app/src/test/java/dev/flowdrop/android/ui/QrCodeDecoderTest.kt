package dev.flowdrop.android.ui

import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.common.BitMatrix
import com.google.zxing.qrcode.QRCodeWriter
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Verifies the pure decode path used by [QrScannerScreen]: a luminance plane in,
 * the QR text out. The rendered frame comes from zxing's own writer, so this
 * covers the CameraX-free half of the scanner (the camera stream itself needs a
 * device).
 */
class QrCodeDecoderTest {

    private fun qrMatrix(text: String, size: Int = 480): BitMatrix =
        QRCodeWriter().encode(
            text,
            BarcodeFormat.QR_CODE,
            size,
            size,
            mapOf(EncodeHintType.MARGIN to 2),
        )

    /** Black pixels are 0x00 luminance, white pixels 0xFF, one byte per pixel. */
    private fun BitMatrix.toLuminance(): ByteArray {
        val bytes = ByteArray(width * height)
        for (y in 0 until height) {
            for (x in 0 until width) {
                bytes[y * width + x] = if (get(x, y)) 0x00 else 0xFF.toByte()
            }
        }
        return bytes
    }

    /** 90-degree rotation, the way a phone held sideways delivers its frames. */
    private fun rotate90(source: ByteArray, width: Int, height: Int): ByteArray {
        val bytes = ByteArray(source.size)
        for (y in 0 until height) {
            for (x in 0 until width) {
                bytes[x * height + (height - 1 - y)] = source[y * width + x]
            }
        }
        return bytes
    }

    @Test
    fun `decodes a pairing payload`() {
        val text = "flowdrop://pair?v=1&code=0429&url=http%3A%2F%2F192.168.1.10%3A8080"
        val matrix = qrMatrix(text)
        val bytes = matrix.toLuminance()

        assertEquals(
            text,
            decodeQrFromLuminancePlane(
                yPlane = bytes,
                dataWidth = matrix.width,
                dataHeight = matrix.height,
                width = matrix.width,
                height = matrix.height,
            ),
        )
    }

    @Test
    fun `decodes a rotated frame`() {
        val text = "flowdrop://pair?v=1&code=0007"
        val matrix = qrMatrix(text)
        val width = matrix.width
        val height = matrix.height
        val rotated = rotate90(matrix.toLuminance(), width, height)

        assertEquals(
            text,
            decodeQrFromLuminancePlane(
                yPlane = rotated,
                dataWidth = height,
                dataHeight = width,
                width = height,
                height = width,
            ),
        )
    }

    @Test
    fun `padded row stride is handled`() {
        val text = "flowdrop://pair?v=1&code=1234"
        val matrix = qrMatrix(text)
        val width = matrix.width
        val height = matrix.height
        val stride = width + 24
        val padded = ByteArray(stride * height) { 0xFF.toByte() }
        for (y in 0 until height) {
            for (x in 0 until width) {
                padded[y * stride + x] = if (matrix.get(x, y)) 0x00 else 0xFF.toByte()
            }
        }

        assertEquals(
            text,
            decodeQrFromLuminancePlane(
                yPlane = padded,
                dataWidth = stride,
                dataHeight = height,
                width = width,
                height = height,
            ),
        )
    }

    @Test
    fun `a frame without a code yields null`() {
        val width = 320
        val height = 240
        val blank = ByteArray(width * height) { 0xFF.toByte() }

        assertNull(
            decodeQrFromLuminancePlane(
                yPlane = blank,
                dataWidth = width,
                dataHeight = height,
                width = width,
                height = height,
            ),
        )
    }

    @Test
    fun `malformed geometry yields null instead of throwing`() {
        val bytes = ByteArray(16)
        assertNull(decodeQrFromLuminancePlane(bytes, 4, 4, 8, 8))
        assertNull(decodeQrFromLuminancePlane(bytes, 4, 4, 0, 0))
        assertNull(decodeQrFromLuminancePlane(ByteArray(0), 10, 10, 10, 10))
    }
}
