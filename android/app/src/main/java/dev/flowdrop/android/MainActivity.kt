package dev.flowdrop.android

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dev.flowdrop.android.ui.FlowDropTheme
import dev.flowdrop.android.ui.HubPickerDialog
import dev.flowdrop.android.ui.InboxScreen
import dev.flowdrop.android.ui.LoginScreen
import dev.flowdrop.android.ui.OutboxQueueDialog
import dev.flowdrop.android.ui.OutboxQueueEntry
import dev.flowdrop.android.ui.PairingHubHeader
import dev.flowdrop.android.ui.QrScannerScreen
import dev.flowdrop.android.ui.ShareTargetDialog
import dev.flowdrop.android.ui.ToastHost
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {

    private val viewModel: AppViewModel by viewModels {
        AppViewModelFactory(application as FlowDropApp)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        setContent {
            FlowDropTheme {
                FlowDropRoot(viewModel)
            }
        }

        // The initial share is a launch event. It is handled only on a fresh
        // start: a configuration change keeps the unconfirmed request in the
        // ViewModel, and a stale Intent must never be replayed after confirm,
        // cancel or process death (all of which restore a non-null state).
        if (savedInstanceState == null) handleShareIntent(intent)

        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                // Foreground: re-pull the list and reopen the socket, since the
                // platform gives no background guarantee for either.
                viewModel.onForeground()
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        // Every new intent is a new share, even when its content matches one
        // delivered earlier: the OS reuses the Activity, not the payload.
        handleShareIntent(intent)
    }

    override fun onStop() {
        super.onStop()
        viewModel.onBackground()
    }

    /**
     * Extracts ACTION_SEND / ACTION_SEND_MULTIPLE payloads.
     *
     * Only the URIs the system granted to this intent are used, and the grant
     * is kept for the duration of the launch. The upload reads them lazily, so
     * the app never needs broad storage permission and never buffers a whole
     * file in memory.
     *
     * The delivered intent is NOT mutated: clearing EXTRA_STREAM/clipData can
     * drop the URI grant the platform attached to it.
     */
    private fun handleShareIntent(intent: Intent?) {
        val payload = parseShareIntent(intent) ?: return

        // Keep the grant explicitly for this activity while staging happens:
        // for persistable grants this survives; for one-shot grants the
        // immediate staging copy below is what makes the upload reliable.
        for (uri in payload.uris) {
            runCatching {
                contentResolver.takePersistableUriPermission(
                    uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION,
                )
            }
        }
        viewModel.submitShare(payload)
    }
}

/** Pure intent parsing, kept free of Activity state so it can be unit tested. */
fun parseShareIntent(intent: Intent?): SharePayload? {
    if (intent == null) return null
    val action = intent.action ?: return null
    if (action != Intent.ACTION_SEND && action != Intent.ACTION_SEND_MULTIPLE) return null

    val texts = mutableListOf<String>()
    intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()?.let { if (it.isNotBlank()) texts += it }
    intent.getStringExtra(Intent.EXTRA_SUBJECT)
        ?.takeIf { subj -> subj.isNotBlank() && texts.none { it.contains(subj) } }
        ?.let { subject ->
            // A subject alone (e.g. a shared link title) is only useful when
            // there is no body text; never send it as a second item.
            if (texts.isEmpty()) texts += subject
        }

    val uris = mutableListOf<Uri>()
    when (action) {
        Intent.ACTION_SEND -> {
            @Suppress("DEPRECATION")
            val single = intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
            if (single != null) uris += single
        }
        Intent.ACTION_SEND_MULTIPLE -> {
            @Suppress("DEPRECATION")
            val multiple = intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM)
            multiple?.let { uris += it }
        }
    }
    // ClipData covers apps that attach streams only there.
    intent.clipData?.let { clip ->
        for (index in 0 until clip.itemCount) {
            clip.getItemAt(index).uri?.let { if (it !in uris) uris += it }
        }
    }
    // A few apps (and `am start -d`) put the single shared item in the data URI.
    // Accept it only for content/file schemes so a stray http URL is not
    // mistaken for an attachment.
    if (uris.isEmpty()) {
        intent.data?.let { data ->
            if (data.scheme == "content" || data.scheme == "file") uris += data
        }
    }
    if (texts.isEmpty() && uris.isEmpty()) return null
    return SharePayload(texts = texts, uris = uris)
}

