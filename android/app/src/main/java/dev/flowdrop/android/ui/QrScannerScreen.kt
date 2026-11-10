package dev.flowdrop.android.ui

import android.graphics.ImageFormat
import android.util.Size
import androidx.activity.compose.BackHandler
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import dev.flowdrop.android.R
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.delay

/** How long the scanner runs without a hit before it admits nothing was found. */
private const val NO_RESULT_HINT_DELAY_MS = 8_000L

/**
 * Full-screen QR scanner for the pairing payload.
 *
 * CameraX preview plus one analysis stream; the first readable QR code stops
 * the analysis and hands the raw text to [onDecoded]. Nothing is parsed,
 * logged or persisted here, and the raw payload is never rendered: the only
 * visible states are "scanning", "nothing recognised yet" and "camera
 * unavailable". Back and the close button cancel without touching the form.
 */
@Composable
fun QrScannerScreen(
    onDecoded: (String) -> Unit,
    onCancel: () -> Unit,
) {
    BackHandler(onBack = onCancel)

    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val currentOnDecoded by rememberUpdatedState(onDecoded)
    val previewView = remember {
        PreviewView(context).apply { scaleType = PreviewView.ScaleType.FILL_CENTER }
    }
    val decoded = remember { AtomicBoolean(false) }
    var cameraUnavailable by remember { mutableStateOf(false) }
    var nothingRecognised by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        delay(NO_RESULT_HINT_DELAY_MS)
        if (!decoded.get()) nothingRecognised = true
    }

    DisposableEffect(context, lifecycleOwner) {
        val executor = Executors.newSingleThreadExecutor()
        val mainExecutor = ContextCompat.getMainExecutor(context)
        val providerFuture = ProcessCameraProvider.getInstance(context)
        val analysis = ImageAnalysis.Builder()
            .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
            // QR codes are small; 720p frames keep them readable without the
            // per-frame cost of a full-resolution stream.
            .setResolutionSelector(
                ResolutionSelector.Builder()
                    .setResolutionStrategy(
                        ResolutionStrategy(
                            Size(1280, 720),
                            ResolutionStrategy.FALLBACK_RULE_CLOSEST_HIGHER_THEN_LOWER,
                        ),
                    )
                    .build(),
            )
            .build()
        analysis.setAnalyzer(executor) { image ->
            var text: String? = null
            try {
                if (!decoded.get()) {
                    text = runCatching { decodeFrame(image) }.getOrNull()
                }
            } finally {
                image.close()
            }
            if (text != null && decoded.compareAndSet(false, true)) {
                mainExecutor.execute { currentOnDecoded(text) }
            }
        }

        providerFuture.addListener({
            try {
                val provider = providerFuture.get()
                val preview = Preview.Builder().build().apply {
                    setSurfaceProvider(previewView.surfaceProvider)
                }
                provider.unbindAll()
                provider.bindToLifecycle(
                    lifecycleOwner,
                    CameraSelector.DEFAULT_BACK_CAMERA,
                    preview,
                    analysis,
                )
            } catch (e: Exception) {
                // No camera (or it is busy): keep the screen usable and tell the
                // user to fall back to the manual pairing form.
                cameraUnavailable = true
            }
        }, mainExecutor)

        onDispose {
            analysis.clearAnalyzer()
            runCatching { providerFuture.get().unbindAll() }
            executor.shutdown()
        }
    }

    Surface(modifier = Modifier.fillMaxSize(), color = Color.Black) {
        Box(modifier = Modifier.fillMaxSize()) {
            AndroidView(factory = { previewView }, modifier = Modifier.fillMaxSize())

            Text(
                stringResource(R.string.scan_title),
                style = MaterialTheme.typography.titleMedium,
                color = Color.White,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .statusBarsPadding()
                    .padding(top = 16.dp),
            )
            TextButton(
                onClick = onCancel,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .statusBarsPadding()
                    .padding(top = 4.dp, end = 8.dp),
            ) {
                Text(stringResource(R.string.action_close), color = Color.White)
            }

            Column(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .background(Color.Black.copy(alpha = 0.6f))
                    .padding(horizontal = 20.dp, vertical = 16.dp),
            ) {
                Text(
                    stringResource(R.string.scan_hint),
                    style = MaterialTheme.typography.bodyMedium,
                    color = Color.White,
                )
                if (nothingRecognised && !cameraUnavailable) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        stringResource(R.string.scan_no_result),
                        style = MaterialTheme.typography.bodyMedium,
                        color = Color(0xFFFFB4AB),
                    )
                }
                if (cameraUnavailable) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        stringResource(R.string.scan_camera_unavailable),
                        style = MaterialTheme.typography.bodyMedium,
                        color = Color(0xFFFFB4AB),
                    )
                }
                Spacer(Modifier.height(8.dp))
                Text(
                    stringResource(R.string.scan_no_auto_pair),
                    style = MaterialTheme.typography.labelMedium,
                    color = Color.White.copy(alpha = 0.85f),
                )
                Spacer(Modifier.height(12.dp))
                Button(onClick = onCancel, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(R.string.action_cancel))
                }
            }
        }
    }
}

/**
 * Turns one analysis frame into QR text.
 *
 * Only the luminance plane is read: QR decoding is greyscale anyway, and the
 * chroma planes would be wasted copies. Frames of an unexpected format simply
 * yield null instead of failing the analyzer.
 */
private fun decodeFrame(image: ImageProxy): String? {
    if (image.format != ImageFormat.YUV_420_888) return null
    val plane = image.planes.firstOrNull() ?: return null
    val buffer = plane.buffer
    val bytes = ByteArray(buffer.remaining())
    buffer.get(bytes)
    if (bytes.isEmpty()) return null

    // The Y plane is `rowStride` bytes wide; the visible frame is `image.width`.
    // Some devices hand out a final row that is shorter than the stride, so the
    // usable height is derived from the buffer instead of assumed.
    val dataWidth = plane.rowStride
    if (dataWidth <= 0) return null
    val dataHeight = minOf(image.height, bytes.size / dataWidth)
    if (dataHeight <= 0) return null
    val width = minOf(image.width, dataWidth)
    if (width <= 0) return null

    return decodeQrFromLuminancePlane(
        yPlane = bytes,
        dataWidth = dataWidth,
        dataHeight = dataHeight,
        width = width,
        height = dataHeight,
    )
}
