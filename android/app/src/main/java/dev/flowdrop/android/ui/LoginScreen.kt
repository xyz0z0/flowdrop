package dev.flowdrop.android.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import dev.flowdrop.android.R
import dev.flowdrop.android.discovery.DiscoveredHub
import dev.flowdrop.android.discovery.DiscoveryState
import dev.flowdrop.android.discovery.DiscoveryStatus

/**
 * Login form: Hub address, username, password.
 *
 * The address and username are remembered; the password is not. Submitting
 * clears the password field as soon as the attempt finishes.
 */
@Composable
fun LoginScreen(
    hubAddress: String,
    username: String,
    password: String,
    loggingIn: Boolean,
    error: String?,
    onHubChange: (String) -> Unit,
    onUsernameChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onSubmit: () -> Unit,
    discovery: DiscoveryState = DiscoveryState(),
    onSearchDiscovery: () -> Unit = {},
    onRefreshDiscovery: () -> Unit = {},
    onStopDiscovery: () -> Unit = {},
    onSelectHub: (DiscoveredHub) -> Unit = {},
) {
    val focus = LocalFocusManager.current
    Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp, vertical = 32.dp),
            verticalArrangement = Arrangement.Center,
        ) {
            Text("⇄", style = MaterialTheme.typography.titleLarge, color = MaterialTheme.colorScheme.primary)
            Spacer(Modifier.height(8.dp))
            Text(stringResource(R.string.login_title), style = MaterialTheme.typography.titleLarge)
            Text(
                stringResource(R.string.login_subtitle),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(20.dp))

            NearbyHubsSection(
                discovery = discovery,
                enabled = !loggingIn,
                onSearch = onSearchDiscovery,
                onRefresh = onRefreshDiscovery,
                onStop = onStopDiscovery,
                onSelect = onSelectHub,
            )

            Spacer(Modifier.height(20.dp))

            OutlinedTextField(
                value = hubAddress,
                onValueChange = onHubChange,
                label = { Text(stringResource(R.string.label_hub_address)) },
                placeholder = { Text(stringResource(R.string.hub_address_hint)) },
                singleLine = true,
                enabled = !loggingIn,
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Uri,
                    imeAction = ImeAction.Next,
                ),
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(12.dp))

            OutlinedTextField(
                value = username,
                onValueChange = onUsernameChange,
                label = { Text(stringResource(R.string.label_username)) },
                singleLine = true,
                enabled = !loggingIn,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(12.dp))

            OutlinedTextField(
                value = password,
                onValueChange = onPasswordChange,
                label = { Text(stringResource(R.string.label_password)) },
                singleLine = true,
                enabled = !loggingIn,
                visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Password,
                    imeAction = ImeAction.Done,
                ),
                keyboardActions = KeyboardActions(onDone = {
                    focus.clearFocus()
                    onSubmit()
                }),
                modifier = Modifier.fillMaxWidth(),
            )

            if (error != null) {
                Spacer(Modifier.height(12.dp))
                Text(
                    error,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            Spacer(Modifier.height(20.dp))
            Button(
                onClick = {
                    focus.clearFocus()
                    onSubmit()
                },
                enabled = !loggingIn && password.isNotEmpty() && hubAddress.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                if (loggingIn) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(18.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.onPrimary,
                    )
                    Spacer(Modifier.height(0.dp))
                    Text("  " + stringResource(R.string.login_busy))
                } else {
                    Text(stringResource(R.string.action_login))
                }
            }

            Spacer(Modifier.height(20.dp))
            Text(
                stringResource(R.string.login_http_warning),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(R.string.clear_password_hint),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(R.string.address_change_clears_session),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}


/**
 * Nearby Hub list: address hints only. Tapping a row calls [onSelect], which
 * fills the address field; it never logs in by itself.
 */
@Composable
private fun NearbyHubsSection(
    discovery: DiscoveryState,
    enabled: Boolean,
    onSearch: () -> Unit,
    onRefresh: () -> Unit,
    onStop: () -> Unit,
    onSelect: (DiscoveredHub) -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                stringResource(R.string.discovery_title),
                style = MaterialTheme.typography.titleSmall,
            )
            Spacer(Modifier.weight(1f))
            when (discovery.status) {
                DiscoveryStatus.SEARCHING -> TextButton(onClick = onStop) {
                    Text(stringResource(R.string.discovery_stop))
                }
                DiscoveryStatus.IDLE -> TextButton(onClick = onSearch, enabled = enabled) {
                    Text(stringResource(R.string.discovery_search))
                }
                DiscoveryStatus.FINISHED, DiscoveryStatus.ERROR -> TextButton(
                    onClick = onRefresh,
                    enabled = enabled,
                ) {
                    Text(stringResource(R.string.discovery_refresh))
                }
            }
        }

        when (discovery.status) {
            DiscoveryStatus.SEARCHING -> Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(modifier = Modifier.size(14.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(8.dp))
                Text(
                    stringResource(R.string.discovery_searching),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            DiscoveryStatus.IDLE -> Text(
                stringResource(R.string.discovery_idle),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            DiscoveryStatus.ERROR -> Text(
                stringResource(R.string.discovery_error, discovery.message ?: "未知错误"),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            DiscoveryStatus.FINISHED -> Text(
                if (discovery.hubs.isEmpty()) {
                    stringResource(R.string.discovery_empty)
                } else {
                    stringResource(R.string.discovery_found, discovery.hubs.size)
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }

        if (discovery.hubs.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                discovery.hubs.forEach { hub ->
                    NearbyHubRow(hub = hub, enabled = enabled, onSelect = onSelect)
                }
            }
            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(R.string.discovery_hint),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun NearbyHubRow(
    hub: DiscoveredHub,
    enabled: Boolean,
    onSelect: (DiscoveredHub) -> Unit,
) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled) { onSelect(hub) },
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
        ),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 8.dp),
        ) {
            Text(hub.displayName, style = MaterialTheme.typography.bodyLarge)
            Text(
                hub.baseUrl,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
