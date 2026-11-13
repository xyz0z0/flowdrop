package dev.flowdrop.android.ui

import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.flowdrop.android.ConnLabel
import dev.flowdrop.android.R
import dev.flowdrop.android.UiState
import dev.flowdrop.android.data.ConnState
import dev.flowdrop.android.data.Item
import dev.flowdrop.android.data.ItemCategory

/**
 * Inbox screen: connection status, device nickname, composer, item list with
 * paging, upload/download progress and transient toasts.
 *
 * Nothing here keeps the app awake or polls in the background; the Activity
 * tells the ViewModel when it is visible and the socket is closed otherwise.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun InboxScreen(
    state: UiState,
    composerText: String,
    imageStore: AuthenticatedImageStore,
    onComposerChange: (String) -> Unit,
    onSend: () -> Unit,
    onPickImages: (List<Uri>) -> Unit,
    onPickFiles: (List<Uri>) -> Unit,
    onCancelUpload: (String) -> Unit,
    onDismissUpload: (String) -> Unit,
    onRefresh: () -> Unit,
    onLoadMore: () -> Unit,
    onDelete: (Item) -> Unit,
    onSave: (Item) -> Unit,
    onInstallApk: (Item) -> Unit,
    onCancelDownload: (String) -> Unit,
    onDeviceNameChange: (String) -> Unit,
    onLogout: () -> Unit,
    onNotify: (String, Boolean) -> Unit,
    onCategoryChange: (String) -> Unit = {},
) {
    var editingDevice by remember { mutableStateOf(false) }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(stringResource(R.string.inbox_title))
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            ConnDot(state.conn)
                            Spacer(Modifier.width(5.dp))
                            Text(
                                ConnLabel.text(state.conn),
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                },
                actions = {
                    TextButton(onClick = onRefresh) { Text(stringResource(R.string.action_refresh)) }
                    TextButton(onClick = onLogout) { Text(stringResource(R.string.action_logout)) }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                ),
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .imePadding(),
        ) {
            DeviceBar(
                deviceName = state.deviceName,
                editing = editingDevice,
                onStartEdit = { editingDevice = true },
                onCommit = {
                    editingDevice = false
                    onDeviceNameChange(it)
                },
            )

            if (state.error != null) {
                Surface(color = MaterialTheme.colorScheme.errorContainer) {
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            state.error,
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onErrorContainer,
                            modifier = Modifier.weight(1f),
                        )
                        TextButton(onClick = onRefresh) { Text(stringResource(R.string.action_retry)) }
                    }
                }
            }

            CategoryFilters(
                selected = state.category,
                onSelect = onCategoryChange,
            )

            LazyColumn(
                modifier = Modifier.weight(1f).fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(8.dp),
                contentPadding = PaddingValues(
                    start = 12.dp,
                    end = 12.dp,
                    top = 4.dp,
                    bottom = 12.dp,
                ),
            ) {
                item(key = "composer") {
                    ComposerSection(
                        text = composerText,
                        sending = state.sendingText,
                        uploads = state.uploads,
                        maxUploadBytes = state.maxUploadBytes,
                        onTextChange = onComposerChange,
                        onSend = onSend,
                        onPickImages = onPickImages,
                        onPickFiles = onPickFiles,
                        onCancelUpload = onCancelUpload,
                        onDismissUpload = onDismissUpload,
                    )
                }

                if (state.loading && state.items.isEmpty()) {
                    item(key = "loading") {
                        Box(
                            modifier = Modifier.fillMaxWidth().padding(32.dp),
                            contentAlignment = Alignment.Center,
                        ) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                                Spacer(Modifier.width(10.dp))
                                Text(stringResource(R.string.loading))
                            }
                        }
                    }
                }

                if (!state.loading && state.items.isEmpty()) {
                    item(key = "empty") {
                        val filtered = state.category != ItemCategory.ALL
                        Column(
                            modifier = Modifier.fillMaxWidth().padding(32.dp),
                            horizontalAlignment = Alignment.CenterHorizontally,
                        ) {
                            Text(
                                stringResource(
                                    if (filtered) R.string.empty_category else R.string.empty_inbox,
                                ),
                                style = MaterialTheme.typography.titleMedium,
                            )
                            Spacer(Modifier.height(6.dp))
                            Text(
                                if (filtered) {
                                    stringResource(
                                        R.string.empty_category_hint,
                                        stringResource(categoryLabelRes(state.category)),
                                    )
                                } else {
                                    stringResource(R.string.composer_empty_hint)
                                },
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (filtered) {
                                Spacer(Modifier.height(8.dp))
                                TextButton(onClick = { onCategoryChange(ItemCategory.ALL) }) {
                                    Text(stringResource(R.string.category_show_all))
                                }
                            }
                        }
                    }
                }

                items(items = state.items, key = { it.id }) { item ->
                    val download = remember(item.id, state.downloads) {
                        state.downloads.firstOrNull { it.itemId == item.id }
                    }
                    ItemCard(
                        item = item,
                        imageStore = imageStore,
                        download = download,
                        onDelete = onDelete,
                        onSave = onSave,
                        onInstallApk = onInstallApk,
                        onCancelDownload = onCancelDownload,
                        onNotify = onNotify,
                    )
                }

                if (state.nextCursor.isNotEmpty()) {
                    item(key = "more") {
                        Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                            Button(onClick = onLoadMore, enabled = !state.loadingMore) {
                                if (state.loadingMore) {
                                    CircularProgressIndicator(
                                        modifier = Modifier.size(16.dp),
                                        strokeWidth = 2.dp,
                                    )
                                    Spacer(Modifier.width(8.dp))
                                }
                                Text(stringResource(R.string.action_load_more))
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun CategoryFilters(selected: String, onSelect: (String) -> Unit) {
    Surface(color = MaterialTheme.colorScheme.surface) {
        LazyRow(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            contentPadding = PaddingValues(horizontal = 4.dp),
        ) {
            items(items = ItemCategory.filters, key = { it }) { category ->
                FilterChip(
                    selected = category == selected,
                    onClick = { onSelect(category) },
                    label = { Text(stringResource(categoryLabelRes(category))) },
                )
            }
        }
    }
}

private fun categoryLabelRes(category: String): Int = when (category) {
    ItemCategory.TEXT -> R.string.category_text
    ItemCategory.LINK -> R.string.category_link
    ItemCategory.IMAGE -> R.string.category_image
    ItemCategory.VIDEO -> R.string.category_video
    ItemCategory.AUDIO -> R.string.category_audio
    ItemCategory.DOCUMENT -> R.string.category_document
    ItemCategory.ARCHIVE -> R.string.category_archive
    ItemCategory.OTHER -> R.string.category_other
    else -> R.string.category_all
}

@Composable
private fun DeviceBar(
    deviceName: String,
    editing: Boolean,
    onStartEdit: () -> Unit,
    onCommit: (String) -> Unit,
) {
    var draft by remember(deviceName, editing) { mutableStateOf(deviceName) }
    Surface(color = MaterialTheme.colorScheme.surface) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                stringResource(R.string.device_name_label),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.width(8.dp))
            if (editing) {
                OutlinedTextField(
                    value = draft,
                    onValueChange = { draft = it.take(64) },
                    singleLine = true,
                    modifier = Modifier.weight(1f),
                    textStyle = MaterialTheme.typography.bodyMedium,
                )
                TextButton(onClick = { onCommit(draft) }) { Text("保存") }
            } else {
                Text(
                    deviceName,
                    style = MaterialTheme.typography.bodyMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = onStartEdit) { Text("修改") }
            }
        }
    }
}

@Composable
private fun ConnDot(state: ConnState) {
    val color = when (state) {
        ConnState.OPEN -> MaterialTheme.colorScheme.primary
        ConnState.CONNECTING -> MaterialTheme.colorScheme.tertiary
        else -> MaterialTheme.colorScheme.error
    }
    Surface(color = color, shape = androidx.compose.foundation.shape.CircleShape) {
        Spacer(Modifier.size(7.dp))
    }
}
