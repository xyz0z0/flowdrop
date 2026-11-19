package dev.flowdrop.android.ui

import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.ChecksumException
import com.google.zxing.DecodeHintType
import com.google.zxing.FormatException
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer

/**
 * Decodes a QR code out of the luminance (Y) plane of a YUV_420_888 frame.
 *
 * Kept free of CameraX and Android types so it can be unit tested on the JVM.
 * Only QR symbols are considered: the pairing payload is a QR code, and a
 * wider format list would just cost frames. QR detection is rotation
 * invariant, so the caller does not have to rotate the frame.
 *
 * @param yPlane luminance bytes, as delivered by `ImageProxy.planes[0].buffer`.
 * @param dataWidth row stride of [yPlane] in bytes (may exceed [width]).
 * @param dataHeight number of rows in [yPlane].
 * @param width visible frame width.
 * @param height visible frame height.
 * @return the decoded text, or null when this frame holds no readable QR code.
 */
fun decodeQrFromLuminancePlane(
    yPlane: ByteArray,
    dataWidth: Int,
    dataHeight: Int,
    width: Int,
    height: Int,
): String? {
    if (width <= 0 || height <= 0) return null
    if (dataWidth < width || dataHeight < height) return null
    if (yPlane.size < dataWidth * dataHeight) return null

    val source = PlanarYUVLuminanceSource(
        yPlane,
        dataWidth,
        dataHeight,
        0,
        0,
        width,
        height,
        false,
    )
    val reader = MultiFormatReader()
    reader.setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE)))
    return try {
        reader.decodeWithState(BinaryBitmap(HybridBinarizer(source))).text
    } catch (e: NotFoundException) {
        null
    } catch (e: ChecksumException) {
        null
    } catch (e: FormatException) {
        null
    } finally {
        reader.reset()
    }
}
