package dev.flowdrop.android.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.flowdrop.android.R
import dev.flowdrop.android.TaskStatus
import dev.flowdrop.android.UploadTask

/**
 * Text/link composer plus image & file pickers and the upload progress rows.
 *
 * Picked URIs are content URIs from the system document picker, so the app
 * never needs broad storage permission.
 */
@Composable
fun ComposerSection(
    text: String,
    sending: Boolean,
    uploads: List<UploadTask>,
    maxUploadBytes: Long,
    onTextChange: (String) -> Unit,
    onSend: () -> Unit,
    onPickImages: (List<Uri>) -> Unit,
    onPickFiles: (List<Uri>) -> Unit,
    onCancelUpload: (String) -> Unit,
    onDismissUpload: (String) -> Unit,
) {
    val imagePicker = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments(),
    ) { uris -> if (uris.isNotEmpty()) onPickImages(uris) }

    val filePicker = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments(),
    ) { uris -> if (uris.isNotEmpty()) onPickFiles(uris) }

    Column(modifier = Modifier.fillMaxWidth().padding(top = 2.dp, bottom = 2.dp)) {
        // One card owns the text field and its toolbar, so the composer reads as
        // a single rounded input like the web composer instead of loose rows.
        Surface(
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(14.dp),
            color = MaterialTheme.colorScheme.surface,
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 8.dp)) {
                OutlinedTextField(
                    value = text,
                    onValueChange = onTextChange,
                    modifier = Modifier.fillMaxWidth(),
                    placeholder = { Text(stringResource(R.string.composer_hint)) },
                    minLines = 2,
                    maxLines = 6,
                    enabled = !sending,
                    // The outer Surface is the real container; a second border
                    // inside it would only add visual noise.
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = Color.Transparent,
                        unfocusedBorderColor = Color.Transparent,
                        disabledBorderColor = Color.Transparent,
                    ),
                )
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
                ) {
                    OutlinedButton(
                        onClick = { imagePicker.launch(arrayOf("image/*")) },
                        enabled = !sending,
                        shape = RoundedCornerShape(999.dp),
                        contentPadding = PaddingValues(horizontal = 10.dp, vertical = 0.dp),
                        modifier = Modifier.height(34.dp),
                    ) {
                        ImageGlyph(color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Spacer(Modifier.width(6.dp))
                        Text(
                            stringResource(R.string.composer_pick_image),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                    Spacer(Modifier.width(6.dp))
                    OutlinedButton(
                        onClick = { filePicker.launch(arrayOf("*/*")) },
                        enabled = !sending,
                        shape = RoundedCornerShape(999.dp),
                        contentPadding = PaddingValues(horizontal = 10.dp, vertical = 0.dp),
                        modifier = Modifier.height(34.dp),
                    ) {
                        ClipGlyph(color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Spacer(Modifier.width(6.dp))
                        Text(
                            stringResource(R.string.composer_pick_file),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                    Spacer(Modifier.weight(1f))
                    Button(
                        onClick = onSend,
                        enabled = text.isNotBlank() && !sending,
                        shape = RoundedCornerShape(999.dp),
                        contentPadding = PaddingValues(horizontal = 14.dp, vertical = 0.dp),
                        modifier = Modifier.height(34.dp),
                    ) {
                        PlaneGlyph(color = MaterialTheme.colorScheme.onPrimary)
                        Spacer(Modifier.width(6.dp))
                        Text(
                            stringResource(R.string.composer_send),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                }
            }
        }
        Text(
            text = "单个文件上限 " + formatBytes(maxUploadBytes) + "；多选文件逐个上传，每个文件一个条目。",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 6.dp, start = 2.dp),
        )

        if (uploads.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            uploads.forEach { task ->
                UploadRow(task, onCancelUpload, onDismissUpload)
            }
        }
    }
}

/** Lightweight line icon: image frame with a horizon and a small sun. */
@Composable
private fun ImageGlyph(color: Color) {
    Canvas(modifier = Modifier.size(14.dp)) {
        val w = size.width
        val h = size.height
        val stroke = Stroke(width = w * 0.11f, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val frame = Path().apply {
            moveTo(w * 0.20f, h * 0.16f)
            lineTo(w * 0.80f, h * 0.16f)
            lineTo(w * 0.80f, h * 0.84f)
            lineTo(w * 0.20f, h * 0.84f)
            close()
            moveTo(w * 0.22f, h * 0.68f)
            lineTo(w * 0.42f, h * 0.46f)
            lineTo(w * 0.58f, h * 0.62f)
        }
        drawPath(path = frame, color = color, style = stroke)
        drawCircle(color = color, radius = w * 0.07f, center = Offset(w * 0.40f, h * 0.34f))
    }
}

/** Lightweight line icon: paperclip. */
@Composable
private fun ClipGlyph(color: Color) {
    Canvas(modifier = Modifier.size(14.dp)) {
        val w = size.width
        val h = size.height
        val stroke = Stroke(width = w * 0.11f, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val clip = Path().apply {
            moveTo(w * 0.70f, h * 0.28f)
            lineTo(w * 0.34f, h * 0.64f)
            quadraticTo(w * 0.22f, h * 0.76f, w * 0.36f, h * 0.86f)
            quadraticTo(w * 0.52f, h * 0.96f, w * 0.62f, h * 0.80f)
            lineTo(w * 0.84f, h * 0.48f)
            quadraticTo(w * 0.92f, h * 0.36f, w * 0.80f, h * 0.26f)
            quadraticTo(w * 0.68f, h * 0.16f, w * 0.56f, h * 0.28f)
            lineTo(w * 0.30f, h * 0.56f)
        }
        drawPath(path = clip, color = color, style = stroke)
    }
}

/** Lightweight line icon: paper plane, matching the send action. */
@Composable
private fun PlaneGlyph(color: Color) {
    Canvas(modifier = Modifier.size(14.dp)) {
        val w = size.width
        val h = size.height
        val stroke = Stroke(width = w * 0.11f, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val plane = Path().apply {
            moveTo(w * 0.12f, h * 0.50f)
            lineTo(w * 0.88f, h * 0.16f)
            lineTo(w * 0.64f, h * 0.84f)
            lineTo(w * 0.48f, h * 0.58f)
            close()
            moveTo(w * 0.48f, h * 0.58f)
            lineTo(w * 0.88f, h * 0.16f)
        }
        drawPath(path = plane, color = color, style = stroke)
    }
}

@Composable
private fun UploadRow(task: UploadTask, onCancel: (String) -> Unit, onDismiss: (String) -> Unit) {
    Column(modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                task.name,
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            when (task.status) {
                TaskStatus.RUNNING -> TextButton(onClick = { onCancel(task.key) }) {
                    Text(stringResource(R.string.action_cancel))
                }
                else -> TextButton(onClick = { onDismiss(task.key) }) { Text("关闭") }
            }
        }
        when (task.status) {
            TaskStatus.RUNNING -> {
                if (task.size > 0) {
                    LinearProgressIndicator(
                        progress = { task.fraction },
                        modifier = Modifier.fillMaxWidth().height(4.dp),
                    )
                } else {
                    LinearProgressIndicator(modifier = Modifier.fillMaxWidth().height(4.dp))
                }
                Text(
                    stringResource(R.string.upload_progress, (task.fraction * 100).toInt()) +
                        "  " + formatBytes(task.loaded) + " / " + formatBytes(task.size),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            TaskStatus.DONE -> Text(
                "已发送",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.primary,
            )
            TaskStatus.FAILED -> {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        task.error ?: stringResource(R.string.error_network),
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.weight(1f),
                    )
                }
                Spacer(Modifier.size(2.dp))
                Text(
                    "超过上限会被 Hub 拒绝；可重新选择文件后重试。",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** Human-readable byte size (binary units), matching the web UI. */
fun formatBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val units = arrayOf("KiB", "MiB", "GiB", "TiB")
    var value = bytes.toDouble()
    var index = -1
    while (value >= 1024 && index < units.lastIndex) {
        value /= 1024
        index++
    }
    val rounded = if (value >= 100) "%.0f" else "%.1f"
    return String.format(java.util.Locale.US, "$rounded %s", value, units[index])
}
