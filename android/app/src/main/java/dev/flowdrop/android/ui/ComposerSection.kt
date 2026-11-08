package dev.flowdrop.android.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Button
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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

    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp)) {
        OutlinedTextField(
            value = text,
            onValueChange = onTextChange,
            modifier = Modifier.fillMaxWidth(),
            placeholder = { Text(stringResource(R.string.composer_hint)) },
            minLines = 2,
            maxLines = 6,
            enabled = !sending,
        )
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Button(onClick = onSend, enabled = text.isNotBlank() && !sending) {
                Text(stringResource(R.string.composer_send))
            }
            Spacer(Modifier.width(8.dp))
            OutlinedButton(onClick = { imagePicker.launch(arrayOf("image/*")) }, enabled = !sending) {
                Text(stringResource(R.string.composer_pick_image))
            }
            Spacer(Modifier.width(8.dp))
            OutlinedButton(onClick = { filePicker.launch(arrayOf("*/*")) }, enabled = !sending) {
                Text(stringResource(R.string.composer_pick_file))
            }
        }
        Text(
            text = "单个文件上限 " + formatBytes(maxUploadBytes) + "；多选文件逐个上传，每个文件一个条目。",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 6.dp),
        )

        if (uploads.isNotEmpty()) {
            Spacer(Modifier.height(10.dp))
            uploads.forEach { task ->
                UploadRow(task, onCancelUpload, onDismissUpload)
            }
        }
    }
}

@Composable
private fun UploadRow(task: UploadTask, onCancel: (String) -> Unit, onDismiss: (String) -> Unit) {
    Column(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
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
