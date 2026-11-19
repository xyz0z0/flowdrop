package dev.flowdrop.android.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import dev.flowdrop.android.R
import dev.flowdrop.android.SavedHub
import dev.flowdrop.android.data.OutboxEntry
import dev.flowdrop.android.data.OutboxStatus

/**
 * Compact "待发送 (N)" entry, shown only while something is queued.
 *
 * The same entry is used next to the inbox composer and on the pairing screen:
 * queued items exist across computers, so they must stay reachable even when
 * no computer is currently connected (or paired) at all.
 */
@Composable
fun OutboxQueueEntry(
    count: Int,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    if (count <= 0) return
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.End,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        TextButton(
            onClick = onClick,
            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
        ) {
            Text(
                stringResource(R.string.outbox_entry, count),
                style = MaterialTheme.typography.labelMedium,
            )
        }
    }
}

/**
 * Queue details: what is queued, which computer it belongs to and how its last
 * attempt ended.
 *
 * Every queued item keeps the computer it was written for, so this list may
 * contain items of computers that are no longer paired. Such a target is shown
 * as "已解除配对" and can never be re-sent by hand — only re-pairing the same
 * computer wakes those items again. Nothing here retries on its own: a PENDING
 * item is picked up when its original computer is connected, as long as the app
 * is open.
 */
@Composable
fun OutboxQueueDialog(
    entries: List<OutboxEntry>,
    hubs: List<SavedHub>,
    onRetry: (String) -> Unit,
    onRemove: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    // UNCONFIRMED needs an explicit warning before a second attempt: the first
    // one may already have reached the Hub, so retrying can duplicate it.
    var unconfirmedRetry by remember { mutableStateOf<OutboxEntry?>(null) }
    var removeTarget by remember { mutableStateOf<OutboxEntry?>(null) }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.outbox_title)) },
        text = {
            Column {
                if (entries.isEmpty()) {
                    Text(
                        stringResource(R.string.outbox_empty),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                } else {
                    Text(
                        stringResource(R.string.outbox_hint),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(6.dp))
                    // Bounded height: the queue may hold many items, and the
                    // dialog must never cover the whole screen.
                    LazyColumn(modifier = Modifier.fillMaxWidth().heightIn(max = 360.dp)) {
                        items(items = entries, key = { it.id }) { entry ->
                            OutboxRow(
                                entry = entry,
                                hubName = hubs.firstOrNull { it.id == entry.hubId }?.name,
                                onRetryRequested = {
                                    if (entry.status == OutboxStatus.UNCONFIRMED) {
                                        unconfirmedRetry = entry
                                    } else {
                                        onRetry(entry.id)
                                    }
                                },
                                onRemove = { removeTarget = entry },
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_close)) }
        },
    )

    unconfirmedRetry?.let { entry ->
        AlertDialog(
            onDismissRequest = { unconfirmedRetry = null },
            title = { Text(stringResource(R.string.outbox_retry_unconfirmed_title)) },
            text = { Text(stringResource(R.string.outbox_retry_unconfirmed_message)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        unconfirmedRetry = null
                        onRetry(entry.id)
                    },
                ) { Text(stringResource(R.string.action_retry)) }
            },
            dismissButton = {
                TextButton(onClick = { unconfirmedRetry = null }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }

    removeTarget?.let { entry ->
        AlertDialog(
            onDismissRequest = { removeTarget = null },
            title = { Text(stringResource(R.string.outbox_remove_title)) },
            text = { Text(stringResource(R.string.outbox_remove_message)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        removeTarget = null
                        onRemove(entry.id)
                    },
                ) { Text(stringResource(R.string.outbox_remove)) }
            },
            dismissButton = {
                TextButton(onClick = { removeTarget = null }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }
}

@Composable
private fun OutboxRow(
    entry: OutboxEntry,
    hubName: String?,
    onRetryRequested: () -> Unit,
    onRemove: () -> Unit,
) {
    // An item whose computer is no longer paired keeps its target and waits; a
    // manual retry would have no stored pairing to send to.
    val paired = hubName != null
    val canRetry = paired &&
        (entry.status == OutboxStatus.FAILED || entry.status == OutboxStatus.UNCONFIRMED)
    // SENDING is in flight on the original client; removing it here would race
    // the upload. File uploads are cancelled from the existing progress list.
    val canRemove = entry.status != OutboxStatus.SENDING
    val statusColor = when (entry.status) {
        OutboxStatus.FAILED -> MaterialTheme.colorScheme.error
        OutboxStatus.UNCONFIRMED -> MaterialTheme.colorScheme.tertiary
        OutboxStatus.SENDING -> MaterialTheme.colorScheme.primary
        OutboxStatus.PENDING -> MaterialTheme.colorScheme.onSurfaceVariant
    }

    Column(modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
        val text = entry.text
        if (text != null) {
            Text(
                text,
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 3,
                overflow = TextOverflow.Ellipsis,
            )
        } else {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    stringResource(R.string.tag_file),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(6.dp))
                Text(
                    entry.displayName.ifBlank { stringResource(R.string.outbox_unnamed_content) },
                    style = MaterialTheme.typography.bodyMedium,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
            }
        }

        Spacer(Modifier.height(4.dp))
        val unnamedHub = stringResource(R.string.hub_unnamed)
        val targetName = hubName?.ifBlank { unnamedHub }
            ?: stringResource(R.string.outbox_target_unknown)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                stringResource(R.string.outbox_target, targetName),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(
                stringResource(outboxStatusLabel(entry.status)),
                style = MaterialTheme.typography.labelMedium,
                color = statusColor,
            )
        }
        if (!paired) {
            Text(
                stringResource(R.string.outbox_target_unpaired),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        val failure = entry.failure
        if (!failure.isNullOrBlank()) {
            Text(
                stringResource(R.string.outbox_failure, failure),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        if (canRetry || canRemove) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (canRetry) {
                    TextButton(
                        onClick = onRetryRequested,
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                    ) {
                        Text(
                            stringResource(R.string.action_retry),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                }
                if (canRemove) {
                    TextButton(
                        onClick = onRemove,
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                    ) {
                        Text(
                            stringResource(R.string.outbox_remove),
                            style = MaterialTheme.typography.labelMedium,
                        )
                    }
                }
            }
        }
    }
}

private fun outboxStatusLabel(status: OutboxStatus): Int = when (status) {
    OutboxStatus.PENDING -> R.string.outbox_status_pending
    OutboxStatus.SENDING -> R.string.outbox_status_sending
    OutboxStatus.UNCONFIRMED -> R.string.outbox_status_unconfirmed
    OutboxStatus.FAILED -> R.string.outbox_status_failed
}
