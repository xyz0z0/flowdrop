package dev.flowdrop.android

import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.flowdrop.android.connection.ConnectResult
import dev.flowdrop.android.connection.ConnectTrigger
import dev.flowdrop.android.connection.PairedConnector
import dev.flowdrop.android.connection.SavedPairing
import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.ApiException
import dev.flowdrop.android.data.Blob
import dev.flowdrop.android.data.ConnState
import dev.flowdrop.android.data.DEFAULT_MAX_UPLOAD_BYTES
import dev.flowdrop.android.data.EVENT_PRESENCE_CHANGED
import dev.flowdrop.android.data.EventStream
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.HubProbe
import dev.flowdrop.android.data.HubRouteSelector
import dev.flowdrop.android.data.HubVault
import dev.flowdrop.android.data.Item
import dev.flowdrop.android.data.ItemCategory
import dev.flowdrop.android.data.NetworkWatcher
import dev.flowdrop.android.data.OutboxAttachmentMissingException
import dev.flowdrop.android.data.OutboxAttachmentTooLargeException
import dev.flowdrop.android.data.OutboxEntry
import dev.flowdrop.android.data.OutboxException
import dev.flowdrop.android.data.OutboxStatus
import dev.flowdrop.android.data.OutboxStore
import dev.flowdrop.android.data.OutboxTransportException
import dev.flowdrop.android.data.PresenceDevice
import dev.flowdrop.android.data.RouteCandidate
import dev.flowdrop.android.data.UploadSource
import dev.flowdrop.android.data.parsePairingPayload
import dev.flowdrop.android.data.pairingPrefill
import dev.flowdrop.android.discovery.DiscoveredHub
import dev.flowdrop.android.discovery.DiscoveryController
import dev.flowdrop.android.discovery.DiscoveryState
import dev.flowdrop.android.device.SystemActions
import dev.flowdrop.android.io.ApkInstaller
import dev.flowdrop.android.io.ApkPackageCache
import dev.flowdrop.android.io.DownloadTarget
import dev.flowdrop.android.io.TransferRateMeter
import dev.flowdrop.android.io.UriSources
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

enum class Phase { BOOTING, LOGIN, INBOX }

/**
 * What the boot phase is doing right now.
 *
 * The boot screen is the first thing a user sees, so it says which step is
 * running and which address is being tried instead of presenting an unexplained
 * spinner. Steps only describe progress; they never gate behaviour.
 */
enum class BootStep { READING_SESSION, SELECTING_ADDRESS, RESTORING_SESSION }

/**
 * Boot progress shown by the boot screen.
 *
 * [address] is the address currently being probed or bound, and [candidates] is
 * how many saved addresses this round may try (so a slow round can explain
 * itself instead of looking stuck).
 */
data class BootProgress(
    val step: BootStep = BootStep.READING_SESSION,
    val address: String = "",
    val candidates: Int = 0,
)

enum class TaskStatus { RUNNING, DONE, FAILED }

data class UploadTask(
    val key: String,
    val name: String,
    val size: Long,
    val loaded: Long = 0,
    val status: TaskStatus = TaskStatus.RUNNING,
    val error: String? = null,
    /** Computer this transfer was started for; its rows stay with that Hub. */
    val hubId: String = "",
) {
    val fraction: Float get() = if (size > 0) (loaded.toFloat() / size).coerceIn(0f, 1f) else 0f
}

/**
 * Which action a download belongs to.
 *
 * Saving to Downloads and handing an APK to the installer keep independent
 * rows and labels even though they share one cached package: a finished save
 * must not make the install button look done, and an installer that failed to
 * open must not make the save look failed.
 */
enum class DownloadKind { SAVE, INSTALL }

data class DownloadTask(
    val key: String,
    val itemId: String,
    val name: String,
    val loaded: Long = 0,
    val total: Long? = null,
    /**
     * Measured throughput in bytes/second, null until the first window closes.
     * The UI shows a placeholder meanwhile instead of a fake number.
     */
    val bytesPerSecond: Long? = null,
    val status: TaskStatus = TaskStatus.RUNNING,
    val error: String? = null,
    /** Computer this transfer was started for; its rows stay with that Hub. */
    val hubId: String = "",
    val kind: DownloadKind = DownloadKind.SAVE,
) {
    val fraction: Float
        get() = total?.takeIf { it > 0 }?.let { (loaded.toFloat() / it).coerceIn(0f, 1f) } ?: 0f
}

data class Toast(val id: Long, val text: String, val error: Boolean = false)

/**
 * One paired computer shown at the top of the inbox.
 *
 * [id] is the Hub ID the whole isolation model is keyed by, [name] is the name
 * that Hub reports (it may repeat across computers) and [address] is the
 * address this device last reached it at, which lets the UI tell two
 * same-named computers apart.
 */
data class SavedHub(val id: String, val name: String, val address: String)

data class UiState(
    val phase: Phase = Phase.BOOTING,
    val hubAddress: String = "",
    /** Four-digit pairing code while typing; cleared on submit, never stored. */
    val pairingCode: String = "",
    /** A Hub/device pairing exists in the Keystore store (reconnect, no code). */
    val hasStoredPairing: Boolean = false,
    val hubName: String = "",
    val loggingIn: Boolean = false,
    val loginError: String? = null,
    val deviceName: String = "Android",
    val items: List<Item> = emptyList(),
    val nextCursor: String = "",
    /**
     * Visible file-type filter; the Hub decides what each value contains. Kept
     * in memory only: a new process starts on [ItemCategory.ALL] again.
     */
    val category: String = ItemCategory.ALL,
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    val error: String? = null,
    val conn: ConnState = ConnState.CLOSED,
    /**
     * Devices whose authenticated WebSocket is connected to this Hub right now
     * (`GET /api/v1/presence`). Kept next to — but independent of — [items]: a
     * presence failure never marks the inbox as broken or blocks sending.
     */
    val onlineDevices: List<PresenceDevice> = emptyList(),
    val presenceLoading: Boolean = false,
    /** Last presence failure, shown only inside the online list. */
    val presenceError: String? = null,
    /** True only after a presence snapshot was accepted for the live Hub. */
    val presenceFresh: Boolean = false,
    val uploads: List<UploadTask> = emptyList(),
    val downloads: List<DownloadTask> = emptyList(),
    /**
     * Offline queue of every paired (and already unpaired) computer. Entries
     * carry their own `hubId`, so the UI resolves the target name from
     * [savedHubs] and shows an "unpaired" hint when it is gone.
     */
    val outbox: List<OutboxEntry> = emptyList(),
    val toasts: List<Toast> = emptyList(),
    val maxUploadBytes: Long = DEFAULT_MAX_UPLOAD_BYTES,
    val sendingText: Boolean = false,
    val discovery: DiscoveryState = DiscoveryState(),
    /** Only meaningful while [phase] is [Phase.BOOTING]. */
    val boot: BootProgress = BootProgress(),
    /** Every paired computer, in the order their Hub IDs were first stored. */
    val savedHubs: List<SavedHub> = emptyList(),
    /** Hub ID of the computer whose session owns everything shown right now. */
    val activeHubId: String? = null,
    /** True while the user is pairing an additional computer. */
    val addingHub: Boolean = false,
    /**
     * Non-null while a system share is waiting for its target confirmation.
     * The request is identified (not just "a payload") so a stale dialog can
     * never send what a newer share replaced.
     */
    val shareRequestId: Long? = null,
) {
    /**
     * Whether the online list may be presented as live.
     *
     * Without an accepted snapshot, or with the socket down (the Hub notices a
     * dead connection only after its ping timeout), the UI shows "state pending
     * update" instead of a fake live count.
     */
    val presenceStale: Boolean get() = !presenceFresh || conn != ConnState.OPEN
}

/**
 * Owns all session-scoped state and every network call.
 *
 * Rules encoded here:
 * - the pairing code lives only in [UiState] while typing, is cleared on submit
 *   and is never persisted; the device token never leaves the Keystore store
 *   except as a one-shot `Authorization: Bearer` during session restore;
 * - a paired device reconnects by probing its saved LAN/Tailscale candidates
 *   (see [PairedConnector] and [HubRouteSelector]) instead of asking again;
 * - an unreachable paired Hub keeps the pairing (offline must not force a
 *   re-pair), while an explicit revocation clears it and returns to pairing;
 * - switching address cancels in-flight transfers instead of replaying them;
 * - on background the socket and discovery stop; on foreground the list is
 *   re-fetched and the socket restarted, because Android gives no background
 *   guarantee;
 * - several computers may be paired at once. Each one has its own encrypted
 *   record ([HubVault]) and its own [ApiClient], so a stale handle, a late
 *   response or a 401 belonging to one computer can never read or write another
 *   computer's credentials, inbox, previews, paging cursor or transfers. Only
 *   the active computer runs an event stream, and switching never replays a
 *   write: a running upload keeps its original client, address and identity.
 */
