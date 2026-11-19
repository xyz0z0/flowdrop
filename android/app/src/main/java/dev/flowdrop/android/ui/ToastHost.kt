package dev.flowdrop.android.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Row
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.flowdrop.android.BootProgress
import dev.flowdrop.android.BootStep
import dev.flowdrop.android.R
import dev.flowdrop.android.Toast
import dev.flowdrop.android.data.RouteCandidate
import dev.flowdrop.android.data.routeKindOf
import kotlinx.coroutines.delay

/**
 * First screen after launch: what the app is doing and which Hub address it is
 * working through.
 *
 * A bare full-screen spinner told the user nothing while a Tailscale connection
 * took seconds to establish, so the boot phase now reports its step, the address
 * being tried and how many saved addresses exist. Nothing here is a progress
 * bar over an unknown total; it is a status line, and after a few seconds it
 * explains what to check instead of leaving the user guessing.
 */
@Composable
fun BootScreen(boot: BootProgress, hubName: String) {
    var slow by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        delay(BOOT_SLOW_HINT_MILLIS)
        slow = true
    }
    Box(
        modifier = Modifier.fillMaxSize().padding(horizontal = 32.dp, vertical = 48.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(
                hubName.ifBlank { stringResource(R.string.app_name) },
                style = MaterialTheme.typography.titleLarge,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(R.string.boot_subtitle),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(28.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(
                    modifier = Modifier.size(18.dp),
                    strokeWidth = 2.dp,
                    color = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(10.dp))
                Text(
                    text = stringResource(bootStepLabel(boot.step)),
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            if (boot.address.isNotBlank()) {
                Spacer(Modifier.height(10.dp))
                Text(
                    text = addressLine(boot.address),
                    style = MaterialTheme.typography.labelMedium,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
            if (slow) {
                Spacer(Modifier.height(24.dp))
                Text(
                    text = stringResource(R.string.boot_slow_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
        }
    }
}

/** `http://100.100.100.63:8080 · Tailscale`: address first, network hint after. */
@Composable
private fun addressLine(address: String): String {
    val kind = stringResource(
        when (routeKindOf(address)) {
            RouteCandidate.KIND_LAN -> R.string.boot_kind_lan
            RouteCandidate.KIND_TAILSCALE -> R.string.boot_kind_tailscale
            else -> R.string.boot_kind_other
        },
    )
    return "$address · $kind"
}

@StringRes
private fun bootStepLabel(step: BootStep): Int = when (step) {
    BootStep.READING_SESSION -> R.string.boot_reading_session
    BootStep.SELECTING_ADDRESS -> R.string.boot_selecting_address
    BootStep.RESTORING_SESSION -> R.string.boot_restoring_session
}

/** Long enough to feel instant when the Hub is reachable, short enough to help. */
private const val BOOT_SLOW_HINT_MILLIS = 6_000L

/** Ephemeral bottom toasts; each dismisses itself after a few seconds. */
@Composable
fun ToastHost(toasts: List<Toast>, onDismiss: (Long) -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.BottomCenter) {
        Column(
            verticalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.fillMaxWidth().wrapContentHeight(),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            toasts.forEach { toast ->
                LaunchedEffect(toast.id) {
                    delay(3200)
                    onDismiss(toast.id)
                }
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    color = if (toast.error) {
                        MaterialTheme.colorScheme.errorContainer
                    } else {
                        MaterialTheme.colorScheme.inverseSurface
                    },
                    tonalElevation = 3.dp,
                ) {
                    Text(
                        toast.text,
                        style = MaterialTheme.typography.bodyMedium,
                        color = if (toast.error) {
                            MaterialTheme.colorScheme.onErrorContainer
                        } else {
                            MaterialTheme.colorScheme.inverseOnSurface
                        },
                        modifier = Modifier.padding(horizontal = 14.dp, vertical = 10.dp),
                    )
                }
            }
        }
    }
}
