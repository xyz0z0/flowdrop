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
import dev.flowdrop.android.data.ConnState
import dev.flowdrop.android.data.DEFAULT_MAX_UPLOAD_BYTES
import dev.flowdrop.android.data.EventStream
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.HubProbe
import dev.flowdrop.android.data.HubRouteSelector
import dev.flowdrop.android.data.Item
import dev.flowdrop.android.data.RouteCandidate
import dev.flowdrop.android.data.UploadSource
import dev.flowdrop.android.discovery.DiscoveredHub
import dev.flowdrop.android.discovery.DiscoveryController
import dev.flowdrop.android.discovery.DiscoveryState
import dev.flowdrop.android.io.ApkInstaller
import dev.flowdrop.android.io.DownloadTarget
import dev.flowdrop.android.io.UriSources
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

enum class Phase { BOOTING, LOGIN, INBOX }

enum class TaskStatus { RUNNING, DONE, FAILED }

data class UploadTask(
    val key: String,
    val name: String,
    val size: Long,
    val loaded: Long = 0,
    val status: TaskStatus = TaskStatus.RUNNING,
    val error: String? = null,
) {
    val fraction: Float get() = if (size > 0) (loaded.toFloat() / size).coerceIn(0f, 1f) else 0f
}

data class DownloadTask(
    val key: String,
    val itemId: String,
    val name: String,
    val loaded: Long = 0,
    val total: Long? = null,
    val status: TaskStatus = TaskStatus.RUNNING,
    val error: String? = null,
) {
    val fraction: Float
        get() = total?.takeIf { it > 0 }?.let { (loaded.toFloat() / it).coerceIn(0f, 1f) } ?: 0f
}

data class Toast(val id: Long, val text: String, val error: Boolean = false)

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
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    val error: String? = null,
    val conn: ConnState = ConnState.CLOSED,
    val uploads: List<UploadTask> = emptyList(),
    val downloads: List<DownloadTask> = emptyList(),
    val toasts: List<Toast> = emptyList(),
    val maxUploadBytes: Long = DEFAULT_MAX_UPLOAD_BYTES,
    val sendingText: Boolean = false,
    val discovery: DiscoveryState = DiscoveryState(),
)

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
 *   guarantee.
 */