@Composable
private fun FlowDropRoot(viewModel: AppViewModel) {
    val state by viewModel.state.collectAsState()
    val composerText by viewModel.composerInput.collectAsState()
    val context = LocalContext.current

    // Pairing-QR scanner: opened from the login form, dismissed on its own or
    // when the phase changes. The CAMERA permission is requested only when the
    // user taps the scan entry; a refusal shows one hint and leaves the manual
    // address + code form fully usable.
    var scannerOpen by rememberSaveable { mutableStateOf(false) }
    var cameraPermissionDenied by rememberSaveable { mutableStateOf(false) }
    // Computer list opened from the pairing or boot screen; the inbox hosts its
    // own copy because its top bar owns that entry.
    var hubPickerOpen by rememberSaveable { mutableStateOf(false) }
    // Offline queue, reachable from the pairing screen too: queued items belong
    // to computers that may not be paired (or reachable) at all right now.
    var outboxOpen by rememberSaveable { mutableStateOf(false) }
    val cameraPermissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        if (granted) {
            cameraPermissionDenied = false
            scannerOpen = true
        } else {
            cameraPermissionDenied = true
        }
    }

    LaunchedEffect(state.phase) {
        if (state.phase != Phase.LOGIN) scannerOpen = false
        if (state.phase == Phase.INBOX) hubPickerOpen = false
    }

    // Startup/exit of the login screen is the only place discovery runs.
    LaunchedEffect(state.phase) {
        when (state.phase) {
            Phase.LOGIN -> viewModel.onLoginScreenShown()
            Phase.INBOX -> viewModel.onLoginScreenLeft()
            Phase.BOOTING -> Unit
        }
    }

    androidx.compose.material3.Surface(
        color = androidx.compose.material3.MaterialTheme.colorScheme.background,
    ) {
        androidx.compose.foundation.layout.Box {
            when (state.phase) {
                Phase.BOOTING -> androidx.compose.foundation.layout.Box(modifier = Modifier.fillMaxSize()) {
                    dev.flowdrop.android.ui.BootScreen(boot = state.boot, hubName = state.hubName)
                    // A slow/offline computer must not trap the user in boot: the
                    // saved computers stay reachable, but only when there is a
                    // choice to make.
                    if (state.savedHubs.size > 1) {
                        TextButton(
                            onClick = { hubPickerOpen = true },
                            modifier = Modifier.align(Alignment.TopEnd).padding(top = 32.dp, end = 12.dp),
                        ) {
                            Text(stringResource(R.string.hub_switch_entry))
                        }
                    }
                }
                Phase.LOGIN -> Column(modifier = Modifier.fillMaxSize()) {
                    if (state.savedHubs.isNotEmpty()) {
                        // Also covers the offline case: pairing is shown for the
                        // selected computer, other paired computers stay one tap
                        // away. "返回" exists only while adding a computer.
                        PairingHubHeader(
                            hubName = state.hubName,
                            addingHub = state.addingHub,
                            onOpenHubs = { hubPickerOpen = true },
                            onCancelAdd = viewModel::cancelAddHub,
                        )
                    }
                    // Same entry as the inbox composer's: without a pairing the
                    // queue is the only way to see what is still waiting.
                    OutboxQueueEntry(
                        count = state.outbox.size,
                        onClick = { outboxOpen = true },
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp),
                    )
                    LoginScreen(
                        hubAddress = state.hubAddress,
                        pairingCode = state.pairingCode,
                        hasStoredPairing = state.hasStoredPairing,
                        hubName = state.hubName,
                        loggingIn = state.loggingIn,
                        error = state.loginError,
                        onHubChange = viewModel::updateHubAddress,
                        onPairingCodeChange = viewModel::updatePairingCode,
                        onSubmit = viewModel::pair,
                        onRetryConnection = viewModel::retryConnection,
                        discovery = state.discovery,
                        onSearchDiscovery = viewModel::searchNearbyHubs,
                        onRefreshDiscovery = viewModel::refreshNearbyHubs,
                        onStopDiscovery = viewModel::stopHubDiscovery,
                        onSelectHub = viewModel::selectDiscoveredHub,
                        onScanQr = {
                            val granted = ContextCompat.checkSelfPermission(
                                context,
                                Manifest.permission.CAMERA,
                            ) == PackageManager.PERMISSION_GRANTED
                            if (granted) {
                                cameraPermissionDenied = false
                                scannerOpen = true
                            } else {
                                cameraPermissionLauncher.launch(Manifest.permission.CAMERA)
                            }
                        },
                        cameraPermissionDenied = cameraPermissionDenied,
                    )
                }
                Phase.INBOX -> InboxScreen(
                    state = state,
                    composerText = composerText,
                    imageStore = viewModel.imageStore,
                    onComposerChange = viewModel::updateComposer,
                    onSend = viewModel::sendText,
                    onPickImages = viewModel::uploadUris,
                    onPickFiles = viewModel::uploadUris,
                    onCancelUpload = viewModel::cancelUpload,
                    onDismissUpload = viewModel::dismissUpload,
                    onRefresh = viewModel::resync,
                    onLoadMore = viewModel::loadMore,
                    onDelete = { viewModel.deleteItem(it.id) },
                    onSave = viewModel::saveItem,
                    onInstallApk = viewModel::installApk,
                    onCancelDownload = viewModel::cancelDownload,
                    onDeviceNameChange = viewModel::updateDeviceName,
                    onLogout = viewModel::logout,
                    onNotify = { message, error -> viewModel.toast(message, error) },
                    onCategoryChange = viewModel::selectCategory,
                    onRefreshPresence = viewModel::refreshPresence,
                    onSelectHub = viewModel::selectHub,
                    onBeginAddHub = viewModel::beginAddHub,
                    onOpenOutbox = { outboxOpen = true },
                )
            }
            if (state.phase == Phase.LOGIN && scannerOpen) {
                QrScannerScreen(
                    onDecoded = { raw ->
                        // The scanner hands over the raw text once; it is parsed
                        // and dropped here, never logged or stored.
                        scannerOpen = false
                        viewModel.applyScannedPairing(raw)
                    },
                    onCancel = { scannerOpen = false },
                )
            }
            ToastHost(
                toasts = state.toasts,
                onDismiss = viewModel::dismissToast,
            )

            if (hubPickerOpen && state.savedHubs.isNotEmpty()) {
                HubPickerDialog(
                    hubs = state.savedHubs,
                    activeHubId = state.activeHubId,
                    onSelect = { hubId ->
                        hubPickerOpen = false
                        viewModel.selectHub(hubId)
                    },
                    onAddHub = {
                        hubPickerOpen = false
                        viewModel.beginAddHub()
                    },
                    onDismiss = { hubPickerOpen = false },
                )
            }

            // Queue details are hosted once, above the phase switch, so the
            // pairing screen and the inbox share one dialog, one state and one
            // pair of retry/removal callbacks.
            if (outboxOpen) {
                OutboxQueueDialog(
                    entries = state.outbox,
                    hubs = state.savedHubs,
                    onRetry = viewModel::retryOutbox,
                    onRemove = viewModel::removeOutbox,
                    onDismiss = { outboxOpen = false },
                )
            }

            // The system share stays unconfirmed across phases: the dialog is
            // hosted above the phase switch so it survives pairing/boot, and it
            // is shown only once a saved computer can actually be chosen.
            val shareRequestId = state.shareRequestId
            if (shareRequestId != null && state.savedHubs.isNotEmpty()) {
                ShareTargetDialog(
                    requestId = shareRequestId,
                    hubs = state.savedHubs,
                    activeHubId = state.activeHubId,
                    onConfirm = viewModel::confirmShare,
                    onDismiss = viewModel::dismissShare,
                )
            }

            // Surface lifecycle-driven session changes once, for accessibility.
            LaunchedEffect(state.phase, state.loginError) {
                val message = state.loginError
                if (state.phase == Phase.LOGIN && message != null) {
                    dev.flowdrop.android.device.SystemActions.toast(context, message)
                }
            }
        }
    }
}