class AppViewModel(
    private val app: FlowDropApp,
    private val container: AppContainer,
) : ViewModel() {

    private val prefs = container.preferences
    private val vault: HubVault = container.vault

    private val _state = MutableStateFlow(
        UiState(
            hubAddress = prefs.hubAddress,
            hubName = prefs.hubName,
            deviceName = prefs.deviceName,
        ),
    )
    val state: StateFlow<UiState> = _state.asStateFlow()

    /**
     * Foreground-only nearby-Hub search. It never touches the session or a
     * credential: a discovered row only fills the address field.
     */
    private val discovery = DiscoveryController(
        driver = container.newDiscoveryDriver(),
        scope = viewModelScope,
        onState = { next -> _state.update { it.copy(discovery = next) } },
    )

    private var client: ApiClient? = null
    private var eventStream: EventStream? = null

    // Generation tokens for the first-page request and for "load more". A
    // response whose token is stale belongs to a previous category (or to an
    // older snapshot) and must not touch items, cursor, error or spinner state.
    private var requestSeq = 0
    private var loadMoreSeq = 0

    // Presence has its own generation. It is deliberately not [requestSeq]: a
    // category switch must not cancel an online-list refresh and a slow answer
    // from a previous Hub (or from before a logout) must never be written into
    // the current identity. [client] identity is checked as well, because each
    // bound Hub gets a fresh ApiClient instance.
    private var presenceSeq = 0

    // Items announced while a first-page snapshot is in flight. The snapshot
    // may predate them, so they are re-fetched once it lands.
    private val snapshotting = java.util.concurrent.atomic.AtomicBoolean(false)
    private val pendingCreates = ConcurrentHashMap.newKeySet<String>()

    // Registered/removed by each transfer's own coroutine and cleared from Main,
    // so they must be thread-safe and are never iterated live (see cancelTransfers).
    // Each job remembers the computer it was started for, so cancelling one
    // computer's transfers never touches another's.
    private val uploadJobs = ConcurrentHashMap<String, HubJob>()
    private val downloadJobs = ConcurrentHashMap<String, HubJob>()

    /**
     * Complete APK packages shared by the save and install actions, keyed by
     * Hub, item and the Hub's SHA-256. A package downloaded for one action is
     * reused by the other instead of a second transfer, and persists across an
     * installer prompt the user cancelled or that failed to open.
     */
    private val apkCache = ApkPackageCache(File(app.cacheDir, "downloads/apk"))

    /**
     * Transfers of every paired computer. Only the active computer's entries are
     * published to [UiState], so switching computers neither hides a running
     * upload nor shows one computer's transfers in another computer's inbox.
     */
    private val uploadTasks = TransferBook<UploadTask>(idOf = { it.key }, hubOf = { it.hubId })
    private val downloadTasks = TransferBook<DownloadTask>(idOf = { it.key }, hubOf = { it.hubId })

    /**
     * Unconfirmed system shares, by request id. A share is only ever sent to the
     * computer its confirmation named; the id keeps a stale dialog from sending
     * a payload that a newer share already replaced.
     */
    private val shareRequests = ConcurrentHashMap<Long, ShareRequest>()
    private val shareStagingJobs = ConcurrentHashMap<Long, Job>()
    private val shareReady = ConcurrentHashMap<Long, CompletableDeferred<Unit>>()
    private var shareSeq = 0L

    /**
     * Durable queue of everything the user asked to send. Items keep the Hub and
     * device name they were written for, so switching computers never redirects
     * or replays them into the wrong inbox.
     */
    private val outbox: OutboxStore = container.outbox

    /**
     * The one running drain per computer, Main-thread only. A slow upload for Hub
     * A therefore never blocks the drain of a Hub B the user just connected to.
     */
    private val outboxDrainJobs = HashMap<String, Job>()

    /** Computers whose drain was requested while theirs was already running. */
    private val outboxDrainRequested = HashSet<String>()

    /** Bounded reconnect loop while the current computer has PENDING items. */
    private var outboxReconnectJob: Job? = null

    /** Main-thread token so an older queue read cannot overwrite a newer one. */
    private var outboxPublishSeq = 0L

    /**
     * Uploads started by a queue drain, kept apart from [uploadJobs] on purpose:
     * a session teardown (401, switching away) must not cancel them into an
     * "interrupted, unknown" state, and an explicit 取消 still can.
     */
    private val outboxUploadJobs = ConcurrentHashMap<String, HubJob>()

    private val images = dev.flowdrop.android.ui.AuthenticatedImageStore { client }

    val imageStore: dev.flowdrop.android.ui.AuthenticatedImageStore get() = images

    /** False while the Activity is backgrounded: no probing, no reselection. */
    private var foreground = true

    /**
     * Foreground-only default-network watcher: the moment Wi-Fi returns or the
     * Tailscale VPN comes up, a reselection runs without waiting for a socket
     * failure or for the connector's failure throttle.
     */
    private val networkWatcher: NetworkWatcher = container.newNetworkWatcher()

    private val routeSelector = HubRouteSelector(probe = { candidate -> HubProbe.hubId(candidate) })

    /**
     * Hub ID that owns [client] and [eventStream]. Written on Main only; read
     * from every async continuation so a late answer for the previous computer
     * is discarded instead of being written into the new inbox.
     */
    @Volatile
    private var activeHubId: String? = null

    /**
     * The computer the user last asked to open, plus the monotonic generation of
     * that request.
     *
     * Every asynchronous completion (switch, automatic reselect, pairing,
     * revocation, logout) captures this pair when it starts and is applied only
     * while it still matches. A result that arrives after the user moved on can
     * therefore not open, clear, re-target or abort anything. A null hub means
     * "no computer may publish", which is the case while a new computer is being
     * paired.
     */
    @Volatile
    private var intendedHubId: String? = null

    @Volatile
    private var intentGen: Long = 0L

    /** Starts a new request and returns its generation. Main thread only. */
    private fun beginIntent(hubId: String?): Long {
        intentGen += 1
        intendedHubId = hubId
        return intentGen
    }

    /** Whether request [gen] for [hubId] is still what the user wants. */
    private fun isCurrentIntent(gen: Long, hubId: String): Boolean =
        intentGen == gen && intendedHubId == hubId

    /** Whether request [gen] is the newest one. */
    private fun isCurrentIntent(gen: Long): Boolean = intentGen == gen

    /** A switch (or a pairing form) is in flight: automatic rounds stand back. */
    private fun selectionPending(): Boolean = intendedHubId != activeHubId

    /**
     * One single-flight connector per computer. A round for Hub B can therefore
     * never consume, cancel or publish the round that is reconnecting Hub A.
     */
    private val connectors = HashMap<String, PairedConnector>()

    private fun connectorFor(hubId: String): PairedConnector = connectors.getOrPut(hubId) {
        PairedConnector(
            scope = viewModelScope,
            pairing = { loadSavedPairing(hubId) },
            pick = { saved -> pickPairedCandidate(hubId, saved) },
            bind = { candidate -> bindPaired(hubId, candidate) },
            isForeground = { foreground },
        )
    }

    /** The stored pairing of [hubId], or null when it is not paired anymore. */
    private fun loadSavedPairing(hubId: String): SavedPairing? {
        val stored = vault.load(hubId) ?: return null
        if (stored.hubId.isBlank() || stored.hubId != hubId) return null
        return SavedPairing(
            hubId = stored.hubId,
            candidates = stored.candidates.map { it.toRouteCandidate() },
        )
    }

    init {
        restoreSession()
    }

    // ---------- session lifecycle ----------

    /**
     * Startup. A paired device reconnects silently through its saved LAN and
     * Tailscale addresses; an unpaired one gets discovery plus the pairing form.
     *
     * The session record lives behind the Android Keystore, and decrypting it is
     * an IPC round trip that would otherwise run on the main thread *before* the
     * first frame: the app showed nothing until the boot spinner could finally be
     * composed. It is read off the main thread instead, while [Phase.BOOTING]
     * stays on screen from the very first composition.
     */
    private fun restoreSession() {
        viewModelScope.launch {
            // The queue is prepared first, before any computer is published and
            // before any drain can start: a SENDING entry left by a killed
            // process becomes UNCONFIRMED. [AppContainer.prepareOutbox] is a
            // synchronous process-wide once step, so an Activity rebuild inside
            // this process never re-recovers a send that is still in flight; a
            // failure is surfaced instead of being swallowed.
            var prepareError: Throwable? = null
            val loaded = withContext(Dispatchers.IO) {
                prepareError = runCatching { container.prepareOutbox() }.exceptionOrNull()
                Triple(vault.all(), vault.activeHubId(), prefs.hubAddress)
            }
            prepareError?.let { toast("待发送队列无法恢复：${friendly(it)}", true) }
            val (records, storedActive, lastAddress) = loaded
            val hubs = records.mapNotNull { it.toSavedHub() }
            // Last used computer, unless it was removed meanwhile.
            val active = storedActive?.takeIf { id -> hubs.any { it.id == id } }
                ?: hubs.firstOrNull()?.id
            val record = active?.let { id -> records.firstOrNull { it.hubId == id } }
            val address = record?.baseUrl?.takeIf { it.isNotBlank() } ?: lastAddress
            _state.update {
                it.copy(
                    savedHubs = hubs,
                    activeHubId = active,
                    hubAddress = address,
                    hubName = record?.hubName?.takeIf { name -> name.isNotBlank() } ?: prefs.hubName,
                    hasStoredPairing = record != null,
                    phase = if (record != null) Phase.BOOTING else Phase.LOGIN,
                    boot = BootProgress(
                        step = BootStep.SELECTING_ADDRESS,
                        address = address,
                        candidates = record?.candidates?.size ?: 0,
                    ),
                )
            }
            publishOutbox()
            if (record == null || active == null) {
                beginIntent(null)
                return@launch
            }
            val gen = beginIntent(active)
            handleConnectResult(gen, active, connectorFor(active).connect(ConnectTrigger.STARTUP))
        }
    }

    /** Re-reads the saved computers after the vault changed. */
    private fun refreshSavedHubs() {
        val hubs = vault.all().mapNotNull { it.toSavedHub() }
        _state.update { it.copy(savedHubs = hubs) }
    }

    fun updateHubAddress(value: String) {
        _state.update { it.copy(hubAddress = value, loginError = null) }
    }

    /** Four-digit pairing code: digits only, leading zeros preserved. */
    fun updatePairingCode(value: String) {
        val digits = value.filter { it in '0'..'9' }.take(PAIRING_CODE_LENGTH)
        _state.update { it.copy(pairingCode = digits, loginError = null) }
    }

    /**
     * Applies a scanned pairing QR payload: it only prefills the form.
     *
     * A scanned code never pairs by itself, never replaces an address the user
     * already typed and is never logged or persisted: the raw payload is parsed
     * and immediately discarded. An unusable payload leaves the current input
     * untouched (the code printed on the Hub stays valid).
     */
    fun applyScannedPairing(raw: String): Boolean {
        if (_state.value.phase != Phase.LOGIN) return false
        val payload = parsePairingPayload(raw)
        if (payload == null) {
            toast(app.getString(R.string.pairing_qr_invalid), error = true)
            return false
        }
        val prefill = pairingPrefill(payload, _state.value.hubAddress)
        _state.update { current ->
            current.copy(
                pairingCode = prefill.code,
                hubAddress = prefill.hubAddress ?: current.hubAddress,
                loginError = null,
            )
        }
        toast(app.getString(R.string.pairing_qr_filled))
        return true
    }

    /**
     * Pairs this device with the Hub at [UiState.hubAddress] using the code
     * shown on the Hub's settings screen.
     *
     * The attempt runs against an isolated, in-memory [SessionStore]: a wrong
     * code, a cancelled form or a Hub that turns out to be another computer can
     * only end this attempt and never touch the computers that are already
     * paired. The code is cleared the moment it is submitted and is never
     * stored; the device token returned once by the Hub is persisted by
     * [ApiClient.pair] into that attempt's record, which is moved into the
     * vault under its Hub ID only after the Hub confirms the pairing.
     */
    fun pair() {
        val snapshot = _state.value
        if (snapshot.loggingIn) return
        val endpoint = HubEndpoint.parse(snapshot.hubAddress)
        if (endpoint == null) {
            _state.update { it.copy(loginError = app.getString(R.string.error_bad_address)) }
            return
        }
        val code = snapshot.pairingCode
        if (code.length != PAIRING_CODE_LENGTH) {
            _state.update { it.copy(loginError = "请输入 4 位配对码") }
            return
        }
        _state.update { it.copy(pairingCode = "", loggingIn = true, loginError = null) }
        // No computer may publish while a new one is being paired: an older
        // reconnect round must not reopen the previous inbox, and the token lets
        // a late answer of this attempt be dropped after cancelAddHub()/switch.
        val gen = beginIntent(null)
        viewModelScope.launch {
            // The attempt has no Hub ID yet, so a 401 on it can never be the
            // active computer's 401 and is handled by the pair() error path.
            val attempt = container.newPairingAttempt(endpoint) { onUnauthorized(NO_HUB, it) }
            try {
                val info = withContext(Dispatchers.IO) { attempt.client.pair(code, prefs.deviceName) }
                // Back on Main, before anything is persisted: a cancelled form or
                // a computer the user picked meanwhile wins, so this attempt must
                // not add a record or move the default computer.
                if (!isCurrentIntent(gen)) return@launch
                val record = withContext(Dispatchers.IO) {
                    val saved = attempt.store.load()
                    if (saved == null || saved.hubId.isBlank()) {
                        null
                    } else {
                        // Only now does the attempt become a saved computer: the
                        // Hub revealed its ID and accepted this device.
                        vault.save(
                            saved.copy(
                                baseUrl = endpoint.origin,
                                hubName = info.hub?.name?.takeIf { it.isNotBlank() }
                                    ?: saved.hubName,
                            ),
                        )
                        vault.load(saved.hubId)
                    }
                }
                val hubId = record?.hubId
                if (hubId == null) {
                    _state.update {
                        it.copy(loggingIn = false, loginError = app.getString(R.string.error_network))
                    }
                    return@launch
                }
                // A client for that computer only: its own cookie jar was just
                // filled by the pairing response, so the inbox opens without a
                // second round trip. Its limit comes from that same record.
                val api = withContext(Dispatchers.IO) {
                    container.newHubClient(hubId, endpoint) { onUnauthorized(hubId, it) }
                }
                // Still the same attempt? A cancel or switch that happened while
                // the client was being built must not be overridden by an activation.
                if (!isCurrentIntent(gen)) return@launch
                // activateHub persists the default computer and the address/name
                // preferences in one step under its own intent.
                activateHub(hubId, api, beginIntent(hubId), endpoint.origin)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: ApiException) {
                if (!isCurrentIntent(gen)) return@launch
                _state.update { it.copy(loggingIn = false, loginError = pairErrorMessage(e)) }
            } catch (e: Exception) {
                if (!isCurrentIntent(gen)) return@launch
                _state.update {
                    it.copy(loggingIn = false, loginError = app.getString(R.string.error_network))
                }
            }
        }
    }

    /** User-initiated reconnect of the already paired Hub; no code required. */
    fun retryConnection() {
        val hubId = activeHubId ?: _state.value.activeHubId
        if (hubId == null) {
            // Nothing is bound: a revoked last computer leaves the login screen
            // with a saved pairing but no active hub.
            _state.update { it.copy(loggingIn = false) }
            return
        }
        if (_state.value.loggingIn) return
        _state.update { it.copy(loggingIn = true, loginError = null) }
        // The user explicitly asked for this computer: its bind may publish.
        val gen = beginIntent(hubId)
        viewModelScope.launch {
            handleConnectResult(gen, hubId, connectorFor(hubId).connect(ConnectTrigger.MANUAL))
        }
    }

    private fun pairErrorMessage(e: ApiException): String = when {
        e.isNetwork -> app.getString(R.string.error_network)
        e.status == 403 && e.code == "origin_not_allowed" ->
            "Hub 拒绝了该地址：请把 ${_state.value.hubAddress} 的主机名加入 Hub 的 allowed_hosts"
        e.status == 429 || e.code == "pairing_locked" -> "配对码已锁定，请在 Hub 上重新生成"
        e.status == 401 || e.code == "pairing_invalid" -> "配对码无效或已过期，请在 Hub 上重新查看"
        else -> e.message
    }

    // ---------- reconnect (paired Hub) ----------

    /**
     * LAN first, then Tailscale, then any other saved address; only an address
     * whose credential-free probe reports the expected Hub ID is accepted.
     *
     * Caller cancellation must reach the connector so a backgrounded/finished
     * round really stops probing; only probe failures become "no route".
     */
    private suspend fun pickPairedCandidate(hubId: String, saved: SavedPairing): RouteCandidate? {
        // The address that answered last time is the best guess after a cold
        // start, and it is known without probing anything else.
        val preferred = vault.load(hubId)?.baseUrl.orEmpty()
        _state.update {
            it.copy(
                boot = BootProgress(
                    step = BootStep.SELECTING_ADDRESS,
                    address = preferred.ifBlank { it.boot.address },
                    candidates = saved.candidates.size,
                ),
            )
        }
        val chosen = try {
            routeSelector.select(saved.hubId, saved.candidates, preferredUrl = preferred)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        }
        if (chosen != null) {
            _state.update {
                it.copy(
                    boot = it.boot.copy(step = BootStep.RESTORING_SESSION, address = chosen.url),
                )
            }
        }
        return chosen
    }

    /** IO half of a bind; nothing in here publishes state to the UI. */
    private sealed interface BindOutcome {
        class Ready(val api: ApiClient) : BindOutcome
        class Failed(val result: ConnectResult) : BindOutcome
    }

    /**
     * Binds one verified candidate of [hubId]: move *that Hub's* record to the
     * verified origin ([SessionStore.switchToPaired] — never `clear`, which
     * would drop the pairing), build a client bound to that Hub's store, then
     * rebuild its cookies/CSRF from its stored device token.
     *
     * The store handle belongs to [hubId] alone, so this can neither read nor
     * write another computer's cookies, token or saved address. Only the
     * request work runs on [Dispatchers.IO]; every ViewModel side effect is
     * applied on the caller's dispatcher, i.e. Main. Nothing is published
     * unless the restore succeeded, so a failed round leaves the previous
     * client and both pairings untouched.
     */
    private suspend fun bindPaired(hubId: String, candidate: RouteCandidate): ConnectResult {
        val stored = vault.load(hubId) ?: return ConnectResult.NotPaired
        if (stored.hubId.isBlank() || stored.hubId != hubId) return ConnectResult.NotPaired
        val endpoint = HubEndpoint.parse(candidate.url) ?: return ConnectResult.NoRoute

        val outcome = withContext(Dispatchers.IO) {
            val store = vault.store(hubId)
            if (!store.switchToPaired(endpoint, hubId)) {
                return@withContext BindOutcome.Failed(ConnectResult.NoRoute)
            }
            val api = container.newHubClient(hubId, endpoint) { onUnauthorized(hubId, it) }
            try {
                // The candidate came from the selector, which probed exactly
                // this origin and matched the Hub ID a moment ago.
                api.restorePairedSession(hubId, originVerified = true)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: ApiException) {
                return@withContext if (e.code == CODE_PAIRING_REJECTED) {
                    BindOutcome.Failed(ConnectResult.PairingRejected)
                } else {
                    // Temporary failure: pairing and previous client stay.
                    BindOutcome.Failed(ConnectResult.NoRoute)
                }
            } catch (_: Exception) {
                return@withContext BindOutcome.Failed(ConnectResult.NoRoute)
            }
            BindOutcome.Ready(api)
        }

        return when (outcome) {
            is BindOutcome.Failed -> outcome.result
            is BindOutcome.Ready -> {
                // The user may have picked another computer while this round was
                // restoring. Opening the wrong inbox (or reopening one the user
                // already left) is worse than discarding this result.
                if (intendedHubId != hubId) return ConnectResult.Deferred
                // Publish (and persist the last-used computer) as one step under
                // the intent this bind belongs to; a newer request wins even if
                // it was issued while this restore was finishing.
                if (!activateHub(hubId, outcome.api, intentGen, candidate.url)) {
                    return ConnectResult.Deferred
                }
                ConnectResult.Bound(candidate)
            }
        }
    }

    /**
     * Makes [api] the live client of [hubId] and nothing else.
     *
     * Everything that belonged to the previous computer is dropped *here*, at
     * the moment the new session is known to work: its event stream, its list
     * and paging cursor, its online list, its cached previews and its generation
     * tokens. Late responses, socket callbacks and 401s of the old client are
     * rejected by the hub identity checks long before they can reach [UiState].
     * A running upload or download is not cancelled or replayed: it keeps the
     * client, address and identity it started with.
     */
    private fun activateHub(hubId: String, api: ApiClient, gen: Long, address: String? = null): Boolean {
        if (!isCurrentIntent(gen, hubId)) return false
        val previous = activeHubId
        // Always close the previous socket and drop its instance, even when the
        // Hub ID stays the same: a rebind for the same computer builds a *new*
        // ApiClient, and the old stream would otherwise keep asking the replaced
        // client (and stay silent forever, because startEventStream() refuses to
        // create a second one).
        eventStream?.stop()
        eventStream = null
        if (previous != hubId) {
            requestSeq += 1
            loadMoreSeq += 1
            clearPresence()
            pendingCreates.clear()
            snapshotting.set(false)
            images.clear()
            // The reconnect loop belongs to the computer that was on screen.
            outboxReconnectJob?.cancel()
            outboxReconnectJob = null
        }
        activeHubId = hubId
        client = api
        // Remember this computer for the next start. Persisted here, under the
        // same intent protection as the UI effect below: a stale or failed round
        // never overwrites the last usable computer with a target it did not open.
        runCatching { vault.setActive(hubId) }
        val record = vault.load(hubId)
        val savedAddress = address
            ?: record?.baseUrl?.takeIf { it.isNotBlank() }
            ?: _state.value.hubAddress
        prefs.hubAddress = savedAddress
        val name = record?.hubName.orEmpty()
        if (name.isNotBlank()) prefs.hubName = name
        _state.update {
            it.copy(
                phase = Phase.INBOX,
                activeHubId = hubId,
                addingHub = false,
                hasStoredPairing = true,
                hubAddress = savedAddress,
                hubName = name.ifBlank { it.hubName },
                loginError = null,
                loggingIn = false,
                maxUploadBytes = api.maxUploadBytes,
                conn = ConnState.CLOSED,
                boot = BootProgress(),
                // The previous computer's inbox must never be shown as this
                // one's while the first page is being fetched.
                items = if (previous == hubId) it.items else emptyList(),
                nextCursor = if (previous == hubId) it.nextCursor else "",
                loading = false,
                loadingMore = false,
                error = null,
            )
        }
        refreshSavedHubs()
        publishTransfers()
        enterInbox()
        return true
    }

    private suspend fun handleConnectResult(gen: Long, hubId: String, result: ConnectResult) {
        if (!isCurrentIntent(gen, hubId)) {
            // The user already moved on: a late result has no side effects at all.
            // In particular a stale PairingRejected must not delete the record of
            // this Hub ID, because a newer pairing may already have replaced it;
            // an actually revoked device is still detected by the next valid
            // restore (the Hub answers authenticated:false).
            return
        }
        when (result) {
            is ConnectResult.Bound ->
                // activateHub already ran inside bindPaired under this intent.
                publishTransfers()
            ConnectResult.PairingRejected -> onPairingRevoked(hubId)
            ConnectResult.NotPaired -> onNotPaired(hubId)
            ConnectResult.NoRoute -> onNoRoute(hubId)
            ConnectResult.Deferred -> onDeferred(hubId)
        }
    }

    /**
     * The round was abandoned (backgrounded) or joined another round that was
     * abandoned. Never leave the UI spinning or stuck on the boot screen: the
     * pairing is kept and a foreground event starts a fresh round.
     */
    private fun onDeferred(hubId: String) {
        // A share confirmed for this computer is already durable in its queue; it
        // is sent by a later bind of that same computer, never by another one.
        // The abandoned target is no longer what the user wants; hand the intent
        // back to the computer that is on screen so automatic reconnect works.
        if (hubId != activeHubId) beginIntent(activeHubId)
        _state.update {
            it.copy(
                loggingIn = false,
                phase = if (it.phase == Phase.BOOTING) Phase.LOGIN else it.phase,
            )
        }
    }

    /**
     * Paired but unreachable (offline, Hub ID mismatch, no candidate answered):
     * keep every credential and show a retry affordance instead of pairing again.
     *
     * Only [hubId] is affected. If another computer is live, it stays live and
     * keeps its inbox; the failed switch says so and sends nothing anywhere.
     */
    private fun onNoRoute(hubId: String) {
        val message = app.getString(R.string.error_network)
        val live = activeHubId
        val switching = live != null && live != hubId
        if (switching) {
            // The computer on screen keeps working; only this switch failed.
            _state.update { it.copy(loggingIn = false) }
            toast("无法连接「${hubDisplayName(hubId)}」，仍停留在当前电脑", true)
            // The newest intent is "stay on the live computer" again, so a later
            // automatic reconnect of it may publish.
            beginIntent(live)
            scheduleOutboxReconnect()
            return
        }
        // Cold start (or a dropped session) with a paired computer that is
        // temporarily unreachable: show a usable inbox for *that* computer so the
        // user can still write/queue, and let the refresh button reconnect.
        val previous = activeHubId
        val record = vault.load(hubId)
        activeHubId = hubId
        if (record != null) runCatching { vault.setActive(hubId) }
        val address = record?.baseUrl?.takeIf { it.isNotBlank() } ?: _state.value.hubAddress
        val name = record?.hubName.orEmpty()
        if (address.isNotBlank()) prefs.hubAddress = address
        clearPresence()
        _state.update {
            it.copy(
                phase = Phase.INBOX,
                activeHubId = hubId,
                hasStoredPairing = true,
                addingHub = false,
                hubAddress = address,
                hubName = name.ifBlank { it.hubName },
                loginError = null,
                loggingIn = false,
                conn = ConnState.CLOSED,
                loading = false,
                loadingMore = false,
                error = message,
                boot = BootProgress(),
                // Never show another computer's inbox as this one's.
                items = if (previous == hubId) it.items else emptyList(),
                nextCursor = if (previous == hubId) it.nextCursor else "",
            )
        }
        requestSeq += 1
        loadMoreSeq += 1
        publishTransfers()
        publishOutbox()
        // Nothing is bound, so no selection stays "pending": a later foreground
        // event, the refresh button or the queue's own backoff may retry.
        beginIntent(hubId)
        scheduleOutboxReconnect()
    }

    /** The round found no stored pairing for [hubId] anymore. */
    private fun onNotPaired(hubId: String) {
        val live = activeHubId
        if (live != null && live != hubId) {
            // A computer that is still usable stays on screen; only the vanished
            // target is reported.
            refreshSavedHubs()
            beginIntent(live)
            _state.update { it.copy(loggingIn = false) }
            toast("该电脑未配对，请重新配对", true)
            return
        }
        if (live == hubId) {
            teardownClient(hubId, client)
            activeHubId = null
        }
        refreshSavedHubs()
        beginIntent(null)
        _state.update {
            it.copy(
                phase = Phase.LOGIN,
                activeHubId = null,
                hasStoredPairing = false,
                loggingIn = false,
                pairingCode = "",
                items = emptyList(),
                nextCursor = "",
                conn = ConnState.CLOSED,
            )
        }
    }

    /**
     * The Hub answered `authenticated:false` (HTTP 200) for our stored device
     * token: this device was revoked for [hubId]. Only that one computer's
     * record is cleared; the other paired computers stay paired and any of them
     * can be selected instead.
     */
    private fun onPairingRevoked(hubId: String) {
        val name = hubDisplayName(hubId)
        val live = activeHubId
        if (live == hubId) teardownClient(hubId, client) else cancelTransfers(hubId)
        // Only this computer's record goes away; every other pairing (and its
        // credentials) is untouched.
        runCatching { vault.remove(hubId) }
        refreshSavedHubs()
        if (live != null && live != hubId) {
            // The revoked computer was a switch target: the working one stays.
            beginIntent(live)
            _state.update { it.copy(loggingIn = false) }
            toast("「$name」已在 Hub 上解除配对", true)
            return
        }
        if (live == hubId) activeHubId = null
        val next = _state.value.savedHubs.firstOrNull()
        _state.update {
            it.copy(
                phase = if (next != null) Phase.BOOTING else Phase.LOGIN,
                activeHubId = next?.id,
                hasStoredPairing = next != null,
                hubName = "",
                pairingCode = "",
                loggingIn = false,
                items = emptyList(),
                nextCursor = "",
                conn = ConnState.CLOSED,
                error = null,
                boot = BootProgress(),
                loginError = if (next == null) {
                    "此设备已在 Hub 上解除配对，请重新输入配对码"
                } else {
                    "「$name」已在 Hub 上解除配对"
                },
            )
        }
        if (next != null) selectHub(next.id) else beginIntent(null)
    }

    // ---------- multiple computers ----------

    /**
     * Switches the inbox to [hubId].
     *
     * The switch is published only after that computer's session was verified
     * and bound: until then the current inbox keeps working, so a failed or
     * cancelled switch simply stays where the user was. No write is replayed and
     * no transfer is redirected to the new computer.
     */
    fun selectHub(hubId: String) {
        if (hubId == activeHubId) {
            // Choosing the computer that is already on screen cancels a pending
            // switch to another one: the user's last choice wins instead of the
            // earlier round opening its own inbox moments later.
            connectors.values.forEach { it.cancel() }
            beginIntent(hubId)
            _state.update { it.copy(addingHub = false, loggingIn = false, loginError = null) }
            if (_state.value.phase == Phase.LOGIN) resumeActiveHub()
            return
        }
        if (vault.load(hubId) == null) {
            toast("该电脑未配对，请重新配对", true)
            refreshSavedHubs()
            return
        }
        // A share confirmed for another computer is durable in that computer's
        // queue: this switch neither sends it nor deletes it.
        // Abandon the previous selection so it cannot finish after this one.
        connectors.entries.filter { it.key != hubId }.forEach { it.value.cancel() }
        if (intendedHubId == hubId && _state.value.loggingIn) {
            // This hub is already being connected (a joined round): keep that
            // round and its intent instead of restarting the request identity.
            _state.update { it.copy(addingHub = false) }
            return
        }
        val gen = beginIntent(hubId)
        _state.update { it.copy(addingHub = false, loggingIn = true, loginError = null) }
        viewModelScope.launch {
            handleConnectResult(gen, hubId, connectorFor(hubId).connect(ConnectTrigger.MANUAL))
        }
    }

    /** Starts pairing an additional computer; existing pairings are untouched. */
    fun beginAddHub() {
        if (_state.value.addingHub) return
        // No computer may publish from now on: a round that is still reconnecting
        // the active computer must not open its inbox on top of the pairing form.
        connectors.values.forEach { it.cancel() }
        beginIntent(null)
        _state.update {
            it.copy(
                addingHub = true,
                pairingCode = "",
                loginError = null,
                phase = Phase.LOGIN,
                // The pairing screen decides from this flag whether it offers a
                // code form or a "reconnect" button: while adding a computer the
                // code form is what the user needs, even though the active
                // computer's pairing is kept untouched.
                hasStoredPairing = false,
                loggingIn = false,
            )
        }
        // No background event stream for a computer the user is not using.
        eventStream?.stop()
        eventStream = null
        _state.update { it.copy(conn = ConnState.CLOSED, presenceFresh = false) }
    }

    /**
     * Leaves the add-computer form. Nothing was written by the attempt, so the
     * previously active computer can be resumed as it was.
     */
    fun cancelAddHub() {
        discovery.clear()
        // A pairing that is still in flight is invalidated here: its late answer
        // must not open the new computer or overwrite this screen.
        beginIntent(activeHubId)
        _state.update {
            it.copy(
                addingHub = false,
                pairingCode = "",
                loginError = null,
                loggingIn = false,
                hasStoredPairing = it.savedHubs.isNotEmpty(),
            )
        }
        if (activeHubId != null && client != null) {
            _state.update { it.copy(phase = Phase.INBOX) }
            resumeActiveHub()
        } else {
            _state.update { it.copy(phase = Phase.LOGIN) }
        }
    }

    /** Re-opens the socket and refetches the list of the already bound hub. */
    private fun resumeActiveHub() {
        val hubId = activeHubId ?: return
        if (client == null) {
            // The pairing was kept but its client was dropped (401): run a real
            // reconnect round. selectHub() cannot be used here, it delegates
            // back to this method for the active hub.
            if (_state.value.loggingIn) return
            _state.update { it.copy(loggingIn = true, loginError = null) }
            val gen = beginIntent(hubId)
            viewModelScope.launch {
                handleConnectResult(gen, hubId, connectorFor(hubId).connect(ConnectTrigger.MANUAL))
            }
            return
        }
        _state.update { it.copy(phase = Phase.INBOX) }
        startEventStream()
        resync()
        drainOutbox()
    }

    private fun hubDisplayName(hubId: String): String {
        val saved = _state.value.savedHubs.firstOrNull { it.id == hubId }
        return saved?.name?.takeIf { it.isNotBlank() }
            ?: vault.load(hubId)?.hubName?.takeIf { it.isNotBlank() }
            ?: saved?.address
            ?: "电脑"
    }

    // ---------- nearby Hub discovery (address hint only) ----------

    /** One automatic short search when the foreground pairing screen appears. */
    fun onLoginScreenShown() {
        val snapshot = _state.value
        if (snapshot.phase != Phase.LOGIN) return
        // A paired device that adds another computer searches too: the form is
        // the same form and the address hint is not a credential.
        if (snapshot.hasStoredPairing && !snapshot.addingHub) return
        discovery.start(auto = true)
    }

    /** Leaving the login screen stops NSD and drops the stale rows. */
    fun onLoginScreenLeft() {
        discovery.clear()
    }

    fun searchNearbyHubs() {
        if (_state.value.phase != Phase.LOGIN) return
        discovery.start()
    }

    fun refreshNearbyHubs() {
        if (_state.value.phase != Phase.LOGIN) return
        discovery.restart()
    }

    fun stopHubDiscovery() {
        discovery.stop()
    }

    /**
     * Selecting a nearby Hub only prefills its address. No pairing attempt, no
     * session restore and no cookie or token is sent to a discovered server: the
     * user still has to submit a code from that Hub's settings screen.
     */
    fun selectDiscoveredHub(hub: DiscoveredHub) {
        if (_state.value.phase != Phase.LOGIN) return
        _state.update { it.copy(hubAddress = hub.baseUrl, loginError = null) }
        // Release multicast/resolve resources; the rows stay visible.
        discovery.stop()
    }

    /**
     * Signs the *active* computer out: revokes this device on that Hub and
     * forgets it locally. Other paired computers keep their sessions and the
     * next one is opened, so a logout never strands the user on an empty form.
     */
    fun logout() {
        val hubId = activeHubId
        val api = client
        // Logout owns the intent while its network call runs. If the user picks a
        // computer meanwhile, that newer intent wins and this completion only
        // forgets the record instead of tearing down what is now on screen.
        val gen = beginIntent(hubId)
        viewModelScope.launch {
            if (api != null) {
                // Revokes this device on that one Hub, then forgets it locally.
                runCatching { withContext(Dispatchers.IO) { api.logout() } }
            }
            if (hubId != null) {
                if (activeHubId == hubId) teardownClient(hubId, api)
                runCatching { vault.remove(hubId) }
            }
            if (!isCurrentIntent(gen) || intendedHubId != hubId) {
                // A newer request took over while this ran: keep its UI/active.
                refreshSavedHubs()
                if (hubId != null && activeHubId == hubId) activeHubId = null
                return@launch
            }
            activeHubId = null
            refreshSavedHubs()
            val next = _state.value.savedHubs.firstOrNull()
            _state.update {
                it.copy(
                    phase = if (next != null) Phase.BOOTING else Phase.LOGIN,
                    activeHubId = next?.id,
                    hasStoredPairing = next != null,
                    hubName = "",
                    pairingCode = "",
                    loggingIn = false,
                    items = emptyList(),
                    nextCursor = "",
                    error = null,
                    conn = ConnState.CLOSED,
                    boot = BootProgress(),
                )
            }
            if (next != null) selectHub(next.id)
        }
    }

    /**
     * Drops the live client of [hubId] without touching its stored pairing: a
     * 401 only means that cookie session is gone, and its device token may still
     * be able to restore it. The hub id stays as the identity being shown, so a
     * retry reconnects the same computer.
     */
    private fun teardownClient(hubId: String, api: ApiClient?) {
        // Only the exact client that failed may be dropped: a late 401 of a
        // replaced client (same hub, A->B->A) must not dismantle the new session.
        if (activeHubId == hubId && (api == null || client === api)) {
            eventStream?.stop()
            eventStream = null
            client = null
        }
        cancelTransfers(hubId)
        images.clear()
        // Invalidate in-flight first-page/load-more responses of the old session.
        requestSeq += 1
        loadMoreSeq += 1
        clearPresence()
        pendingCreates.clear()
        snapshotting.set(false)
    }

    /**
     * Cancels the running transfers of one computer and tells the user.
     *
     * Transfers of other computers are untouched: an upload started against Hub
     * A keeps running (and completes on A) while Hub B is being used. Nothing is
     * ever replayed; a cancelled write is the user's to retry.
     */
    private fun cancelTransfers(hubId: String, notice: String? = null) {
        // Snapshot then clear: each transfer's own coroutine removes its entry
        // from the other thread, so the maps are never iterated live.
        val pending = uploadJobs.entries.filter { it.value.hubId == hubId }.map { it.key }
        pending.forEach { key -> uploadJobs.remove(key)?.job?.cancel() }
        val pendingDownloads = downloadJobs.entries.filter { it.value.hubId == hubId }.map { it.key }
        // Only cancel: each job's own finally releases its slot, the same rule
        // cancelDownload follows, so a cancelled transfer never leaves a key
        // pointing at a job that is no longer running.
        pendingDownloads.forEach { key -> downloadJobs[key]?.job?.cancel() }
        publishTransfers()
        if (pending.isNotEmpty() || pendingDownloads.isNotEmpty()) {
            notice?.let { toast(it, error = true) }
        }
    }

    private fun onUnauthorized(hubId: String, api: ApiClient) {
        // May be raised from an OkHttp callback: hop to Main before touching
        // sockets, transfer maps or UI state. Both the hub *and* the client
        // instance are checked, so a 401 of a computer the user is not looking at,
        // or of a client that was already replaced, disturbs nothing.
        viewModelScope.launch {
            if (!isLive(hubId, api)) return@launch
            if (_state.value.phase != Phase.INBOX) return@launch
            teardownClient(hubId, api)
            val paired = vault.load(hubId)?.hubId?.isNotBlank() == true
            _state.update {
                it.copy(
                    phase = Phase.LOGIN,
                    hasStoredPairing = paired,
                    activeHubId = hubId,
                    items = emptyList(),
                    nextCursor = "",
                    conn = ConnState.CLOSED,
                    pairingCode = "",
                    loggingIn = false,
                    loginError = app.getString(R.string.error_unauthorized),
                )
            }
        }
    }

    private fun enterInbox() {
        discovery.clear()
        _state.update {
            it.copy(
                phase = Phase.INBOX,
                hasStoredPairing = true,
                loginError = null,
                loggingIn = false,
                conn = ConnState.CLOSED,
                deviceName = prefs.deviceName,
            )
        }
        if (!foreground) {
            // The Activity went to background while this round finished: keep
            // the inbox state, but open no socket and scan nothing in background.
            // onForeground restarts the stream and refetches the list.
            eventStream?.stop()
            eventStream = null;
            return
        }
        startEventStream()
        resync()
        drainOutbox()
    }

    /**
     * A dropped link or a failed request asks for a reselection of the *active*
     * computer. The connector coalesces concurrent requests and throttles
     * failure-driven ones, so a burst of socket callbacks can never open a
     * second link.
     */
    private fun requestReselect(trigger: ConnectTrigger) {
        if (!foreground) return
        val hubId = activeHubId ?: return
        if (vault.load(hubId)?.hubId?.isNotBlank() != true) return
        if (_state.value.phase == Phase.BOOTING) return
        // An automatic reselect of the computer on screen must never take over a
        // switch the user just asked for.
        if (selectionPending()) return
        val gen = beginIntent(hubId)
        viewModelScope.launch { handleConnectResult(gen, hubId, connectorFor(hubId).connect(trigger)) }
    }


    // ---------- realtime ----------

    /** True while [api] is still the live client of [hubId]. */
    private fun isLive(hubId: String, api: ApiClient): Boolean =
        activeHubId == hubId && client === api

    private fun startEventStream() {
        // Never open a socket from a background state, whatever the caller path.
        if (!foreground) return
        if (eventStream != null) return
        val hubId = activeHubId ?: return
        val api = client ?: return
        eventStream = EventStream(
            scope = viewModelScope,
            // Only the active computer's own client may open a socket.
            clientProvider = { if (isLive(hubId, api)) api else null },
            onEvent = { type, id ->
                // Socket callbacks arrive on the OkHttp dispatcher thread, while
                // category switches and resync bookkeeping run on Main. Hop to
                // Main so the request generation tokens and the snapshotting
                // window are only ever touched from one thread.
                viewModelScope.launch {
                    // A socket of a computer that is no longer active can still
                    // deliver callbacks until it is closed; none of them may
                    // reach the inbox that is on screen now.
                    if (!isLive(hubId, api)) return@launch
                    when (type) {
                        "resync" -> resync()
                        "item.created" -> if (id.isNotEmpty()) onItemCreated(id)
                        "item.deleted" -> {
                            pendingCreates.remove(id)
                            _state.update { s -> s.copy(items = s.items.filterNot { it.id == id }) }
                        }
                        // An invalidation notice only: refetch presence, never
                        // the item list (no polling, no item churn).
                        EVENT_PRESENCE_CHANGED -> refreshPresence()
                    }
                }
            },
            // Any connection change (including OPEN) invalidates the current
            // online snapshot: it has to be fetched again after a reconnect.
            onState = { conn ->
                if (isLive(hubId, api)) {
                    _state.update { it.copy(conn = conn, presenceFresh = false) }
                    // A (re)opened socket is a reliable "we are reachable again"
                    // moment: pick up anything queued while it was down.
                    if (conn == ConnState.OPEN) viewModelScope.launch { drainOutbox(hubId) }
                }
            },
            onUnauthorized = { if (isLive(hubId, api)) onUnauthorized(hubId, api) },
            onUnavailable = { if (isLive(hubId, api)) requestReselect(ConnectTrigger.STREAM_FAILED) },
        ).also { it.start() }
    }

    /**
     * Called from the Activity when it becomes visible again.
     *
     * The active computer reselects through its own connector — LAN first, then
     * Tailscale — so coming back to the home LAN does not keep the Tailscale
     * address that was good on the road. The startup round and this one coalesce
     * into a single selection (the connector owns that), including the case where
     * a background event cancelled the boot restore.
     */
    fun onForeground() {
        foreground = true
        // Registered before any round starts: a network that came up while the
        // app was away must be able to win the selection that follows.
        // The callback arrives on a ConnectivityManager thread, so hop to Main
        // before touching any view-model state.
        networkWatcher.start {
            viewModelScope.launch { requestReselect(ConnectTrigger.NETWORK_CHANGED) }
        }
        val snapshot = _state.value
        val hubId = activeHubId
        // A switch the user started before backgrounding keeps its intent: the
        // foreground round of the old computer must not preempt it.
        if (!snapshot.addingHub && hubId != null && !selectionPending() &&
            (snapshot.phase == Phase.BOOTING || snapshot.hasStoredPairing)
        ) {
            val gen = beginIntent(hubId)
            viewModelScope.launch {
                handleConnectResult(gen, hubId, connectorFor(hubId).connect(ConnectTrigger.FOREGROUND))
            }
        }
        if (snapshot.phase != Phase.INBOX) {
            // The pairing form is on screen: no socket for a computer that is
            // not being used.
            return
        }
        // Keep the current link usable while the reselection runs; a winning
        // bind replaces this stream, so there is never more than one link.
        eventStream?.let { it.restart() } ?: startEventStream()
        resync()
        scheduleOutboxReconnect()
    }

    /** Called when the Activity goes to the background: no sockets, no probing. */
    fun onBackground() {
        foreground = false
        networkWatcher.stop()
        eventStream?.stop()
        // No background retry of the offline queue: it resumes on foreground.
        outboxReconnectJob?.cancel()
        outboxReconnectJob = null
        // No background discovery: stop NSD and release the multicast lock.
        discovery.stop()
        // Abandon every half-finished selection round; they are redone on
        // foreground. Other computers have no round running to cancel.
        connectors.values.forEach { it.cancel() }
    }

    // ---------- inbox ----------

    fun resync() {
        val hubId = activeHubId ?: return
        val api = client
        if (api == null) {
            // The refresh button (and a foreground resync) must reconnect an
            // offline inbox instead of silently doing nothing.
            reconnectActiveHub()
            return
        }
        // Every socket (re)open, foreground resync, first authenticated load and
        // explicit refresh also refreshes the online list. It has its own
        // generation, so this never interferes with the item list below.
        refreshPresence()
        // No "already loading" early return: a category switch must be able to
        // replace the first page that is still in flight. The generation token
        // makes the newest request win instead.
        val token = ++requestSeq
        val wanted = _state.value.category
        snapshotting.set(true)
        _state.update { it.copy(loading = true) }
        viewModelScope.launch {
            try {
                val page = withContext(Dispatchers.IO) {
                    api.list(null, ApiClient.PAGE_SIZE, wanted)
                }
                if (requestSeq != token || !isLive(hubId, api)) return@launch
                val known = page.items.mapTo(HashSet()) { it.id }
                _state.update {
                    it.copy(
                        items = page.items,
                        nextCursor = page.nextCursor,
                        loading = false,
                        error = null,
                        maxUploadBytes = api.maxUploadBytes,
                    )
                }
                snapshotting.set(false)
                // Items created while the snapshot was in flight may be missing
                // from it; they are re-fetched once the page has landed.
                val missed = pendingCreates.filterNot { it in known }
                pendingCreates.clear()
                for (id in missed) fetchAndInsert(id)
                drainOutbox(hubId)
            } catch (e: ApiException) {
                if (requestSeq != token || !isLive(hubId, api)) return@launch
                if (e.isUnauthorized) {
                    onUnauthorized(hubId, api)
                } else {
                    _state.update { it.copy(loading = false, error = friendly(e)) }
                    // A read may be retried freely; the user never has to retype
                    // anything, and no write is replayed on the new address.
                    if (e.isNetwork) requestReselect(ConnectTrigger.NETWORK_LOST)
                }
            } catch (e: Exception) {
                if (requestSeq != token || !isLive(hubId, api)) return@launch
                _state.update { it.copy(loading = false, error = friendly(e)) }
            } finally {
                // A newer request owns the flags now; never clear its spinner.
                if (requestSeq == token) {
                    snapshotting.set(false)
                    _state.update { if (it.loading) it.copy(loading = false) else it }
                }
            }
        }
    }

    // ---------- presence (read-only online list) ----------

    /**
     * Refreshes the online device list.
     *
     * Runs on its own generation and on the ViewModel scope (Main dispatcher),
     * so it is ordered independently of category/item requests and of the
     * socket callbacks that queue it here. Failures stay local: names already
     * shown are kept and marked stale, sending/receiving is untouched, and only
     * a 401 leaves through the shared [onUnauthorized] path.
     */
    fun refreshPresence() {
        val hubId = activeHubId ?: return
        val api = client ?: return
        val token = ++presenceSeq
        _state.update { it.copy(presenceLoading = true) }
        viewModelScope.launch {
            try {
                val response = withContext(Dispatchers.IO) { api.presence() }
                if (!isCurrentPresence(hubId, api, token)) return@launch
                _state.update {
                    it.copy(
                        onlineDevices = response.online,
                        presenceLoading = false,
                        presenceError = null,
                        presenceFresh = true,
                    )
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: Exception) {
                if (!isCurrentPresence(hubId, api, token)) return@launch
                if (e is ApiException && e.isUnauthorized) {
                    // Never hide an auth failure: the shared path drops the
                    // session and asks the user to pair again.
                    _state.update { it.copy(presenceLoading = false) }
                    onUnauthorized(hubId, api)
                } else {
                    // Keep the previous names but stop claiming they are live.
                    _state.update {
                        it.copy(
                            presenceLoading = false,
                            presenceError = friendly(e),
                            presenceFresh = false,
                        )
                    }
                }
            }
        }
    }

    /**
     * Only the newest refresh of the *current* Hub may write to [UiState].
     * The hub id is checked as well as the client, so an answer for a computer
     * that was left behind cannot be written into the visible inbox.
     */
    private fun isCurrentPresence(hubId: String, api: ApiClient, token: Int): Boolean =
        presenceSeq == token && isLive(hubId, api)

    /** Forgets the online list because the session identity changed. */
    private fun clearPresence() {
        presenceSeq += 1
        _state.update {
            it.copy(
                onlineDevices = emptyList(),
                presenceLoading = false,
                presenceError = null,
                presenceFresh = false,
            )
        }
    }

    // ---------- offline outbox ----------

    /** Republishes the queue, which always holds the entries of *every* computer. */
    private fun publishOutbox() {
        // A slower, older read must never overwrite a newer queue snapshot.
        val token = ++outboxPublishSeq
        viewModelScope.launch {
            val entries = withContext(Dispatchers.IO) { runCatching { outbox.list() }.getOrNull() }
                ?: return@launch
            if (token != outboxPublishSeq) return@launch
            _state.update { it.copy(outbox = entries) }
        }
    }

    /**
     * Conditional status change on the shared store.
     *
     * The read and the write happen under the store's own monitor (its methods
     * are synchronized on the instance), so an explicit retry/remove racing a
     * drain can never cancel a request that is already on the wire or turn a
     * SENDING entry back into PENDING. Must be called from [Dispatchers.IO] (or
     * any caller that is allowed to wait for the store lock).
     */
    private fun transitionOutbox(
        id: String,
        from: Set<OutboxStatus>,
        to: OutboxStatus,
        failure: String? = null,
    ): OutboxTransition = synchronized(outbox) {
        val all = runCatching { outbox.list() }.getOrElse { return@synchronized OutboxTransition.FAILED }
        val current = all.firstOrNull { it.id == id } ?: return@synchronized OutboxTransition.GONE
        if (current.status !in from) return@synchronized OutboxTransition.SKIPPED
        val applied = runCatching { outbox.setStatus(id, to, failure) }.getOrDefault(false)
        if (applied) OutboxTransition.APPLIED else OutboxTransition.FAILED
    }

    private fun removeOutboxAtomically(id: String, allowSending: Boolean): OutboxTransition = synchronized(outbox) {
        val all = runCatching { outbox.list() }.getOrElse { return@synchronized OutboxTransition.FAILED }
        val current = all.firstOrNull { it.id == id } ?: return@synchronized OutboxTransition.GONE
        if (!allowSending && current.status == OutboxStatus.SENDING) {
            return@synchronized OutboxTransition.SKIPPED
        }
        val removed = runCatching { outbox.remove(id) }.getOrDefault(false)
        if (removed) OutboxTransition.APPLIED else OutboxTransition.FAILED
    }

    /**
     * Upload limit of [hubId]: the live client's newest value, otherwise the
     * limit saved with that computer's pairing, otherwise the app default.
     */
    private fun hubUploadLimit(hubId: String): Long {
        val api = client
        if (activeHubId == hubId && api != null) return api.maxUploadBytes
        return vault.load(hubId)?.maxUploadBytes?.takeIf { it > 0 } ?: DEFAULT_MAX_UPLOAD_BYTES
    }

    private suspend fun outboxPending(hubId: String): Boolean = withContext(Dispatchers.IO) {
        runCatching {
            outbox.list().any { it.hubId == hubId && it.status == OutboxStatus.PENDING }
        }.getOrDefault(false)
    }

    /**
     * Drains the queue of the computer on screen through its *verified* client.
     *
     * At most one drain runs *per Hub*: every entry keeps the Hub it was written
     * for, so a switch never redirects a queued write, and an upload still open
     * for A cannot delay the drain of a B that was just connected. A request that
     * arrives while that Hub already drains is coalesced, not dropped. The
     * request in flight keeps its own client and Hub; the loop only stops
     * *between* entries when the app leaves the foreground or the user switches.
     */
    private fun drainOutbox(hubId: String? = null) {
        if (!foreground) return
        val active = activeHubId ?: return
        if (hubId != null && hubId != active) return
        val api = client ?: return
        if (_state.value.phase != Phase.INBOX) return
        if (outboxDrainJobs.containsKey(active)) {
            // Coalesce per computer instead of dropping: an item enqueued while
            // this drain runs is picked up when it finishes, even if the link
            // meanwhile reports OPEN.
            outboxDrainRequested.add(active)
            return
        }
        outboxDrainRequested.remove(active)
        val job = viewModelScope.launch {
            try {
                drainOutboxEntries(active, api)
            } finally {
                outboxDrainJobs.remove(active)
                val again = outboxDrainRequested.remove(active)
                // A->B->A: the same Hub can come back with a new client while the
                // old drain was still finishing its item, so only then restart.
                // A switch to another computer is served by that computer's job.
                if (again || (activeHubId == active && client !== api)) drainOutbox(active)
            }
        }
        outboxDrainJobs[active] = job
    }

    private suspend fun drainOutboxEntries(hubId: String, api: ApiClient) {
        // Re-read every round: items enqueued while this drain was running (a
        // second send, a confirmed share) must not wait for the next trigger.
        while (true) {
            if (!foreground || !isLive(hubId, api) || _state.value.phase != Phase.INBOX) return
            val next = withContext(Dispatchers.IO) {
                runCatching { outbox.list() }.getOrNull()
            }?.firstOrNull { it.hubId == hubId && it.status == OutboxStatus.PENDING } ?: break
            // A send that stays PENDING means the network path is still down:
            // stop this round instead of busy-retrying; the bounded backoff (or a
            // new link) wakes it up again.
            if (!sendOutboxEntry(next, hubId, api)) break
        }
        // Items may still be queued with an unhealthy link (cold start on the
        // home Wi-Fi while the Hub is off): reconnect with a small bounded
        // backoff. No-op when the queue is empty or the link is healthy.
        scheduleOutboxReconnect()
    }

    /**
     * Attempts exactly one queued item.
     *
     * Durable order: SENDING is persisted *before* any byte is written, and the
     * content is deleted only after the Hub definitely accepted it. Faults are
     * classified conservatively: only a failure proven to be before the network
     * layer goes back to PENDING (safe to replay), a definite 4xx becomes FAILED,
     * and everything that may have reached the Hub (or that cannot be read back)
     * becomes UNCONFIRMED, which is never replayed automatically.
     */
    private suspend fun sendOutboxEntry(entry: OutboxEntry, hubId: String, api: ApiClient): Boolean {
        // Conditional transition: an explicit remove (or a status the user just
        // changed) must win, and a SENDING entry may never be started twice.
        when (withContext(Dispatchers.IO) {
            transitionOutbox(entry.id, setOf(OutboxStatus.PENDING), OutboxStatus.SENDING)
        }) {
            OutboxTransition.APPLIED -> Unit
            OutboxTransition.GONE, OutboxTransition.SKIPPED -> return true // removed/handled
            OutboxTransition.FAILED -> {
                // A disk write failure must not be mistaken for "nothing to do",
                // or the same PENDING item would be read forever in a busy loop.
                toast("无法更新待发送状态，稍后重试", true)
                return false
            }
        }
        publishOutbox()
        try {
            val item = when {
                entry.text != null -> {
                    val type = if (LINK_REGEX.matches(entry.text)) "link" else "text"
                    withContext(Dispatchers.IO) { api.createText(type, entry.text, entry.senderDevice) }
                }
                else -> {
                    // The saved limit is only a staging hint; the live limit is
                    // authoritative and checked against the real staged bytes
                    // before any network write.
                    if (entry.sizeBytes > api.maxUploadBytes) {
                        setOutboxStatus(entry, OutboxStatus.FAILED, app.getString(R.string.error_upload_too_large))
                        return true
                    }
                    val source = withContext(Dispatchers.IO) { outbox.uploadSource(entry) }
                    uploadOutboxFile(entry, source, hubId, api) ?: run {
                        // The user cancelled this upload: whether the Hub already
                        // stored it is unknown, so it must not replay by itself.
                        setOutboxStatus(entry, OutboxStatus.UNCONFIRMED, "上传已取消，结果未知")
                        return true
                    }
                }
            }
            // Definite success: only now is the content dropped. If the local
            // bookkeeping fails, the entry is explicitly left as "may be sent"
            // so a later drain can never replay a delivered item.
            val removal = withContext(Dispatchers.IO) { removeOutboxAtomically(entry.id, allowSending = true) }
            if (removal == OutboxTransition.FAILED) {
                withContext(Dispatchers.IO) {
                    transitionOutbox(
                        entry.id,
                        setOf(OutboxStatus.SENDING),
                        OutboxStatus.UNCONFIRMED,
                        "已发送，但本地记录未能清理，请勿重发",
                    )
                }
                toast("「${entry.displayName}」已发送，但本地待发送记录未能清理", true)
            } else {
                // APPLIED, or GONE because the user cleared it meanwhile: the
                // item is delivered either way.
                adoptCreatedItem(item, hubId)
            }
            publishOutbox()
        } catch (cancelled: CancellationException) {
            // A cancelled write may have reached the Hub; keep it unknown. This
            // runs uncancelled and on IO, so a teardown does not lose the state
            // and the store lock is never taken on the main thread.
            withContext(NonCancellable + Dispatchers.IO) {
                transitionOutbox(
                    entry.id,
                    setOf(OutboxStatus.SENDING),
                    OutboxStatus.UNCONFIRMED,
                    "发送被中断，结果未知",
                )
            }
            throw cancelled
        } catch (e: OutboxTransportException) {
            if (e.mayHaveReachedHub) {
                setOutboxStatus(entry, OutboxStatus.UNCONFIRMED, friendly(e))
                return true
            }
            // Proven not to have left the device: a later retry cannot duplicate.
            setOutboxStatus(entry, OutboxStatus.PENDING, "网络不可达，连接恢复后重试")
            // A stale OPEN socket must not park the queue: make the route
            // selection run again instead of trusting the old link. Only the
            // computer whose client actually failed may do this — a late failure
            // of a computer the user already left must not reselect (or disturb)
            // the one now on screen.
            if (isLive(hubId, api)) requestReselect(ConnectTrigger.STREAM_FAILED)
            return false
        } catch (e: ApiException) {
            when {
                e.isUnauthorized -> {
                    // A definite rejection: the item stays queued for the session
                    // the shared 401 path rebuilds, and nothing is deleted.
                    setOutboxStatus(entry, OutboxStatus.PENDING, friendly(e))
                    onUnauthorized(hubId, api)
                    return false
                }
                e.status in 400..499 -> setOutboxStatus(entry, OutboxStatus.FAILED, friendly(e))
                // 5xx and unexpected responses: the Hub may have stored it.
                else -> setOutboxStatus(entry, OutboxStatus.UNCONFIRMED, friendly(e))
            }
        } catch (e: OutboxAttachmentMissingException) {
            setOutboxStatus(entry, OutboxStatus.FAILED, e.message ?: "附件已丢失")
        } catch (e: OutboxException) {
            // Local storage trouble: nothing was sent, so keep it queued and let
            // the bounded backoff retry later.
            setOutboxStatus(entry, OutboxStatus.PENDING, e.message ?: "本地存储异常")
            return false
        } catch (e: Exception) {
            // Bad/empty success body and anything else unexpected: unknown.
            setOutboxStatus(entry, OutboxStatus.UNCONFIRMED, friendly(e))
        }
        return true
    }

    private suspend fun setOutboxStatus(entry: OutboxEntry, status: OutboxStatus, failure: String?) {
        withContext(Dispatchers.IO) {
            transitionOutbox(entry.id, setOf(OutboxStatus.SENDING), status, failure)
        }
        publishOutbox()
    }

    /**
     * Uploads one queued attachment with the usual progress row and cancel
     * action. Returns null when the user cancelled that upload.
     *
     * The call is deliberately not part of [uploadJobs]: a session teardown
     * caused by a 401 must not turn a *definite* rejection into "interrupted,
     * unknown", and a write already on the wire keeps its client and Hub.
     */
    private suspend fun uploadOutboxFile(
        entry: OutboxEntry,
        source: UploadSource,
        hubId: String,
        api: ApiClient,
    ): Item? {
        val key = "outbox:${entry.id}"
        val task = UploadTask(key = key, name = source.name, size = entry.sizeBytes, hubId = hubId)
        uploadTasks.put(task)
        publishTransfers()
        val outcome = CompletableDeferred<Result<Item>?>()
        val job = viewModelScope.launch {
            try {
                val item = api.upload(source, entry.senderDevice) { loaded, total ->
                    updateUpload(key) { it.copy(loaded = loaded, size = total ?: it.size) }
                }
                outcome.complete(Result.success(item))
            } catch (cancelled: CancellationException) {
                outcome.complete(null)
                throw cancelled
            } catch (e: Exception) {
                outcome.complete(Result.failure(e))
            } finally {
                outboxUploadJobs.remove(key)
            }
        }
        outboxUploadJobs[key] = HubJob(hubId, job)
        val result = outcome.await()
        if (result != null && result.isSuccess) {
            updateUpload(key) { it.copy(status = TaskStatus.DONE, loaded = it.size) }
        }
        uploadTasks.remove(key)
        publishTransfers()
        return result?.getOrThrow()
    }

    /**
     * Small bounded backoff that reconnects the computer on screen while it has
     * PENDING items and no healthy link (cold start on the same Wi-Fi with the
     * Hub still off, or a socket closed after a route change).
     *
     * It never runs in the background, never takes over a user-requested switch,
     * and reuses the single-flight [PairedConnector] with its intent guard, so it
     * cannot race or busy-loop.
     */
    private fun scheduleOutboxReconnect() {
        if (!foreground) return
        val hubId = activeHubId ?: return
        if (_state.value.phase != Phase.INBOX) return
        if (outboxReconnectJob?.isActive == true) return
        outboxReconnectJob = viewModelScope.launch {
            var wait = OUTBOX_RETRY_INITIAL_MS
            try {
                while (foreground && activeHubId == hubId && _state.value.phase == Phase.INBOX) {
                    if (!outboxPending(hubId)) return@launch
                    // Always back off first: a write that just failed must not be
                    // retried in a busy loop.
                    delay(wait)
                    wait = (wait * 2).coerceAtMost(OUTBOX_RETRY_MAX_MS)
                    if (!foreground || activeHubId != hubId || _state.value.phase != Phase.INBOX) return@launch
                    if (!outboxPending(hubId)) return@launch
                    if (client != null && _state.value.conn == ConnState.OPEN) {
                        // A stale OPEN link would otherwise park the queue forever;
                        // retry the send, and a further failure forces the route
                        // selection instead of trusting that link again.
                        drainOutbox(hubId)
                    } else {
                        reconnectActiveHub()
                    }
                }
            } finally {
                outboxReconnectJob = null
            }
        }
    }

    /** One automatic reconnect round for the computer on screen, if nothing else is. */
    private fun reconnectActiveHub() {
        val hubId = activeHubId ?: return
        if (selectionPending() || _state.value.loggingIn) return
        _state.update { it.copy(loggingIn = true, loginError = null) }
        val gen = beginIntent(hubId)
        viewModelScope.launch {
            handleConnectResult(gen, hubId, connectorFor(hubId).connect(ConnectTrigger.NETWORK_LOST))
        }
    }

    /**
     * Explicit user retry of one queued item (the UI confirms UNCONFIRMED first).
     *
     * The target is never chosen here: an entry of another computer is only set
     * back to PENDING and waits for the user to switch, so it can never be
     * delivered to the wrong Hub.
     */
    fun retryOutbox(id: String) {
        viewModelScope.launch {
            val entry = withContext(Dispatchers.IO) {
                runCatching { outbox.list().firstOrNull { it.id == id } }.getOrNull()
            } ?: return@launch
            val paired = withContext(Dispatchers.IO) { vault.load(entry.hubId) != null }
            if (!paired) {
                toast("「${hubDisplayName(entry.hubId)}」已解除配对，重新配对同一台电脑后可再发送", true)
                return@launch
            }
            // Conditional: never takes an entry that a drain already marked
            // SENDING (that would replay a write already on the wire).
            when (withContext(Dispatchers.IO) {
                transitionOutbox(
                    id,
                    setOf(OutboxStatus.PENDING, OutboxStatus.UNCONFIRMED, OutboxStatus.FAILED),
                    OutboxStatus.PENDING,
                )
            }) {
                OutboxTransition.APPLIED -> Unit
                OutboxTransition.GONE -> return@launch
                OutboxTransition.SKIPPED -> {
                    toast("正在发送中，请先取消该上传", true)
                    return@launch
                }
                OutboxTransition.FAILED -> {
                    toast("无法更新待发送状态，稍后重试", true)
                    return@launch
                }
            }
            publishOutbox()
            if (entry.hubId == activeHubId) {
                drainOutbox(entry.hubId)
                scheduleOutboxReconnect()
                if (client == null) toast("已重新排队，连接「${hubDisplayName(entry.hubId)}」后发送")
            } else {
                toast("已重新排队，切换到「${hubDisplayName(entry.hubId)}」并连接后发送")
            }
        }
    }

    /** Explicit user removal: the only way queued content is discarded. */
    fun removeOutbox(id: String) {
        viewModelScope.launch {
            val entry = withContext(Dispatchers.IO) {
                runCatching { outbox.list().firstOrNull { it.id == id } }.getOrNull()
            } ?: return@launch
            // Never steal an upload already on the wire: its own 取消 is the way
            // to stop it. The check and the removal share the store's lock.
            when (withContext(Dispatchers.IO) { removeOutboxAtomically(id, allowSending = false) }) {
                OutboxTransition.APPLIED, OutboxTransition.GONE -> Unit
                OutboxTransition.SKIPPED -> toast("正在发送中，请先取消该上传", true)
                OutboxTransition.FAILED -> toast("无法移除待发送内容，稍后重试", true)
            }
            publishOutbox()
        }
    }

    /**
     * Switches the visible file-type filter. The Hub filters before paging, so
     * the old page and its cursor are dropped and the first page is re-fetched
     * for the new category.
     */
    fun selectCategory(category: String) {
        if (category !in ItemCategory.filters) return
        if (_state.value.category == category) return
        // Invalidate every in-flight request (snapshot, load more, single item)
        // so no response for the previous category can be written into this one.
        requestSeq += 1
        loadMoreSeq += 1
        pendingCreates.clear()
        snapshotting.set(false)
        _state.update {
            it.copy(
                category = category,
                items = emptyList(),
                nextCursor = "",
                loading = false,
                loadingMore = false,
                error = null,
            )
        }
        resync()
    }

    fun loadMore() {
        val hubId = activeHubId ?: return
        val api = client ?: return
        val snapshot = _state.value
        if (snapshot.loadingMore || snapshot.nextCursor.isEmpty()) return
        val token = requestSeq
        val loadToken = ++loadMoreSeq
        val wanted = snapshot.category
        _state.update { it.copy(loadingMore = true) }
        viewModelScope.launch {
            try {
                val page = withContext(Dispatchers.IO) {
                    api.list(snapshot.nextCursor, ApiClient.PAGE_SIZE, wanted)
                }
                if (requestSeq != token || !isLive(hubId, api)) return@launch
                _state.update { s ->
                    val seen = s.items.mapTo(HashSet()) { it.id }
                    s.copy(
                        items = s.items + page.items.filterNot { it.id in seen },
                        nextCursor = page.nextCursor,
                        loadingMore = false,
                    )
                }
            } catch (e: Exception) {
                if (requestSeq != token || !isLive(hubId, api)) return@launch
                if (e is ApiException && e.isUnauthorized) onUnauthorized(hubId, api)
                _state.update { it.copy(loadingMore = false) }
                toast(friendly(e), error = true)
            } finally {
                // A category switch bumps loadMoreSeq: its spinner must survive
                // the finally block of the abandoned page.
                if (loadMoreSeq == loadToken) {
                    _state.update { if (it.loadingMore) it.copy(loadingMore = false) else it }
                }
            }
        }
    }

    /** A realtime `item.created`: defer while a snapshot is in flight. */
    private fun onItemCreated(id: String) {
        if (snapshotting.get()) {
            pendingCreates.add(id)
            return
        }
        fetchAndInsert(id)
    }

    private fun fetchAndInsert(id: String) {
        val hubId = activeHubId ?: return
        val api = client ?: return
        viewModelScope.launch {
            try {
                val item = withContext(Dispatchers.IO) { api.getItem(id) }
                if (!isLive(hubId, api)) return@launch
                // The Hub decides the category and it may have changed while this
                // single-item fetch was open, so match against the live state.
                _state.update { s ->
                    when {
                        s.items.any { it.id == item.id } -> s
                        !ItemCategory.matches(item, s.category) -> s
                        else -> s.copy(items = listOf(item) + s.items)
                    }
                }
            } catch (_: Exception) {
                // A later resync repairs the list.
            }
        }
    }

    /**
     * Merges an item this device just created (composer, upload, share) into the
     * list, but only when it belongs to the visible category *and* to the
     * computer that is on screen. Nothing is ever classified on the client: the
     * category comes from the Hub's response.
     */
    private fun adoptCreatedItem(item: Item, hubId: String) {
        if (!isLive(hubId, client ?: return)) return
        val current = _state.value
        if (!ItemCategory.matches(item, current.category)) {
            toast(app.getString(R.string.category_sent_elsewhere, categoryLabel(current.category)))
            return
        }
        _state.update { s ->
            when {
                s.items.any { it.id == item.id } -> s
                !ItemCategory.matches(item, s.category) -> s
                else -> s.copy(items = listOf(item) + s.items)
            }
        }
    }

    private fun categoryLabel(category: String): String = app.getString(
        when (category) {
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
        },
    )

    fun sendText() {
        val value = composerText.value.trim()
        val snapshot = _state.value
        if (value.isEmpty() || snapshot.sendingText) return
        val hubId = activeHubId
        if (hubId == null) {
            // Nothing is paired/selected: there is no target to queue for.
            toast("请先选择一台电脑", true)
            return
        }
        // Capture the target and the sender name *before* the write, so a later
        // switch cannot re-target this text.
        val device = prefs.deviceName
        _state.update { it.copy(sendingText = true) }
        viewModelScope.launch {
            // Durable first: the composer is cleared only after the payload is on
            // disk, and a missing client no longer means "silently do nothing".
            val saved = try {
                withContext(Dispatchers.IO) { outbox.enqueueText(hubId, device, value) }
                true
            } catch (e: Exception) {
                toast("无法保存待发送内容：${friendly(e)}", true)
                false
            }
            _state.update { it.copy(sendingText = false) }
            if (!saved) return@launch
            if (composerText.value.trim() == value) composerText.value = ""
            publishOutbox()
            drainOutbox(hubId)
            if (client == null || activeHubId != hubId) {
                toast("已保存到「${hubDisplayName(hubId)}」的待发送，连接后自动发送")
            }
        }
    }

    /** Composer text lives outside UiState so typing does not recompose the list. */
    private val composerText = MutableStateFlow("")
    val composerInput: StateFlow<String> = composerText.asStateFlow()

    fun updateComposer(value: String) {
        composerText.value = value
    }

    fun deleteItem(id: String) {
        val hubId = activeHubId ?: return
        val api = client ?: return
        val token = requestSeq
        val snapshot = _state.value.items
        val index = snapshot.indexOfFirst { it.id == id }
        // A second tap on a row that is already gone must not send another
        // delete: the optimistic removal below is what makes it gone, so a
        // missing row means this id is already being deleted.
        if (index < 0) return
        _state.update { s -> s.copy(items = s.items.filterNot { it.id == id }) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { api.delete(id) }
                // Deleting several rows one after another is one action from the
                // user's point of view, so its notice replaces itself.
                toast("已删除", replaceLastSame = true)
            } catch (e: Exception) {
                // Only the category that owns this row may roll back, and never
                // the whole list: a delete that succeeded in parallel stays gone.
                if (requestSeq == token && isLive(hubId, api)) {
                    restoreDeletedItem(id, index, snapshot)
                }
                if (e is ApiException && e.isUnauthorized) {
                    onUnauthorized(hubId, api)
                } else {
                    toast(friendly(e), true)
                }
            }
        }
    }

    /**
     * Puts back the single row whose delete failed.
     *
     * [snapshot] is the list the row was removed from and [index] its position
     * there. The row is re-inserted after the neighbour that preceded it, so an
     * optimistic list keeps its order without resurrecting rows removed by other
     * deletes that did succeed.
     */
    private fun restoreDeletedItem(id: String, index: Int, snapshot: List<Item>) {
        val removed = snapshot.getOrNull(index)?.takeIf { it.id == id } ?: return
        _state.update { s ->
            if (s.items.any { it.id == id }) return@update s
            val previousId = snapshot.getOrNull(index - 1)?.id
            val afterPrevious = previousId
                ?.let { prev -> s.items.indexOfFirst { it.id == prev }.takeIf { at -> at >= 0 }?.plus(1) }
                ?: index.coerceIn(0, s.items.size)
            val items = s.items.toMutableList()
            items.add(afterPrevious.coerceIn(0, items.size), removed)
            s.copy(items = items)
        }
    }

    fun updateDeviceName(name: String) {
        val trimmed = name.take(64)
        prefs.deviceName = trimmed
        _state.update { it.copy(deviceName = trimmed) }
    }

    // ---------- uploads ----------

    fun uploadUris(uris: List<Uri>) {
        val hubId = activeHubId
        if (hubId == null) {
            toast("请先选择一台电脑", true)
            return
        }
        if (uris.isEmpty()) return
        viewModelScope.launch {
            val sources = mutableListOf<UploadSource>()
            var unreadable = 0
            for (uri in uris) {
                // A partial picker result must never be dropped silently: the
                // user is told how many files could not even be described.
                val source = runCatching { UriSources.fromUri(app.contentResolver, uri) }.getOrNull()
                if (source == null) unreadable++ else sources += source
            }
            if (unreadable > 0) {
                toast("$unreadable 个文件无法读取，未加入待发送", true)
            }
            enqueueSources(hubId, sources)
        }
    }

    fun uploadLocalFiles(files: List<File>) {
        val hubId = activeHubId ?: return
        val sources = files.map { UriSources.fromFile(it) }
        viewModelScope.launch { enqueueSources(hubId, sources) }
    }

    /**
     * Persists [sources] into [hubId]'s queue and only then tries the live
     * client.
     *
     * The target and the sender name are captured here, so switching computers
     * mid-upload changes nothing about where the bytes go. The size check uses
     * that Hub's saved limit (or the app default), and [OutboxStore] enforces the
     * *real* staged bytes while copying.
     */
    private suspend fun enqueueSources(hubId: String, sources: List<UploadSource>) {
        if (sources.isEmpty()) return
        val device = prefs.deviceName
        val limit = withContext(Dispatchers.IO) { hubUploadLimit(hubId) }
        var queued = 0
        for (source in sources) {
            try {
                withContext(Dispatchers.IO) { outbox.enqueueFile(hubId, device, source, limit) }
                queued++
            } catch (e: OutboxAttachmentTooLargeException) {
                toast(app.getString(R.string.error_upload_too_large) + "（${source.name}）", true)
            } catch (e: Exception) {
                toast("「${source.name}」无法保存到待发送：${friendly(e)}", true)
            }
        }
        if (queued == 0) return
        publishOutbox()
        drainOutbox(hubId)
        if (client == null || activeHubId != hubId) {
            toast("已保存到「${hubDisplayName(hubId)}」的待发送，连接后自动发送")
        }
    }

    fun cancelUpload(key: String) {
        uploadJobs.remove(key)?.job?.cancel()
        // Queue uploads are kept out of [uploadJobs] (a 401 teardown must not
        // cancel them), but the user's explicit 取消 still stops one.
        outboxUploadJobs.remove(key)?.job?.cancel()
        uploadTasks.remove(key)
        publishTransfers()
    }

    fun dismissUpload(key: String) {
        uploadTasks.remove(key)
        publishTransfers()
    }

    // ---------- downloads ----------

    /** Streams the attachment to Downloads (or app storage on old Android). */
    fun saveItem(item: Item) {
        val hubId = activeHubId ?: return
        val api = client ?: return
        val blob = item.blob ?: return
        val key = "save:$hubId:${item.id}"
        // Both actions of one item must not transfer at the same time: they share
        // one staged package, and the UI's disabled buttons are not the only way
        // to call these methods. Waiting is unnecessary because either action
        // reuses the other's completed package.
        if (downloadJobs.containsKey(key) || downloadJobs.containsKey("apk:$hubId:${item.id}")) return
        // A finished save is terminal for this item on this computer: the row is
        // already in Downloads and the button says so. A failed or cancelled one
        // left no row behind, so those stay retryable.
        if (downloadTasks.of(hubId).any { it.key == key && it.status == TaskStatus.DONE }) return
        val task = DownloadTask(
            key = key,
            itemId = item.id,
            name = blob.name,
            total = blob.sizeBytes,
            hubId = hubId,
            kind = DownloadKind.SAVE,
        )
        downloadTasks.put(task)
        publishTransfers()
        // Started only after its slot is registered: the main dispatcher may run
        // the body up to its first suspension inside this call, and a body that
        // fails immediately (destination creation) would otherwise reach its
        // finally before the registration below and leave a dead job behind.
        val job = viewModelScope.launch(start = CoroutineStart.LAZY) {
            var target: DownloadTarget? = null
            try {
                // Creating the destination is part of the transfer: if it fails,
                // the task fails instead of reporting a file that never existed.
                val created = DownloadTarget.create(app, blob.name, blob.contentType)
                target = created
                val downloaded = if (item.isApk) {
                    // An APK also goes through the shared package cache first, so
                    // installing it afterwards reuses these bytes instead of
                    // downloading the same file a second time.
                    val cached = stagedApk(hubId, item.id, blob, api, key)
                    withContext(Dispatchers.IO) {
                        cached.inputStream().buffered().use { input ->
                            created.openSink().use { sink -> input.copyTo(sink) }
                        }
                        cached.length()
                    }
                } else {
                    withContext(Dispatchers.IO) {
                        created.openSink().use { sink ->
                            api.download(item.id, sink, progressReporter(key))
                        }.bytes
                    }
                }
                // Closing the stream only flushed the bytes; MediaStore still has
                // to accept the row before the file is visible in Downloads.
                val published = (created as? DownloadTarget.MediaStoreTarget)?.finish(true) ?: true
                if (!published) throw IOException("cannot publish Downloads entry")
                updateDownload(key) { it.copy(status = TaskStatus.DONE, loaded = downloaded, total = downloaded) }
                toast(app.getString(R.string.saved_to_downloads, created.location))
            } catch (e: CancellationException) {
                discardTarget(target)
                downloadTasks.remove(key)
                publishTransfers()
                throw e
            } catch (e: Exception) {
                discardTarget(target)
                if (e is ApiException && e.isUnauthorized) onUnauthorized(hubId, api)
                updateDownload(key) { it.copy(status = TaskStatus.FAILED, error = friendly(e)) }
                toast("${app.getString(R.string.error_save_failed)}：${friendly(e)}", true)
            } finally {
                downloadJobs.remove(key)
            }
        }
        downloadJobs[key] = HubJob(hubId, job)
        job.start()
    }

    /**
     * Downloads an APK into the shared package cache and hands it to the system
     * installer. The app never installs silently; Android shows its own
     * confirmation.
     */
    fun installApk(item: Item) {
        val hubId = activeHubId ?: return
        val api = client ?: return
        val blob = item.blob ?: return
        val key = "apk:$hubId:${item.id}"
        // Same mutual exclusion as a save: a second request for either action of
        // this item must not start another GET over the shared package.
        if (downloadJobs.containsKey(key) || downloadJobs.containsKey("save:$hubId:${item.id}")) return
        val task = DownloadTask(
            key = key,
            itemId = item.id,
            name = blob.name,
            total = blob.sizeBytes,
            hubId = hubId,
            kind = DownloadKind.INSTALL,
        )
        downloadTasks.put(task)
        publishTransfers()
        // Same ordering as a save: register the job before it can run, so an
        // immediately failing body cannot clear its own slot too early.
        val job = viewModelScope.launch(start = CoroutineStart.LAZY) {
            try {
                val file = stagedApk(hubId, item.id, blob, api, key)
                // DONE means the package is on disk and can be handed over again;
                // the installer prompt is not part of the download, so a cancelled
                // or failed prompt keeps this state instead of deleting the APK.
                updateDownload(key) {
                    it.copy(status = TaskStatus.DONE, loaded = file.length(), total = file.length())
                }
                handOverApk(file)
            } catch (e: CancellationException) {
                downloadTasks.remove(key)
                publishTransfers()
                throw e
            } catch (e: Exception) {
                if (e is ApiException && e.isUnauthorized) onUnauthorized(hubId, api)
                updateDownload(key) { it.copy(status = TaskStatus.FAILED, error = friendly(e)) }
                toast("${app.getString(R.string.error_download_failed)}：${friendly(e)}", true)
            } finally {
                downloadJobs.remove(key)
            }
        }
        downloadJobs[key] = HubJob(hubId, job)
        job.start()
    }

    /**
     * Resolves a complete local APK for [itemId], reusing the shared cache when
     * it already holds one and otherwise streaming a fresh copy into a staging
     * file that only becomes visible after a size check.
     *
     * The Hub's own SHA-256 is verified by [ApiClient.download]; the length check
     * here catches a Hub that sent no checksum header. A cancelled or failed
     * transfer deletes its staging file, so nothing partial is ever reused.
     */
    private suspend fun stagedApk(
        hubId: String,
        itemId: String,
        blob: Blob,
        api: ApiClient,
        key: String,
    ): File = withContext(Dispatchers.IO) {
        // Cache probing, directory creation, publishing and cleanup are all
        // filesystem work: they belong on IO together with the transfer itself.
        apkCache.complete(hubId, itemId, blob.sha256, blob.sizeBytes)?.let { cached ->
            // A reused package was not downloaded in this attempt; show its real
            // size so the row does not look empty while it is handed over.
            updateDownload(key) { it.copy(loaded = cached.length(), total = cached.length()) }
            return@withContext cached
        }
        val part = apkCache.start(hubId, itemId, blob.sha256, blob.sizeBytes)
        val onProgress = progressReporter(key)
        try {
            val result = part.file.outputStream().buffered().use { sink ->
                api.download(itemId, sink, onProgress)
            }
            // Keep the final totals even when the transfer ends between two
            // throttled samples: leaving the last sample's bytes would show a
            // file smaller than it is.
            updateDownload(key) { it.copy(loaded = result.bytes, total = result.bytes) }
            part.publish()
        } catch (e: Throwable) {
            part.discard()
            throw e
        }
    }

    /**
     * One progress callback per transfer: [TransferRateMeter] throttles the
     * speed to one value per window on a monotonic clock, and the counts are
     * always applied so a closed window never drops bytes.
     */
    private fun progressReporter(key: String): (Long, Long?) -> Unit {
        val meter = TransferRateMeter()
        return { loaded, total ->
            val rate = meter.sample(loaded)
            updateDownload(key) {
                it.copy(
                    loaded = loaded,
                    total = total ?: it.total,
                    bytesPerSecond = rate ?: it.bytesPerSecond,
                )
            }
        }
    }

    /**
     * Hands a complete local package to the system installer.
     *
     * Deliberately never throws: a missing installer or a revoked "install
     * unknown apps" grant is not a download failure, so the task stays DONE and
     * the user can retry without downloading the APK again.
     */
    private fun handOverApk(file: File) {
        if (!SystemActions.canInstallPackages(app)) {
            toast(app.getString(R.string.error_apk_install_permission), true)
            SystemActions.openInstallPermissionSettings(app)
            return
        }
        val uri = runCatching { ApkInstaller.contentUriFor(app, file) }.getOrNull()
        val started = uri != null && SystemActions.installApk(app, uri)
        if (!started) toast(app.getString(R.string.error_apk_install), true)
    }

    fun cancelDownload(key: String) {
        // The job keeps its slot registered until its own finally releases it: a
        // retry started here could publish a task that the cancelled transfer's
        // cleanup would then delete. Only the task row drops immediately, so the
        // user still sees the cancel at once.
        downloadJobs[key]?.job?.cancel()
        downloadTasks.remove(key)
        publishTransfers()
    }

    /**
     * Releases a download destination that never produced a finished file.
     *
     * A MediaStore row is deleted so its pending entry does not linger as a
     * broken zero-byte file, and an app-storage export is removed the same way.
     */
    private fun discardTarget(target: DownloadTarget?) {
        when (target) {
            is DownloadTarget.MediaStoreTarget -> target.finish(false)
            is DownloadTarget.AppFileTarget -> target.file()?.delete()
            null -> Unit
        }
    }

    fun dismissDownload(key: String) {
        downloadTasks.remove(key)
        publishTransfers()
    }

    // ---------- system share ----------

    /**
     * Records a system share and asks for its target.
     *
     * Nothing is sent until the user confirms a specific computer, so an app
     * that shares to FlowDrop never wakes up one Hub by accident. URIs are
     * copied into the app cache right away: the read grant belongs to the
     * launching Intent and may be gone by the time the target is chosen, which
     * is exactly why the copy is made before the dialog is answered.
     */
    fun submitShare(payload: SharePayload) {
        if (payload.isEmpty()) return
        // A new unconfirmed share replaces the previous one: its staged copies and
        // its pending continuation are released instead of lingering.
        _state.value.shareRequestId?.let { previous ->
            shareStagingJobs.remove(previous)?.cancel()
            shareReady.remove(previous)?.complete(Unit)
            shareRequests.remove(previous)?.discard()
        }
        val id = ++shareSeq
        val request = ShareRequest(id = id, texts = payload.texts)
        shareRequests[id] = request
        _state.update { it.copy(shareRequestId = id) }
        if (payload.uris.isEmpty()) return
        // Staging is tracked per request so a confirmation that arrives before
        // the copies are finished waits for them instead of sending a share with
        // its attachments missing.
        val ready = CompletableDeferred<Unit>()
        shareReady[id] = ready
        val job = viewModelScope.launch {
            try {
                for (uri in payload.uris) {
                    // Staged without the *current* computer's limit: the target
                    // is not known yet and may accept much larger files. The
                    // target's own limit is applied when it uploads them.
                    val result = withContext(Dispatchers.IO) { stageSharedUri(uri) } ?: continue
                    // A dismissed request refuses the copy and deletes it here.
                    if (!request.add(result.first, result.second)) break
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: Exception) {
                toast("无法读取分享内容：${friendly(e)}", true)
            } finally {
                ready.complete(Unit)
                shareReady.remove(id)
                shareStagingJobs.remove(id)
            }
        }
        shareStagingJobs[id] = job
    }

    /**
     * Queues the pending share [requestId] for [hubId] and to nothing else, then
     * opens that computer if it is not the live one.
     *
     * Both the request id and the target are checked: a stale dialog (a newer
     * share replaced it, or it was dismissed) does nothing, and an unpaired
     * target is refused with a message instead of being redirected. Every part
     * of the share is durable *before* any connection is opened, so a failed or
     * abandoned switch can neither lose it nor send it elsewhere.
     */
    fun confirmShare(requestId: Long, hubId: String) {
        if (_state.value.shareRequestId != requestId) return
        if (shareRequests[requestId] == null) return
        if (vault.load(hubId) == null) {
            toast("该电脑已不在已配对列表中，分享未发送", true)
            return
        }
        _state.update { it.copy(shareRequestId = null) }
        viewModelScope.launch {
            // Wait for the cache copies of the shared URIs (if any) first: an
            // immediate confirmation must still send the attachments.
            shareReady.remove(requestId)?.await()
            shareStagingJobs.remove(requestId)
            val request = shareRequests.remove(requestId) ?: return@launch
            // Durable before selection: every part is in that one computer's
            // queue before a connection is opened or a byte is sent.
            val queued = try {
                queueShare(request, hubId)
            } catch (e: Exception) {
                // Keep the staged copies and the pending request: the user gets
                // the dialog back and one more chance instead of losing content.
                shareRequests[requestId] = request
                _state.update { it.copy(shareRequestId = requestId) }
                toast("无法保存分享内容：${friendly(e)}", true)
                return@launch
            }
            if (queued == 0) {
                request.discard()
                return@launch
            }
            // The copies are now in app-private queue storage; the cache staging
            // can go.
            request.discard()
            publishOutbox()
            val api = client
            if (hubId == activeHubId && api != null && _state.value.phase == Phase.INBOX) {
                drainOutbox(hubId)
                return@launch
            }
            // Still open the chosen computer as before; the queued items are
            // delivered by the drain that follows its bind.
            val gen = beginIntent(hubId)
            _state.update { it.copy(loggingIn = true, loginError = null) }
            handleConnectResult(gen, hubId, connectorFor(hubId).connect(ConnectTrigger.MANUAL))
        }
    }

    /** The user closed the share dialog: nothing is sent and staging is freed. */
    fun dismissShare(requestId: Long) {
        if (_state.value.shareRequestId != requestId) return
        _state.update { it.copy(shareRequestId = null) }
        shareStagingJobs.remove(requestId)?.cancel()
        shareReady.remove(requestId)?.complete(Unit)
        shareRequests.remove(requestId)?.discard()
    }

    /**
     * Persists every part of a confirmed share into [hubId]'s queue.
     *
     * Returns how many items were queued. On failure the already queued parts
     * are rolled back, so the caller can keep the intact staged copies and let
     * the user retry instead of duplicating a half-saved share.
     */
    private suspend fun queueShare(request: ShareRequest, hubId: String): Int = withContext(Dispatchers.IO) {
        val device = prefs.deviceName
        val limit = hubUploadLimit(hubId)
        // The whole batch is published (or rolled back) under the store's own
        // lock, so a drain can never observe and send a half-saved share and then
        // see it disappear on rollback (which would duplicate it on retry).
        synchronized(outbox) {
            val created = mutableListOf<String>()
            try {
                for (source in request.sources()) {
                    created += outbox.enqueueFile(hubId, device, source, limit).id
                }
                request.texts.firstOrNull()?.trim()?.takeIf { it.isNotEmpty() }?.let { text ->
                    created += outbox.enqueueText(hubId, device, text).id
                }
                created.size
            } catch (e: Exception) {
                created.forEach { runCatching { outbox.remove(it) } }
                throw e
            }
        }
    }

    /**
     * Streams a granted content URI into the app cache and returns an
     * [UploadSource] backed by that file. Returns null when the size exceeds
     * the Hub limit (the user is told) or the URI cannot be opened.
     */
    private fun stageSharedUri(uri: Uri): Pair<UploadSource, File>? {
        val dir = File(app.cacheDir, "share-in").apply { mkdirs() }
        val source = UriSources.fromUri(app.contentResolver, uri)
        // A unique name per copy: the old millisecond+name scheme could hand two
        // shares of the same file the same path and delete each other's copy.
        val target = File(dir, UUID.randomUUID().toString())
        try {
            source.open().use { input ->
                target.outputStream().use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        val n = input.read(buffer)
                        if (n == -1) break
                        output.write(buffer, 0, n)
                    }
                }
            }
        } catch (e: Exception) {
            target.delete()
            throw e
        }
        // Keep the friendly original name *and* MIME type for the multipart part,
        // but read from the staged copy so the grant can expire safely.
        return UriSources.fromFile(target).copy(name = source.name, contentType = source.contentType) to target
    }

    // ---------- toasts ----------

    /**
     * Shows a transient notice.
     *
     * [replaceLastSame] makes a repeated identical outcome (deleting several rows
     * in a row) take the place of every notice it repeats, wherever those sit in
     * the visible stack; notices with other text or another severity keep their
     * own slot.
     */
    fun toast(message: String, error: Boolean = false, replaceLastSame: Boolean = false) {
        _state.update { s ->
            val repeated = replaceLastSame && s.toasts.any { it.text == message && it.error == error }
            val kept = if (repeated) {
                s.toasts.filterNot { it.text == message && it.error == error }
            } else {
                s.toasts
            }
            s.copy(toasts = (kept + Toast(System.nanoTime(), message, error)).takeLast(4))
        }
    }

    fun dismissToast(id: Long) {
        _state.update { s -> s.copy(toasts = s.toasts.filterNot { it.id == id }) }
    }

    private fun friendly(e: Throwable): String = when (e) {
        is ApiException -> when {
            e.isUnauthorized -> app.getString(R.string.error_unauthorized)
            e.isNetwork -> app.getString(R.string.error_network)
            e.isTooLarge -> app.getString(R.string.error_upload_too_large)
            e.code == "rate_limited" -> "请求过于频繁，请稍后再试"
            e.code == "disk_full" -> "Hub 磁盘空间不足"
            e.code == "invalid_link" -> "链接必须是 http(s) 绝对地址"
            else -> e.message
        }
        else -> e.message ?: app.getString(R.string.error_network)
    }

    override fun onCleared() {
        super.onCleared()
        eventStream?.stop()
        outboxReconnectJob?.cancel()
        connectors.values.forEach { it.cancel() }
        connectors.clear()
        discovery.release()
        images.clear()
        // Never leave a staged copy of a shared file behind.
        shareRequests.values.forEach { it.discard() }
        shareRequests.clear()
    }

    // ---------- transfer isolation ----------

    /** Publishes only the active computer's transfers to [UiState]. */
    private fun publishTransfers() {
        val hubId = activeHubId
        val uploads = uploadTasks.of(hubId)
        val downloads = downloadTasks.of(hubId)
        _state.update { it.copy(uploads = uploads, downloads = downloads) }
    }

    private fun updateUpload(key: String, transform: (UploadTask) -> UploadTask) {
        val next = uploadTasks.update(key, transform) ?: return
        if (next.hubId == activeHubId) {
            _state.update { s ->
                s.copy(uploads = s.uploads.map { if (it.key == key) next else it })
            }
        }
    }

    private fun updateDownload(key: String, transform: (DownloadTask) -> DownloadTask) {
        val next = downloadTasks.update(key, transform) ?: return
        if (next.hubId == activeHubId) {
            _state.update { s ->
                s.copy(downloads = s.downloads.map { if (it.key == key) next else it })
            }
        }
    }

    companion object {
        private const val PAIRING_CODE_LENGTH = 4

        /** Matches no real Hub: an unpaired attempt's 401 affects no inbox. */
        private const val NO_HUB = "\u0000unpaired"

        /** `restorePairedSession` maps `authenticated:false` to this code. */
        private const val CODE_PAIRING_REJECTED = "pairing_rejected"

        private val LINK_REGEX = Regex("^https?://\\S+$", RegexOption.IGNORE_CASE)

        /** First wait of the queue's reconnect backoff (then doubled, capped). */
        private const val OUTBOX_RETRY_INITIAL_MS = 3_000L

        /** Upper bound of that backoff: recover soon after a Hub comes back. */
        private const val OUTBOX_RETRY_MAX_MS = 30_000L
    }
}

