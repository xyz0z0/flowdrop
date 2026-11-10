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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dev.flowdrop.android.ui.FlowDropTheme
import dev.flowdrop.android.ui.InboxScreen
import dev.flowdrop.android.ui.LoginScreen
import dev.flowdrop.android.ui.QrScannerScreen
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

        // Share intents are handled after the UI exists so the ViewModel can
        // show a message if the user is not logged in yet.
        handleShareIntent(intent)

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
     * drop the URI grant the platform attached to it. Re-processing is
     * prevented by tracking the intent identity instead.
     */
    private fun handleShareIntent(intent: Intent?) {
        val payload = parseShareIntent(intent) ?: return
        val fingerprint = payload.texts.hashCode() * 31 + payload.uris.hashCode()
        if (fingerprint == lastShareFingerprint) return
        lastShareFingerprint = fingerprint

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

    private var lastShareFingerprint: Int = 0
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
                Phase.BOOTING -> dev.flowdrop.android.ui.BootScreen()
                Phase.LOGIN -> LoginScreen(
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
