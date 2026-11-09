package dev.flowdrop.android.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import dev.flowdrop.android.DownloadTask
import dev.flowdrop.android.R
import dev.flowdrop.android.TaskStatus
import dev.flowdrop.android.data.Item
import dev.flowdrop.android.device.SystemActions
import java.text.DateFormat
import java.util.Date

/**
 * One inbox entry: text/link body, image thumbnail, or file card, plus the
 * copy / open / download / install-APK / delete actions.
 */
@Composable
fun ItemCard(
    item: Item,
    imageStore: AuthenticatedImageStore,
    download: DownloadTask?,
    onDelete: (Item) -> Unit,
    onSave: (Item) -> Unit,
    onInstallApk: (Item) -> Unit,
    onCancelDownload: (String) -> Unit,
    onNotify: (String, Boolean) -> Unit,
) {
    val context = LocalContext.current
    var confirming by remember { mutableStateOf(false) }
    var enlarged by remember { mutableStateOf(false) }
    // Per-item: survives list recycling and returns with the same item id.
    var textExpanded by rememberSaveable(item.id) { mutableStateOf(false) }
    val clipboard = stringResource(R.string.action_copied)
    // Links keep their 3-line preview; only plain text entries can be folded.
    val longText = item.type == "text" && needsTextExpand(item.text.orEmpty())

    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp),
    ) {
        Column(modifier = Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                KindTag(item)
                Spacer(Modifier.width(8.dp))
                Text(
                    buildString {
                        if (item.senderDevice.isNotBlank()) append(item.senderDevice).append(" · ")
                        append(formatTime(item.createdAt))
                    },
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (longText) {
                    TextButton(
                        onClick = { textExpanded = !textExpanded },
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 0.dp),
                    ) {
                        Text(
                            stringResource(
                                if (textExpanded) R.string.action_collapse_text
                                else R.string.action_expand_text,
                            ),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                }
            }

            when {
                item.type == "text" || item.isLink -> {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        item.text.orEmpty(),
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = when {
                            item.isLink -> 3
                            textExpanded -> Int.MAX_VALUE
                            else -> 6
                        },
                        overflow = TextOverflow.Ellipsis,
                    )
                    if (item.isLink) {
                        Spacer(Modifier.height(2.dp))
                        Text(
                            "外部链接：FlowDrop 不校验其内容，打开前请确认来源可信。",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }

                item.isImage && item.blob != null -> {
                    Spacer(Modifier.height(8.dp))
                    val bitmap = rememberAuthenticatedImage(imageStore, item.id)
                    if (bitmap != null) {
                        Image(
                            bitmap = bitmap,
                            contentDescription = item.blob?.name,
                            contentScale = ContentScale.Fit,
                            modifier = Modifier
                                .fillMaxWidth()
                                .heightIn(max = 260.dp)
                                .clip(RoundedCornerShape(8.dp))
                                .clickable { enlarged = true },
                        )
                    } else {
                        Box(
                            modifier = Modifier
                                .fillMaxWidth()
                                .height(120.dp)
                                .clip(RoundedCornerShape(8.dp))
                                .background(MaterialTheme.colorScheme.surfaceVariant),
                            contentAlignment = Alignment.Center,
                        ) {
                            Text(
                                stringResource(R.string.loading),
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    Spacer(Modifier.height(4.dp))
                    Text(
                        buildString {
                            append(item.blob?.name.orEmpty())
                            append(" · ")
                            append(formatBytes(item.blob?.sizeBytes ?: 0))
                        },
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }

                item.blob != null -> {
                    Spacer(Modifier.height(8.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(if (item.isApk) "📦" else "📄", style = MaterialTheme.typography.titleLarge)
                        Spacer(Modifier.width(8.dp))
                        Column {
                            Text(
                                item.blob?.name.orEmpty(),
                                style = MaterialTheme.typography.bodyMedium,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Text(
                                buildString {
                                    append(formatBytes(item.blob?.sizeBytes ?: 0))
                                    if (item.isApk) append(" · ").append("Android 安装包")
                                },
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }

            if (download != null && download.status == TaskStatus.RUNNING) {
                Spacer(Modifier.height(8.dp))
                if (download.total != null && download.total!! > 0) {
                    LinearProgressIndicator(
                        progress = { download.fraction },
                        modifier = Modifier.fillMaxWidth().height(4.dp),
                    )
                } else {
                    LinearProgressIndicator(modifier = Modifier.fillMaxWidth().height(4.dp))
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        stringResource(R.string.download_progress, (download.fraction * 100).toInt()),
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(onClick = { onCancelDownload(download.key) }) {
                        Text(stringResource(R.string.action_cancel))
                    }
                }
            }
            if (download != null && download.status == TaskStatus.FAILED) {
                Text(
                    download.error ?: stringResource(R.string.error_download_failed),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }

            Spacer(Modifier.height(8.dp))
            Row(
                horizontalArrangement = Arrangement.spacedBy(0.dp),
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth(),
            ) {
                if (item.type == "text" || item.isLink) {
                    TextButton(onClick = {
                        val ok = SystemActions.copy(context, "FlowDrop", item.text.orEmpty())
                        onNotify(if (ok) clipboard else "复制失败", !ok)
                    }) { Text(stringResource(R.string.action_copy)) }
                }
                if (item.isLink && !item.text.isNullOrBlank()) {
                    TextButton(onClick = {
                        if (!SystemActions.openLink(context, item.text!!)) onNotify("无法打开链接", true)
                    }) { Text(stringResource(R.string.action_open_link)) }
                }
                if (item.blob != null) {
                    TextButton(
                        onClick = { onSave(item) },
                        enabled = download?.status != TaskStatus.RUNNING,
                    ) { Text(stringResource(R.string.action_download)) }
                    if (item.isApk) {
                        TextButton(
                            onClick = { onInstallApk(item) },
                            enabled = download?.status != TaskStatus.RUNNING,
                        ) { Text(stringResource(R.string.action_install_apk)) }
                    }
                }
                Spacer(Modifier.weight(1f))
                if (confirming) {
                    TextButton(onClick = {
                        confirming = false
                        onDelete(item)
                    }) {
                        Text(
                            stringResource(R.string.action_confirm_delete),
                            color = MaterialTheme.colorScheme.error,
                        )
                    }
                    TextButton(onClick = { confirming = false }) {
                        Text(stringResource(R.string.action_cancel))
                    }
                } else {
                    TextButton(onClick = { confirming = true }) {
                        Text(
                            stringResource(R.string.action_delete),
                            color = MaterialTheme.colorScheme.error,
                        )
                    }
                }
            }
        }
    }

    if (enlarged && item.isImage) {
        Dialog(onDismissRequest = { enlarged = false }) {
            Surface(
                shape = RoundedCornerShape(12.dp),
                color = MaterialTheme.colorScheme.surface,
            ) {
                Column(modifier = Modifier.padding(12.dp)) {
                    val bitmap = rememberAuthenticatedImage(imageStore, item.id)
                    if (bitmap != null) {
                        Image(
                            bitmap = bitmap,
                            contentDescription = item.blob?.name,
                            contentScale = ContentScale.Fit,
                            modifier = Modifier.fillMaxWidth().heightIn(max = 520.dp),
                        )
                    }
                    Spacer(Modifier.height(8.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            item.blob?.name.orEmpty(),
                            style = MaterialTheme.typography.labelMedium,
                            modifier = Modifier.weight(1f),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        TextButton(onClick = { enlarged = false }) {
                            Text(stringResource(R.string.action_cancel))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun KindTag(item: Item) {
    val (label, color) = when {
        item.isApk -> stringResource(R.string.tag_apk) to MaterialTheme.colorScheme.secondary
        item.type == "link" -> stringResource(R.string.tag_link) to MaterialTheme.colorScheme.primary
        item.type == "image" -> stringResource(R.string.tag_image) to MaterialTheme.colorScheme.primary
        item.type == "file" -> stringResource(R.string.tag_file) to MaterialTheme.colorScheme.secondary
        else -> stringResource(R.string.tag_text) to MaterialTheme.colorScheme.secondary
    }
    Surface(
        shape = RoundedCornerShape(6.dp),
        color = color.copy(alpha = 0.12f),
    ) {
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = color,
            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
        )
    }
}

fun formatTime(epochMillis: Long): String {
    if (epochMillis <= 0) return ""
    return DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT)
        .format(Date(epochMillis))
}