/** Text and content URIs extracted from an incoming ACTION_SEND intent. */
data class SharePayload(
    val texts: List<String> = emptyList(),
    val uris: List<Uri> = emptyList(),
) {
    fun isEmpty(): Boolean = texts.isEmpty() && uris.isEmpty()
}

/** A coroutine plus the computer it was started for. */
private class HubJob(val hubId: String, val job: Job)

/** Outcome of a conditional queue mutation (see AppViewModel.transitionOutbox). */
private enum class OutboxTransition { APPLIED, SKIPPED, GONE, FAILED }

/**
 * Running transfers keyed by id, each tagged with the computer it belongs to.
 *
 * Progress callbacks arrive from IO threads and ViewModel bookkeeping runs on
 * Main, so access is synchronised and [of] always returns a snapshot.
 */
private class TransferBook<T>(
    private val idOf: (T) -> String,
    private val hubOf: (T) -> String,
) {
    // Insertion-ordered so the progress rows keep the order the transfers started in.
    private val entries = LinkedHashMap<String, T>()

    @Synchronized
    fun put(item: T) {
        entries[idOf(item)] = item
    }

    @Synchronized
    fun remove(id: String): T? = entries.remove(id)

    @Synchronized
    fun update(id: String, transform: (T) -> T): T? {
        val current = entries[id] ?: return null
        val next = transform(current)
        entries[id] = next
        return next
    }

    @Synchronized
    fun of(hubId: String?): List<T> =
        entries.values.filter { hubOf(it) == hubId }
}

