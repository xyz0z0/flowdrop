package dev.flowdrop.android.ui

import android.net.Uri
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.clickable
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
import androidx.compose.material3.AlertDialog
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
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
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
import dev.flowdrop.android.DownloadKind
import dev.flowdrop.android.R
import dev.flowdrop.android.UiState
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
    onRefreshPresence: () -> Unit = {},
    // Multi-computer entries. Defaults keep existing callers/tests compiling.
    onSelectHub: (String) -> Unit = {},
    onBeginAddHub: () -> Unit = {},
    // The queue dialog itself is hosted above the phase switch, so the screen
    // only asks for it: one owner for its state, retry and removal callbacks.
    onOpenOutbox: () -> Unit = {},
) {
    var editingDevice by remember { mutableStateOf(false) }
    var onlineListOpen by remember { mutableStateOf(false) }
    var unpairOpen by remember { mutableStateOf(false) }
    var hubListOpen by remember { mutableStateOf(false) }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            // The title bar keeps the screen name and its actions: the current
            // computer, its link state and "切换" live on the device bar below.
            TopAppBar(
                title = {
                    Text(
                        stringResource(R.string.inbox_title),
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                actions = {
                    TextButton(
                        onClick = {
                            onlineListOpen = true
                            // Opening the list is a refresh trigger; the dialog
                            // itself never polls.
                            onRefreshPresence()
                        },
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                    ) {
                        Text(
                            // A count is only shown for a fresh snapshot on a
                            // live socket; otherwise the entry stays unnamed.
                            if (state.presenceStale) {
                                stringResource(R.string.presence_entry)
                            } else {
                                stringResource(R.string.presence_count, state.onlineDevices.size)
                            },
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                    TextButton(
                        onClick = onRefresh,
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                    ) {
                        Text(
                            stringResource(R.string.action_refresh),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                    TextButton(
                        onClick = { unpairOpen = true },
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                    ) {
                        Text(
                            stringResource(R.string.action_logout),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
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
            // Device bar: one row for the computer being shown, its state and
            // the switch entry, instead of crowding them into the title bar.
            InboxDeviceBar(
                state = state,
                onOpenHubs = { hubListOpen = true },
                onEditDeviceName = { editingDevice = true },
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

            PullToRefreshBox(
                isRefreshing = state.loading && state.items.isNotEmpty(),
                onRefresh = onRefresh,
                modifier = Modifier.weight(1f).fillMaxWidth(),
            ) {
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
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

                    // Queued sends are part of composing, not of the received
                    // list: the entry sits right under the composer and only
                    // exists while something is actually waiting.
                    if (state.outbox.isNotEmpty()) {
                        item(key = "outbox") {
                            OutboxQueueEntry(
                                count = state.outbox.size,
                                onClick = onOpenOutbox,
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    }

                    item(key = "categories") {
                        CategoryFilters(
                            selected = state.category,
                            onSelect = onCategoryChange,
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
                        val downloads = remember(item.id, state.downloads) {
                            state.downloads.filter { it.itemId == item.id }
                        }
                        Box(
                            modifier = Modifier.animateItem(
                                fadeInSpec = tween(
                                    durationMillis = 120,
                                    easing = LinearOutSlowInEasing,
                                ),
                                fadeOutSpec = tween(
                                    durationMillis = 120,
                                    easing = LinearEasing,
                                ),
                            ),
                        ) {
                            ItemCard(
                                item = item,
                                imageStore = imageStore,
                                // Saving and installing are separate transfers of the
                                // same attachment; keep their states apart.
                                saveDownload = downloads.firstOrNull { it.kind == DownloadKind.SAVE },
                                installDownload = downloads.firstOrNull { it.kind == DownloadKind.INSTALL },
                                onDelete = onDelete,
                                onSave = onSave,
                                onInstallApk = onInstallApk,
                                onCancelDownload = onCancelDownload,
                                onNotify = onNotify,
                            )
                        }
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

    if (editingDevice) {
        DeviceNameDialog(
            deviceName = state.deviceName,
            onConfirm = {
                editingDevice = false
                onDeviceNameChange(it)
            },
            onDismiss = { editingDevice = false },
        )
    }

    if (onlineListOpen) {
        PresenceDialog(
            state = state,
            onRefresh = onRefreshPresence,
            onDismiss = { onlineListOpen = false },
        )
    }

    if (hubListOpen && state.savedHubs.isNotEmpty()) {
        HubPickerDialog(
            hubs = state.savedHubs,
            activeHubId = state.activeHubId,
            onSelect = { hubId ->
                hubListOpen = false
                onSelectHub(hubId)
            },
            onAddHub = {
                hubListOpen = false
                onBeginAddHub()
            },
            onDismiss = { hubListOpen = false },
        )
    }

    if (unpairOpen) {
        UnpairDialog(
            onConfirm = {
                unpairOpen = false
                onLogout()
            },
            onDismiss = { unpairOpen = false },
        )
    }
}

/**
 * Strip under the inbox title bar: the current computer (tap to switch), its
 * connection state and the editable local device name.
 *
 * The long name keeps one line and ellipsizes, so a narrow screen still shows
 * the switch affordance; the device name stays next to the state it qualifies.
 */
@Composable
private fun InboxDeviceBar(
    state: UiState,
    onOpenHubs: () -> Unit,
    onEditDeviceName: () -> Unit,
) {
    Surface(color = MaterialTheme.colorScheme.surface, modifier = Modifier.fillMaxWidth()) {
        CurrentHubEntry(
            hubName = state.hubName,
            conn = state.conn,
            onOpen = onOpenHubs,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp),
            secondaryContent = {
                if (state.deviceName.isNotBlank()) {
                    Text(
                        " · ",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        state.deviceName,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.primary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier
                            .clickable(onClick = onEditDeviceName)
                            .padding(horizontal = 2.dp, vertical = 2.dp),
                    )
                }
            },
        )
    }
}

/**
 * Second confirmation for unpairing.
 *
 * Dismissing (cancel, back, tap outside) only closes the dialog; the original
 * `onLogout` path runs at most once, after the dialog is gone.
 */
@Composable
private fun UnpairDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.unpair_title)) },
        text = { Text(stringResource(R.string.unpair_message)) },
        confirmButton = {
            TextButton(onClick = onConfirm) { Text(stringResource(R.string.action_logout)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}

/**
 * Read-only online list: names plus a "本机" marker.
 *
 * When the snapshot is not fresh, or the socket is down, the list is presented
 * as "state pending update" instead of a live count: the Hub can only notice a
 * dead connection after its ping timeout, so a count is never a promise.
 */
@Composable
private fun PresenceDialog(
    state: UiState,
    onRefresh: () -> Unit,
    onDismiss: () -> Unit,
) {
    val stale = state.presenceStale
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.presence_title)) },
        text = {
            Column {
                when {
                    state.onlineDevices.isEmpty() && state.presenceLoading -> Row(
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
                        Spacer(Modifier.width(10.dp))
                        Text(stringResource(R.string.loading))
                    }

                    state.onlineDevices.isEmpty() -> Text(
                        stringResource(
                            if (stale) R.string.presence_unknown else R.string.presence_empty,
                        ),
                        style = MaterialTheme.typography.bodyMedium,
                    )

                    else -> state.onlineDevices.forEach { device ->
                        Row(
                            modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(
                                device.name.ifBlank { stringResource(R.string.presence_unnamed) },
                                style = MaterialTheme.typography.bodyMedium,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                                modifier = Modifier.weight(1f),
                            )
                            if (device.current) {
                                Spacer(Modifier.width(8.dp))
                                Text(
                                    stringResource(R.string.presence_current),
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.primary,
                                )
                            }
                        }
                    }
                }

                if (stale) {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        stringResource(R.string.presence_stale_hint),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                val failure = state.presenceError
                if (failure != null) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        stringResource(R.string.presence_refresh_failed, failure),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_close)) }
        },
        dismissButton = {
            TextButton(onClick = onRefresh, enabled = !state.presenceLoading) {
                Text(stringResource(R.string.action_presence_refresh))
            }
        },
    )
}

@Composable
private fun CategoryFilters(selected: String, onSelect: (String) -> Unit) {
    // Lives inside the list content padding, so it needs no extra outer margin:
    // the chips read as the first row under the composer card.
    LazyRow(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        contentPadding = PaddingValues(vertical = 2.dp),
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

private fun categoryLabelRes(category: String): Int = when (category) {
    ItemCategory.TEXT -> R.string.category_text
    ItemCategory.LINK -> R.string.category_link
    ItemCategory.IMAGE -> R.string.category_image
    ItemCategory.VIDEO -> R.string.category_video
    ItemCategory.AUDIO -> R.string.category_audio
    ItemCategory.DOCUMENT -> R.string.category_document
    ItemCategory.ARCHIVE -> R.string.category_archive
    ItemCategory.INSTALLER -> R.string.category_installer
    ItemCategory.OTHER -> R.string.category_other
    else -> R.string.category_all
}

@Composable
private fun DeviceNameDialog(
    deviceName: String,
    onConfirm: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    var draft by remember(deviceName) { mutableStateOf(deviceName) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.device_name_label)) },
        text = {
            OutlinedTextField(
                value = draft,
                onValueChange = { draft = it.take(64) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                textStyle = MaterialTheme.typography.bodyMedium,
            )
        },
        confirmButton = {
            TextButton(onClick = { onConfirm(draft) }) { Text("保存") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
        },
    )
}