class AppViewModel(
    private val app: FlowDropApp,
    private val container: AppContainer,
) : ViewModel() {

    private val prefs = container.preferences
    private val sessionStore = container.sessionStore

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

    // Registered/removed by each transfer's own coroutine and cleared from Main,
    // so they must be thread-safe and are never iterated live (see cancelTransfers).
    private val uploadJobs = ConcurrentHashMap<String, Job>()
    private val downloadJobs = ConcurrentHashMap<String, Job>()
    private var pendingShare: SharePayload? = null
    private val images = dev.flowdrop.android.ui.AuthenticatedImageStore { client }

    val imageStore: dev.flowdrop.android.ui.AuthenticatedImageStore get() = images

    /** False while the Activity is backgrounded: no probing, no reselection. */
    private var foreground = true

    private val routeSelector = HubRouteSelector(probe = { candidate -> HubProbe.hubId(candidate) })

    /**
     * Single-flight reconnect state machine. The actual probe/bind work is in
     * [pickPairedCandidate] and [bindPaired]; keeping the loop here small and
     * pure (except for those two lambdas) is what makes it unit testable.
     */
    private val connector = PairedConnector(
        scope = viewModelScope,
        pairing = {
            val stored = sessionStore.load()
            if (stored == null || stored.hubId.isBlank()) {
                null
            } else {
                SavedPairing(
                    hubId = stored.hubId,
                    candidates = stored.candidates.map { it.toRouteCandidate() },
                )
            }
        },
        pick = { saved -> pickPairedCandidate(saved) },
        bind = { candidate -> bindPaired(candidate) },
        isForeground = { foreground },
    )

    init {
        restoreSession()
    }

    // ---------- session lifecycle ----------

    /**
     * Startup. A paired device reconnects silently through its saved LAN and
     * Tailscale addresses; an unpaired one gets discovery plus the pairing form.
     */
    private fun restoreSession() {
        val stored = sessionStore.load()
        val paired = stored != null && stored.hubId.isNotBlank()
        val address = stored?.baseUrl?.takeIf { it.isNotBlank() } ?: prefs.hubAddress
        _state.update {
            it.copy(
                hubAddress = address,
                hubName = prefs.hubName,
                hasStoredPairing = paired,
                phase = if (paired) Phase.BOOTING else Phase.LOGIN,
            )
        }
        if (!paired) {
            deliverPendingShare()
            return
        }
        viewModelScope.launch { handleConnectResult(connector.connect(ConnectTrigger.STARTUP)) }
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
     * Pairs this device with the Hub at [UiState.hubAddress] using the code
     * shown on the Hub's settings screen.
     *
     * The code is cleared the moment it is submitted and is never stored; the
     * device token returned once by the Hub is persisted by [ApiClient.pair]
     * into the Keystore-backed session record.
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
        viewModelScope.launch {
            val api = container.activate(endpoint) { onUnauthorized() }
            try {
                val info = withContext(Dispatchers.IO) { api.pair(code, prefs.deviceName) }
                client = api
                val hubName = info.hub?.name.orEmpty()
                prefs.hubAddress = endpoint.origin
                prefs.hubName = hubName
                _state.update {
                    it.copy(
                        hubAddress = endpoint.origin,
                        hubName = hubName,
                        hasStoredPairing = true,
                        maxUploadBytes = api.maxUploadBytes,
                    )
                }
                enterInbox()
            } catch (cancelled: CancellationException) {
                // ViewModel cleared: never report it as a pairing/network error.
                container.dropActiveClient()
                throw cancelled
            } catch (e: ApiException) {
                container.dropActiveClient()
                _state.update { it.copy(loggingIn = false, loginError = pairErrorMessage(e)) }
            } catch (e: Exception) {
                container.dropActiveClient()
                _state.update {
                    it.copy(loggingIn = false, loginError = app.getString(R.string.error_network))
                }
            }
        }
    }

    /** User-initiated reconnect of the already paired Hub; no code required. */
    fun retryConnection() {
        if (_state.value.loggingIn) return
        _state.update { it.copy(loggingIn = true, loginError = null) }
        viewModelScope.launch { handleConnectResult(connector.connect(ConnectTrigger.MANUAL)) }
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
    private suspend fun pickPairedCandidate(saved: SavedPairing): RouteCandidate? =
        try {
            routeSelector.select(saved.hubId, saved.candidates)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        }

    /** IO half of a bind; nothing in here publishes state to the UI. */
    private sealed interface BindOutcome {
        class Ready(val api: ApiClient) : BindOutcome
        class Failed(val result: ConnectResult) : BindOutcome
    }

    /**
     * Binds one verified candidate: move the stored record to that origin while
     * keeping the pairing ([SessionStore.switchToPaired] — never
     * `deactivate`/`discardLocalSession`, which would drop the pairing), build a
     * client for it, then rebuild cookies/CSRF from the stored device token.
     *
     * Only the request work runs on [Dispatchers.IO]; every ViewModel side
     * effect (transfer maps, `client`, event stream, `_state`) is applied on the
     * caller's dispatcher, i.e. Main, so it cannot race [onBackground].
     * Nothing is published unless the restore succeeded, so a failed round
     * leaves the previous client and the pairing untouched.
     */
    private suspend fun bindPaired(candidate: RouteCandidate): ConnectResult {
        val stored = sessionStore.load() ?: return ConnectResult.NotPaired
        val expectedHubId = stored.hubId
        if (expectedHubId.isBlank()) return ConnectResult.NotPaired
        val endpoint = HubEndpoint.parse(candidate.url) ?: return ConnectResult.NoRoute
        if (currentOrigin() != null && currentOrigin() != endpoint.origin) {
            // A transfer started against the old origin must never land in the
            // new client's list: cancel it and ask the user to retry explicitly.
            cancelTransfers(TRANSFER_INTERRUPTED)
        }

        val outcome = withContext(Dispatchers.IO) {
            if (!sessionStore.switchToPaired(endpoint, expectedHubId)) {
                return@withContext BindOutcome.Failed(ConnectResult.NoRoute)
            }
            val api = try {
                container.activatePaired(endpoint, expectedHubId) { onUnauthorized() }
            } catch (_: IllegalStateException) {
                return@withContext BindOutcome.Failed(ConnectResult.NoRoute)
            }
            try {
                api.restorePairedSession(expectedHubId)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: ApiException) {
                container.dropActiveClient()
                return@withContext if (e.code == CODE_PAIRING_REJECTED) {
                    BindOutcome.Failed(ConnectResult.PairingRejected)
                } else {
                    // Temporary failure: pairing and previous client stay.
                    BindOutcome.Failed(ConnectResult.NoRoute)
                }
            } catch (_: Exception) {
                container.dropActiveClient()
                return@withContext BindOutcome.Failed(ConnectResult.NoRoute)
            }
            BindOutcome.Ready(api)
        }

        return when (outcome) {
            is BindOutcome.Failed -> outcome.result
            is BindOutcome.Ready -> {
                switchClient(outcome.api)
                ConnectResult.Bound(candidate)
            }
        }
    }

    private fun switchClient(api: ApiClient) {
        val originChanged = client?.endpoint?.origin != api.endpoint.origin
        eventStream?.stop()
        eventStream = null
        if (originChanged) {
            // Cached previews were fetched with the previous origin's cookies;
            // drop them rather than mixing origins. The pairing is kept.
            images.clear()
        }
        client = api
        _state.update { it.copy(maxUploadBytes = api.maxUploadBytes, conn = ConnState.CLOSED) }
    }

    private suspend fun handleConnectResult(result: ConnectResult) {
        when (result) {
            is ConnectResult.Bound -> {
                _state.update {
                    it.copy(
                        hubAddress = result.candidate.url,
                        hubName = prefs.hubName.ifEmpty { it.hubName },
                        hasStoredPairing = true,
                        loginError = null,
                    )
                }
                enterInbox()
            }
            ConnectResult.PairingRejected -> onPairingRevoked()
            ConnectResult.NotPaired -> _state.update {
                it.copy(
                    phase = Phase.LOGIN,
                    hasStoredPairing = false,
                    loggingIn = false,
                    pairingCode = "",
                )
            }
            ConnectResult.NoRoute -> onNoRoute()
            ConnectResult.Deferred -> onDeferred()
        }
    }

    /**
     * The round was abandoned (backgrounded) or joined another round that was
     * abandoned. Never leave the UI spinning or stuck on the boot screen: the
     * pairing is kept and a foreground event starts a fresh round.
     */
    private fun onDeferred() {
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
     */
    private fun onNoRoute() {
        val message = app.getString(R.string.error_network)
        _state.update {
            if (it.phase == Phase.INBOX) {
                // The existing link stays authoritative: a failed reselection
                // must not overwrite an inbox that still works. A genuinely
                // dead socket is reported by the stream itself.
                it.copy(loggingIn = false)
            } else {
                it.copy(
                    phase = Phase.LOGIN,
                    hasStoredPairing = true,
                    loggingIn = false,
                    conn = ConnState.CLOSED,
                    loginError = message,
                )
            }
        }
    }

    /**
     * The Hub answered `authenticated:false` (HTTP 200) for our stored device
     * token: the device was revoked. Only this case clears the old credentials.
     */
    private fun onPairingRevoked() {
        teardownClient()
        sessionStore.clear()
        prefs.hubName = ""
        _state.update {
            it.copy(
                phase = Phase.LOGIN,
                hasStoredPairing = false,
                hubName = "",
                pairingCode = "",
                loggingIn = false,
                items = emptyList(),
                nextCursor = "",
                conn = ConnState.CLOSED,
                loginError = "此设备已在 Hub 上解除配对，请重新输入配对码",
            )
        }
    }

    // ---------- nearby Hub discovery (address hint only) ----------

    /** One automatic short search when the foreground pairing screen appears. */
    fun onLoginScreenShown() {
        val snapshot = _state.value
        if (snapshot.phase != Phase.LOGIN || snapshot.hasStoredPairing) return
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

    fun logout() {
        val api = client
        viewModelScope.launch {
            eventStream?.stop()
            eventStream = null
            if (api != null) {
                // Revokes this device on the Hub, then forgets it locally.
                runCatching { withContext(Dispatchers.IO) { api.logout() } }
            }
            logoutLocalOnly()
            sessionStore.clear()
            prefs.hubName = ""
            _state.update {
                it.copy(
                    phase = Phase.LOGIN,
                    hasStoredPairing = false,
                    hubName = "",
                    pairingCode = "",
                    loggingIn = false,
                    items = emptyList(),
                    nextCursor = "",
                    uploads = emptyList(),
                    downloads = emptyList(),
                    error = null,
                    conn = ConnState.CLOSED,
                )
            }
        }
    }

    /** Full local teardown, including the stored pairing (logout / revocation). */
    private fun logoutLocalOnly() {
        eventStream?.stop()
        eventStream = null
        cancelTransfers()
        container.deactivate()
        client = null
        images.clear()
    }

    /**
     * Drops the live client without touching the stored pairing: a 401 only
     * means the cookie session is gone, and the device token may still be able
     * to restore it (a revoked device is detected by the restore call).
     */
    private fun teardownClient() {
        eventStream?.stop()
        eventStream = null
        cancelTransfers()
        container.dropActiveClient()
        client = null
        images.clear()
    }

    /**
     * Cancels every running transfer and tells the user. Used when the live
     * origin changes so results from the old client cannot be mixed into the
     * new one; writes are never replayed automatically.
     */
    private fun cancelTransfers(notice: String? = null) {
        // Snapshot then clear: each transfer's own coroutine removes its entry
        // from the other thread, so the maps are never iterated live.
        val pendingUploads = uploadJobs.values.toList()
        uploadJobs.clear()
        val pendingDownloads = downloadJobs.values.toList()
        downloadJobs.clear()
        pendingUploads.forEach { it.cancel() }
        pendingDownloads.forEach { it.cancel() }
        if (pendingUploads.isNotEmpty() || pendingDownloads.isNotEmpty()) {
            _state.update { it.copy(uploads = emptyList(), downloads = emptyList()) }
            notice?.let { toast(it, error = true) }
        }
    }

    private fun currentOrigin(): String? = client?.endpoint?.origin

    private fun activate(endpoint: HubEndpoint): ApiClient {
        val api = container.activate(endpoint) { onUnauthorized() }
        client = api
        _state.update { it.copy(maxUploadBytes = api.maxUploadBytes) }
        return api
    }

    private fun onUnauthorized() {
        // May be raised from an OkHttp callback: hop to Main before touching
        // sockets, transfer maps or UI state.
        viewModelScope.launch {
            if (_state.value.phase != Phase.INBOX) return@launch
            teardownClient()
            val paired = sessionStore.load()?.hubId?.isNotBlank() == true
            _state.update {
                it.copy(
                    phase = Phase.LOGIN,
                    hasStoredPairing = paired,
                    items = emptyList(),
                    nextCursor = "",
                    conn = ConnState.CLOSED,
                    pairingCode = "",
                    loggingIn = false,
                    loginError = app.getString(R.string.error_unauthorized),
                )
            }
            deliverPendingShare()
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
            eventStream = null
            return
        }
        startEventStream()
        resync()
        deliverPendingShare()
    }

    /**
     * A dropped link or a failed request asks for a reselection. The connector
     * coalesces concurrent requests and throttles failure-driven ones, so a
     * burst of socket callbacks can never open a second link.
     */
    private fun requestReselect(trigger: ConnectTrigger) {
        if (!foreground) return
        if (sessionStore.load()?.hubId?.isNotBlank() != true) return
        if (_state.value.phase == Phase.BOOTING) return
        viewModelScope.launch { handleConnectResult(connector.connect(trigger)) }
    }


    // ---------- realtime ----------

    private fun startEventStream() {
        // Never open a socket from a background state, whatever the caller path.
        if (!foreground) return
        if (eventStream != null) return
        eventStream = EventStream(
            scope = viewModelScope,
            clientProvider = { client },
            onEvent = { type, id ->
                when (type) {
                    "resync" -> resync()
                    "item.created" -> if (id.isNotEmpty()) fetchAndInsert(id)
                    "item.deleted" -> _state.update { s -> s.copy(items = s.items.filterNot { it.id == id }) }
                }
            },
            onState = { conn -> _state.update { it.copy(conn = conn) } },
            onUnauthorized = { onUnauthorized() },
            onUnavailable = { requestReselect(ConnectTrigger.STREAM_FAILED) },
        ).also { it.start() }
    }

    /**
     * Called from the Activity when it becomes visible again.
     *
     * Every paired foreground reselects through the connector — LAN first, then
     * Tailscale — so coming back to the home LAN does not keep the Tailscale
     * address that was good on the road. The startup round and this one coalesce
     * into a single selection (the connector owns that), including the case where
     * a background event cancelled the boot restore.
     */
    fun onForeground() {
        foreground = true
        val snapshot = _state.value
        if (snapshot.phase == Phase.BOOTING || snapshot.hasStoredPairing) {
            viewModelScope.launch {
                handleConnectResult(connector.connect(ConnectTrigger.FOREGROUND))
            }
        }
        if (snapshot.phase != Phase.INBOX) return
        // Keep the current link usable while the reselection runs; a winning
        // bind replaces this stream, so there is never more than one link.
        eventStream?.let { it.restart() } ?: startEventStream()
        resync()
    }

    /** Called when the Activity goes to the background: no sockets, no probing. */
    fun onBackground() {
        foreground = false
        eventStream?.stop()
        // No background discovery: stop NSD and release the multicast lock.
        discovery.stop()
        // Abandon a half-finished selection round; it is redone on foreground.
        connector.cancel()
    }

    // ---------- inbox ----------

    fun resync() {
        val api = client ?: return
        if (_state.value.loading) return
        _state.update { it.copy(loading = true) }
        viewModelScope.launch {
            try {
                val page = withContext(Dispatchers.IO) { api.list(null, ApiClient.PAGE_SIZE) }
                _state.update {
                    it.copy(
                        items = page.items,
                        nextCursor = page.nextCursor,
                        loading = false,
                        error = null,
                        maxUploadBytes = api.maxUploadBytes,
                    )
                }
            } catch (e: ApiException) {
                if (e.isUnauthorized) {
                    onUnauthorized()
                } else {
                    _state.update { it.copy(loading = false, error = friendly(e)) }
                    // A read may be retried freely; the user never has to retype
                    // anything, and no write is replayed on the new address.
                    if (e.isNetwork) requestReselect(ConnectTrigger.NETWORK_LOST)
                }
            } catch (e: Exception) {
                _state.update { it.copy(loading = false, error = friendly(e)) }
            }
        }
    }

    fun loadMore() {
        val api = client ?: return
        val snapshot = _state.value
        if (snapshot.loadingMore || snapshot.nextCursor.isEmpty()) return
        _state.update { it.copy(loadingMore = true) }
        viewModelScope.launch {
            try {
                val page = withContext(Dispatchers.IO) { api.list(snapshot.nextCursor, ApiClient.PAGE_SIZE) }
                _state.update { s ->
                    val seen = s.items.mapTo(HashSet()) { it.id }
                    s.copy(
                        items = s.items + page.items.filterNot { it.id in seen },
                        nextCursor = page.nextCursor,
                        loadingMore = false,
                    )
                }
            } catch (e: Exception) {
                if (e is ApiException && e.isUnauthorized) onUnauthorized()
                _state.update { it.copy(loadingMore = false) }
                toast(friendly(e), error = true)
            }
        }
    }

    private fun fetchAndInsert(id: String) {
        val api = client ?: return
        viewModelScope.launch {
            try {
                val item = withContext(Dispatchers.IO) { api.getItem(id) }
                _state.update { s ->
                    if (s.items.any { it.id == item.id }) s else s.copy(items = listOf(item) + s.items)
                }
            } catch (_: Exception) {
                // A later resync repairs the list.
            }
        }
    }

    fun sendText() {
        val api = client ?: return
        val snapshot = _state.value
        val value = composerText.value.trim()
        if (value.isEmpty() || snapshot.sendingText) return
        val type = if (LINK_REGEX.matches(value)) "link" else "text"
        _state.update { it.copy(sendingText = true) }
        viewModelScope.launch {
            try {
                val item = withContext(Dispatchers.IO) {
                    api.createText(type, value, _state.value.deviceName)
                }
                _state.update { s ->
                    s.copy(
                        sendingText = false,
                        items = if (s.items.any { it.id == item.id }) s.items else listOf(item) + s.items,
                    )
                }
                composerText.value = ""
            } catch (e: Exception) {
                _state.update { it.copy(sendingText = false) }
                if (e is ApiException && e.isUnauthorized) onUnauthorized() else toast(friendly(e), true)
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
        val api = client ?: return
        val snapshot = _state.value.items
        _state.update { s -> s.copy(items = s.items.filterNot { it.id == id }) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { api.delete(id) }
                toast("已删除")
            } catch (e: Exception) {
                // Restore and tell the user; a resync would also fix it.
                _state.update { s -> s.copy(items = snapshot) }
                if (e is ApiException && e.isUnauthorized) onUnauthorized() else toast(friendly(e), true)
            }
        }
    }

    fun updateDeviceName(name: String) {
        val trimmed = name.take(64)
        prefs.deviceName = trimmed
        _state.update { it.copy(deviceName = trimmed) }
    }

    // ---------- uploads ----------

    fun uploadUris(uris: List<Uri>) {
        val api = client ?: run {
            toast("请先登录", true)
            return
        }
        if (uris.isEmpty()) return
        val sourceList = uris.mapNotNull { uri ->
            runCatching { UriSources.fromUri(app.contentResolver, uri) }.getOrNull()
        }
        enqueueUploads(api, sourceList)
    }

    fun uploadLocalFiles(files: List<File>) {
        val api = client ?: return
        enqueueUploads(api, files.map { UriSources.fromFile(it) })
    }

    private fun enqueueUploads(
        api: ApiClient,
        sources: List<UploadSource>,
        cleanup: ((UploadSource) -> Unit)? = null,
    ) {
        if (sources.isEmpty()) return
        // One item per attachment, uploaded sequentially: the Hub accepts a
        // single file per request and limits concurrent uploads.
        viewModelScope.launch {
            for (source in sources) {
                val limit = _state.value.maxUploadBytes
                if (source.size != null && source.size > limit) {
                    toast(app.getString(R.string.error_upload_too_large) + "（${source.name}）", true)
                    continue
                }
                val key = UUID.randomUUID().toString()
                val task = UploadTask(key = key, name = source.name, size = source.size ?: 0)
                _state.update { it.copy(uploads = it.uploads + task) }
                val job = launch {
                    try {
                        val item = api.upload(source, _state.value.deviceName) { loaded, total ->
                            updateUpload(key) { it.copy(loaded = loaded, size = total ?: it.size) }
                        }
                        if (client !== api) {
                            // The address switched while this upload was in flight:
                            // its result belongs to the old origin, so drop it.
                            _state.update { s -> s.copy(uploads = s.uploads.filterNot { it.key == key }) }
                            return@launch
                        }
                        updateUpload(key) { it.copy(status = TaskStatus.DONE, loaded = it.size) }
                        _state.update { s ->
                            s.copy(items = if (s.items.any { it.id == item.id }) s.items else listOf(item) + s.items)
                        }
                        _state.update { s -> s.copy(uploads = s.uploads.filterNot { it.key == key }) }
                    } catch (e: CancellationException) {
                        _state.update { s -> s.copy(uploads = s.uploads.filterNot { it.key == key }) }
                        throw e
                    } catch (e: Exception) {
                        if (e is ApiException && e.isUnauthorized) {
                            onUnauthorized()
                            return@launch
                        }
                        updateUpload(key) {
                            it.copy(status = TaskStatus.FAILED, error = friendly(e))
                        }
                        toast("「${source.name}」上传失败：${friendly(e)}", true)
                    } finally {
                        uploadJobs.remove(key)
                        cleanup?.invoke(source)
                    }
                }
                uploadJobs[key] = job
                job.join()
            }
        }
    }

    fun cancelUpload(key: String) {
        uploadJobs.remove(key)?.cancel()
        _state.update { s -> s.copy(uploads = s.uploads.filterNot { it.key == key }) }
    }

    fun dismissUpload(key: String) {
        _state.update { s -> s.copy(uploads = s.uploads.filterNot { it.key == key }) }
    }

    private fun updateUpload(key: String, transform: (UploadTask) -> UploadTask) {
        _state.update { s ->
            s.copy(uploads = s.uploads.map { if (it.key == key) transform(it) else it })
        }
    }

    // ---------- downloads ----------

    /** Streams the attachment to Downloads (or app storage on old Android). */
    fun saveItem(item: Item) {
        val api = client ?: return
        val blob = item.blob ?: return
        val key = "save:${item.id}"
        if (downloadJobs.containsKey(key)) return
        val task = DownloadTask(key = key, itemId = item.id, name = blob.name, total = blob.sizeBytes)
        _state.update { it.copy(downloads = it.downloads + task) }
        val job = viewModelScope.launch {
            val target = DownloadTarget.create(app, blob.name, blob.contentType)
            try {
                withContext(Dispatchers.IO) {
                    target.openSink().use { sink ->
                        api.download(item.id, sink) { loaded, total ->
                            // Called from the IO dispatcher; UiState updates are thread-safe.
                            updateDownload(key) { it.copy(loaded = loaded, total = total ?: it.total) }
                        }
                    }
                }
                (target as? DownloadTarget.MediaStoreTarget)?.finish(true)
                updateDownload(key) { it.copy(status = TaskStatus.DONE) }
                toast(app.getString(R.string.saved_to_downloads, target.location))
            } catch (e: CancellationException) {
                (target as? DownloadTarget.MediaStoreTarget)?.finish(false)
                (target as? DownloadTarget.AppFileTarget)?.file()?.delete()
                _state.update { s -> s.copy(downloads = s.downloads.filterNot { it.key == key }) }
                throw e
            } catch (e: Exception) {
                (target as? DownloadTarget.MediaStoreTarget)?.finish(false)
                (target as? DownloadTarget.AppFileTarget)?.file()?.delete()
                if (e is ApiException && e.isUnauthorized) onUnauthorized()
                updateDownload(key) { it.copy(status = TaskStatus.FAILED, error = friendly(e)) }
                toast("${app.getString(R.string.error_save_failed)}：${friendly(e)}", true)
            } finally {
                downloadJobs.remove(key)
            }
        }
        downloadJobs[key] = job
    }

    /**
     * Downloads an APK into app cache and hands it to the system installer.
     * The app never installs silently; Android shows its own confirmation.
     */
    fun installApk(item: Item) {
        val api = client ?: return
        val blob = item.blob ?: return
        val key = "apk:${item.id}"
        if (downloadJobs.containsKey(key)) return
        val task = DownloadTask(key = key, itemId = item.id, name = blob.name, total = blob.sizeBytes)
        _state.update { it.copy(downloads = it.downloads + task) }
        val job = viewModelScope.launch {
            val dir = File(app.cacheDir, "downloads").apply { mkdirs() }
            val file = File(dir, DownloadTarget.sanitize(blob.name))
            try {
                withContext(Dispatchers.IO) {
                    file.outputStream().use { sink ->
                        api.download(item.id, sink) { loaded, total ->
                            updateDownload(key) { it.copy(loaded = loaded, total = total ?: it.total) }
                        }
                    }
                }
                updateDownload(key) { it.copy(status = TaskStatus.DONE) }
                val uri = ApkInstaller.contentUriFor(app, file)
                val started = dev.flowdrop.android.device.SystemActions.installApk(app, uri)
                if (!started) toast(app.getString(R.string.error_apk_install), true)
            } catch (e: CancellationException) {
                file.delete()
                _state.update { s -> s.copy(downloads = s.downloads.filterNot { it.key == key }) }
                throw e
            } catch (e: Exception) {
                file.delete()
                if (e is ApiException && e.isUnauthorized) onUnauthorized()
                updateDownload(key) { it.copy(status = TaskStatus.FAILED, error = friendly(e)) }
                toast("${app.getString(R.string.error_download_failed)}：${friendly(e)}", true)
            } finally {
                downloadJobs.remove(key)
            }
        }
        downloadJobs[key] = job
    }

    fun cancelDownload(key: String) {
        downloadJobs.remove(key)?.cancel()
        _state.update { s -> s.copy(downloads = s.downloads.filterNot { it.key == key }) }
    }

    fun dismissDownload(key: String) {
        _state.update { s -> s.copy(downloads = s.downloads.filterNot { it.key == key }) }
    }

    private fun updateDownload(key: String, transform: (DownloadTask) -> DownloadTask) {
        _state.update { s ->
            s.copy(downloads = s.downloads.map { if (it.key == key) transform(it) else it })
        }
    }

    // ---------- system share ----------

    fun submitShare(payload: SharePayload) {
        if (payload.isEmpty()) return
        if (_state.value.phase != Phase.INBOX) {
            pendingShare = payload
            return
        }
        processShare(payload)
    }

    private fun processShare(payload: SharePayload) {
        val api = client ?: run {
            pendingShare = payload
            return
        }
        // A shared URL is a link item; plain text is a text item.
        payload.texts.firstOrNull()?.trim()?.takeIf { it.isNotEmpty() }?.let { text ->
            val type = if (LINK_REGEX.matches(text)) "link" else "text"
            viewModelScope.launch {
                try {
                    val item = withContext(Dispatchers.IO) {
                        api.createText(type, text, _state.value.deviceName)
                    }
                    _state.update { s ->
                        s.copy(items = if (s.items.any { it.id == item.id }) s.items else listOf(item) + s.items)
                    }
                    toast(app.getString(R.string.share_done))
                } catch (e: Exception) {
                    if (e is ApiException && e.isUnauthorized) onUnauthorized() else toast(friendly(e), true)
                }
            }
        }
        if (payload.uris.isNotEmpty()) {
            // Share URIs carry a temporary read grant tied to the launching
            // Intent/Activity. Copy them into the app cache first, streaming
            // with a fixed buffer (constant memory), so a long upload can
            // never lose the grant mid-transfer. The copies are deleted as
            // soon as each upload settles.
            viewModelScope.launch {
                val staged = mutableMapOf<String, File>()
                val sources = mutableListOf<UploadSource>()
                try {
                    for (uri in payload.uris) {
                        val result = withContext(Dispatchers.IO) {
                            stageSharedUri(uri, _state.value.maxUploadBytes)
                        } ?: continue
                        staged[result.first.name] = result.second
                        sources += result.first
                    }
                } catch (e: Exception) {
                    toast("无法读取分享内容：${friendly(e)}", true)
                }
                if (sources.isNotEmpty()) {
                    enqueueUploads(api, sources, cleanup = { source ->
                        staged[source.name]?.delete()
                    })
                }
            }
        }
    }

    /**
     * Streams a granted content URI into the app cache and returns an
     * [UploadSource] backed by that file. Returns null when the size exceeds
     * the Hub limit (the user is told) or the URI cannot be opened.
     */
    private fun stageSharedUri(uri: Uri, limit: Long): Pair<UploadSource, File>? {
        val dir = File(app.cacheDir, "share-in").apply { mkdirs() }
        val source = UriSources.fromUri(app.contentResolver, uri)
        if (source.size != null && source.size > limit) {
            toast(app.getString(R.string.error_upload_too_large) + "（${source.name}）", true)
            return null
        }
        val target = File(dir, DownloadTarget.sanitize("${System.currentTimeMillis()}-${source.name}"))
        try {
            source.open().use { input ->
                target.outputStream().use { output ->
                    val buffer = ByteArray(64 * 1024)
                    var written = 0L
                    while (true) {
                        val n = input.read(buffer)
                        if (n == -1) break
                        written += n
                        if (written > limit) {
                            target.delete()
                            toast(app.getString(R.string.error_upload_too_large) + "（${source.name}）", true)
                            return null
                        }
                        output.write(buffer, 0, n)
                    }
                }
            }
        } catch (e: Exception) {
            target.delete()
            throw e
        }
        // Keep the friendly original name for the multipart filename, but read
        // from the staged copy so the grant can expire safely.
        return UriSources.fromFile(target).copy(name = source.name) to target
    }

    private fun deliverPendingShare() {
        val pending = pendingShare ?: return
        if (_state.value.phase != Phase.INBOX) return
        pendingShare = null
        processShare(pending)
    }

    // ---------- toasts ----------

    fun toast(message: String, error: Boolean = false) {
        _state.update { s ->
            s.copy(toasts = (s.toasts + Toast(System.nanoTime(), message, error)).takeLast(4))
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
        connector.cancel()
        discovery.release()
        images.clear()
    }

    companion object {
        private const val PAIRING_CODE_LENGTH = 4

        /** `restorePairedSession` maps `authenticated:false` to this code. */
        private const val CODE_PAIRING_REJECTED = "pairing_rejected"

        private const val TRANSFER_INTERRUPTED = "连接地址已切换，进行中的传输已中断，请重试"

        private val LINK_REGEX = Regex("^https?://\\S+$", RegexOption.IGNORE_CASE)
    }
}

/** Text and content URIs extracted from an incoming ACTION_SEND intent. */
data class SharePayload(
    val texts: List<String> = emptyList(),
    val uris: List<Uri> = emptyList(),
) {
    fun isEmpty(): Boolean = texts.isEmpty() && uris.isEmpty()
}