/**
 * One system share waiting for its target to be confirmed.
 *
 * The cache copies made from the intent's grant arrive asynchronously, so the
 * lists are filled after construction and guarded by the object's lock. A
 * dismissed request refuses further copies ([add] returns false) and deletes the
 * ones it already has: a staged file is never left behind and never sent after
 * the user said no.
 */
private class ShareRequest(
    val id: Long,
    val texts: List<String>,
) {
    private var discarded = false
    private val staged = mutableListOf<File>()
    private val uploadSources = mutableListOf<UploadSource>()

    @Synchronized
    fun add(source: UploadSource, file: File): Boolean {
        if (discarded) {
            runCatching { file.delete() }
            return false
        }
        uploadSources += source
        staged += file
        return true
    }

    @Synchronized
    fun sources(): List<UploadSource> = uploadSources.toList()

    @Synchronized
    fun discard() {
        discarded = true
        staged.forEach { runCatching { it.delete() } }
        staged.clear()
        uploadSources.clear()
    }
}

/** Display record for one stored pairing. */
private fun dev.flowdrop.android.data.StoredSession.toSavedHub(): SavedHub? {
    if (hubId.isBlank()) return null
    return SavedHub(
        id = hubId,
        name = hubName.takeIf { it.isNotBlank() } ?: "电脑",
        address = baseUrl.ifBlank { origin },
    )
}
