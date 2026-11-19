package dev.flowdrop.android.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
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
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.flowdrop.android.ConnLabel
import dev.flowdrop.android.R
import dev.flowdrop.android.SavedHub
import dev.flowdrop.android.data.ConnState

/**
 * Target preselected for a system share.
 *
 * The computer currently in use wins; without one, a single saved computer is
 * the only unambiguous answer and is preselected. With several saved computers
 * and no current one the user must choose, so nothing is preselected.
 */
internal fun defaultShareHubId(hubs: List<SavedHub>, activeHubId: String?): String? =
    activeHubId?.takeIf { id -> hubs.any { it.id == id } } ?: hubs.singleOrNull()?.id

/** Display name of a saved computer; a blank name never renders as an empty row. */
@Composable
private fun savedHubLabel(hub: SavedHub): String =
    hub.name.ifBlank { stringResource(R.string.hub_unnamed) }

/** Same connection dot everywhere a computer's link state is shown. */
@Composable
fun HubConnDot(state: ConnState) {
    val color = when (state) {
        ConnState.OPEN -> MaterialTheme.colorScheme.primary
        ConnState.CONNECTING -> MaterialTheme.colorScheme.tertiary
        else -> MaterialTheme.colorScheme.error
    }
    Surface(color = color, shape = CircleShape) {
        Spacer(Modifier.size(7.dp))
    }
}

/**
 * Current computer entry: name, optional connection state and the "switch"
 * affordance. The inbox top bar, the pairing screen and the boot screen all use
 * it, so tapping a computer name always opens the same computer list.
 */
@Composable
fun CurrentHubEntry(
    hubName: String,
    conn: ConnState? = null,
    onOpen: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    val open = if (onOpen != null) Modifier.clickable(onClick = onOpen) else Modifier
    Row(
        modifier = modifier.then(open).padding(horizontal = 4.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                hubName.ifBlank { stringResource(R.string.hub_unnamed) },
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (conn != null) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    HubConnDot(conn)
                    Spacer(Modifier.width(5.dp))
                    Text(
                        ConnLabel.text(conn),
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        if (onOpen != null) {
            Spacer(Modifier.width(8.dp))
            Text(
                stringResource(R.string.hub_switch_entry),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.primary,
            )
        }
    }
}

/**
 * One selectable list of paired computers.
 *
 * Every row shows its address as well, so two computers with the same name stay
 * distinguishable. In picker mode the computer in use is marked "当前"; in
 * share mode the selection is a radio choice owned by the caller.
 */
@Composable
fun HubChoiceList(
    hubs: List<SavedHub>,
    activeHubId: String?,
    selectedHubId: String?,
    onSelect: (SavedHub) -> Unit,
    selectable: Boolean = false,
    enabled: Boolean = true,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(max = 320.dp)
            .verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        hubs.forEach { hub ->
            val selected = hub.id == selectedHubId
            val marker = when {
                selectable && selected -> "✓"
                !selectable && hub.id == activeHubId -> stringResource(R.string.hub_current)
                else -> ""
            }
            val rowModifier = if (selectable) {
                Modifier
                    .fillMaxWidth()
                    .selectable(
                        selected = selected,
                        enabled = enabled,
                        role = Role.RadioButton,
                        onClick = { onSelect(hub) },
                    )
            } else {
                Modifier
                    .fillMaxWidth()
                    .clickable(enabled = enabled) { onSelect(hub) }
            }
            Row(
                modifier = rowModifier.padding(horizontal = 4.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        savedHubLabel(hub),
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        hub.address,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                if (marker.isNotEmpty()) {
                    Spacer(Modifier.width(8.dp))
                    Text(
                        marker,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.primary,
                    )
                }
            }
        }
    }
}

/**
 * Computer list opened from the inbox top bar, the pairing screen or the boot
 * screen. "添加电脑" reuses the existing QR/manual pairing form.
 */
@Composable
fun HubPickerDialog(
    hubs: List<SavedHub>,
    activeHubId: String?,
    onSelect: (String) -> Unit,
    onAddHub: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.hub_list_title)) },
        text = {
            HubChoiceList(
                hubs = hubs,
                activeHubId = activeHubId,
                selectedHubId = null,
                onSelect = { hub -> onSelect(hub.id) },
            )
        },
        confirmButton = {
            TextButton(onClick = onAddHub) { Text(stringResource(R.string.hub_add)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_close)) }
        },
    )
}

/**
 * Pre-send confirmation for a system share.
 *
 * It shows the concrete target and lets the user change it before anything is
 * uploaded. The choice is keyed by [requestId], so a new share starts from the
 * default instead of inheriting the previous answer. Both actions carry the
 * request id back: only that unconfirmed request can be confirmed or dropped.
 */
@Composable
fun ShareTargetDialog(
    requestId: Long,
    hubs: List<SavedHub>,
    activeHubId: String?,
    onConfirm: (Long, String) -> Unit,
    onDismiss: (Long) -> Unit,
) {
    var selected by remember(requestId) { mutableStateOf(defaultShareHubId(hubs, activeHubId)) }
    val target = hubs.firstOrNull { it.id == selected }

    AlertDialog(
        onDismissRequest = { onDismiss(requestId) },
        title = { Text(stringResource(R.string.share_target_title)) },
        text = {
            Column {
                HubChoiceList(
                    hubs = hubs,
                    activeHubId = activeHubId,
                    selectedHubId = selected,
                    selectable = true,
                    onSelect = { hub -> selected = hub.id },
                )
                Spacer(Modifier.size(8.dp))
                if (target != null) {
                    Text(
                        stringResource(R.string.share_target_to, savedHubLabel(target)),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.primary,
                    )
                } else {
                    Text(
                        stringResource(R.string.share_target_choose),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { selected?.let { onConfirm(requestId, it) } },
                enabled = selected != null,
            ) {
                Text(stringResource(R.string.share_target_send))
            }
        },
        dismissButton = {
            TextButton(onClick = { onDismiss(requestId) }) {
                Text(stringResource(R.string.action_cancel))
            }
        },
    )
}

/**
 * Strip above the pairing form.
 *
 * It keeps the paired computers reachable while pairing, also when the selected
 * computer merely went offline. The "返回" action exists only in add-computer
 * mode, where it leaves that mode instead of pairing yet another computer.
 */
@Composable
fun PairingHubHeader(
    hubName: String,
    conn: ConnState? = null,
    addingHub: Boolean,
    onOpenHubs: () -> Unit,
    onCancelAdd: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(color = MaterialTheme.colorScheme.surface, modifier = modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (addingHub) {
                TextButton(
                    onClick = onCancelAdd,
                    contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                ) {
                    Text(
                        stringResource(R.string.hub_back),
                        style = MaterialTheme.typography.labelMedium,
                    )
                }
            }
            CurrentHubEntry(
                hubName = hubName,
                conn = conn,
                onOpen = onOpenHubs,
                modifier = Modifier.weight(1f),
            )
        }
    }
}
