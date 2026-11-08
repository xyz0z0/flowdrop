package dev.flowdrop.android

import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.ApiException
import dev.flowdrop.android.data.ConnState
import dev.flowdrop.android.data.DEFAULT_MAX_UPLOAD_BYTES
import dev.flowdrop.android.data.EventStream
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.Item
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
    val username: String = "admin",
    val password: String = "",
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
 * - the password lives only in the transient [UiState] while typing and is
 *   cleared immediately after a login attempt (never persisted);
 * - switching Hub addresses clears the previous session first;
 * - any 401 tears the session down and returns to the login screen;
 * - on background the socket is closed; on foreground the list is re-fetched
 *   and the socket restarted, because Android gives no background guarantee.
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
            username = prefs.username,
            deviceName = prefs.deviceName,
        ),
    )
    val state: StateFlow<UiState> = _state.asStateFlow()

    /**
     * Foreground-only nearby-Hub search. It never touches the session or the
     * password: a discovered row only fills the address field.
     */
    private val discovery = DiscoveryController(
        driver = container.newDiscoveryDriver(),
        scope = viewModelScope,
        onState = { next -> _state.update { it.copy(discovery = next) } },
    )

    private var client: ApiClient? = null
    private var eventStream: EventStream? = null
    private var uploadJobs = mutableMapOf<String, Job>()
    private var downloadJobs = mutableMapOf<String, Job>()
    private var pendingShare: SharePayload? = null
    private val images = dev.flowdrop.android.ui.AuthenticatedImageStore { client }

    val imageStore: dev.flowdrop.android.ui.AuthenticatedImageStore get() = images

    init {
        restoreSession()
    }

    // ---------- session lifecycle ----------

    private fun restoreSession() {
        viewModelScope.launch {
            val stored = sessionStore.load()
            val address = stored?.baseUrl?.takeIf { it.isNotBlank() } ?: prefs.hubAddress
            val endpoint = HubEndpoint.parse(address)
            if (endpoint == null || stored == null) {
                _state.update {
                    it.copy(phase = Phase.LOGIN, hubAddress = address, username = prefs.username)
                }
                deliverPendingShare()
                return@launch
            }
            _state.update { it.copy(hubAddress = endpoint.origin) }
            val api = activate(endpoint)
            try {
                val info = api.session()
                if (info.authenticated) {
                    api.persistSession(stored.username, info)
                    enterInbox()
                } else {
                    _state.update { it.copy(phase = Phase.LOGIN) }
                    deliverPendingShare()
                }
            } catch (e: ApiException) {
                _state.update {
                    it.copy(
                        phase = Phase.LOGIN,
                        loginError = if (e.isNetwork) app.getString(R.string.error_network) else null,
                    )
                }
            } catch (e: Exception) {
                _state.update { it.copy(phase = Phase.LOGIN) }
            }
        }
    }

    fun updateHubAddress(value: String) {
        _state.update { it.copy(hubAddress = value, loginError = null) }
    }

    fun updateUsername(value: String) {
        _state.update { it.copy(username = value, loginError = null) }
    }

    fun updatePassword(value: String) {
        _state.update { it.copy(password = value, loginError = null) }
    }

    // ---------- nearby Hub discovery (address hint only) ----------

    /** One automatic short search when the foreground login screen first appears. */
    fun onLoginScreenShown() {
        if (_state.value.phase != Phase.LOGIN) return
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
     * Selecting a nearby Hub only prefills its address. No login, no session
     * restore, no cookie or password is sent to a discovered server: the user
     * still has to press 登录 with the password, exactly like manual entry.
     */
    fun selectDiscoveredHub(hub: DiscoveredHub) {
        if (_state.value.phase != Phase.LOGIN) return
        _state.update { it.copy(hubAddress = hub.baseUrl, loginError = null) }
        // Release multicast/resolve resources; the rows stay visible.
        discovery.stop()
    }

    fun login() {
        val snapshot = _state.value
        if (snapshot.loggingIn) return
        val endpoint = HubEndpoint.parse(snapshot.hubAddress)
        if (endpoint == null) {
            _state.update { it.copy(loginError = app.getString(R.string.error_bad_address)) }
            return
        }
        val username = snapshot.username.trim().ifEmpty { "admin" }
        val password = snapshot.password
        if (password.isEmpty()) return
        _state.update { it.copy(loggingIn = true, loginError = null) }
        viewModelScope.launch {
            // Drop any previous Hub's cookies before touching the new address.
            if (currentOrigin() != null && currentOrigin() != endpoint.origin) {
                logoutLocalOnly()
            }
            val api = activate(endpoint)
            try {
                val info = api.login(username, password)
                // Password is discarded here and never written to storage.
                _state.update { it.copy(password = "", loggingIn = false) }
                prefs.hubAddress = endpoint.origin
                prefs.username = username
                api.persistSession(username, info)
                _state.update {
                    it.copy(
                        maxUploadBytes = api.maxUploadBytes,
                        deviceName = prefs.deviceName,
                    )
                }
                enterInbox()
            } catch (e: ApiException) {
                _state.update {
                    it.copy(
                        password = "",
                        loggingIn = false,
                        loginError = loginErrorMessage(e),
                    )
                }
            } catch (e: Exception) {
                _state.update {
                    it.copy(
                        password = "",
                        loggingIn = false,
                        loginError = app.getString(R.string.error_network),
                    )
                }
            }
        }
    }

    private fun loginErrorMessage(e: ApiException): String = when {
        e.isNotInitialized -> app.getString(R.string.not_initialized_hint)
        e.isNetwork -> app.getString(R.string.error_network)
        e.status == 403 && e.code == "origin_not_allowed" ->
            "Hub 拒绝了该地址：请把 ${_state.value.hubAddress} 的主机名加入 Hub 的 allowed_hosts"
        e.status == 429 -> "登录尝试过于频繁，请稍后再试"
        e.status == 401 || e.code == "invalid_credentials" -> "用户名或密码不正确"
        else -> e.message
    }

    fun logout() {
        val api = client
        viewModelScope.launch {
            eventStream?.stop()
            eventStream = null
            if (api != null) {
                runCatching { withContext(Dispatchers.IO) { api.logout() } }
            }
            logoutLocalOnly()
            _state.update {
                it.copy(
                    phase = Phase.LOGIN,
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

    private fun logoutLocalOnly() {
        eventStream?.stop()
        eventStream = null
        uploadJobs.values.forEach { it.cancel() }
        uploadJobs.clear()
        downloadJobs.values.forEach { it.cancel() }
        downloadJobs.clear()
        container.deactivate()
        client = null
        images.clear()
    }

    private fun currentOrigin(): String? = client?.endpoint?.origin

    private fun activate(endpoint: HubEndpoint): ApiClient {
        val api = container.activate(endpoint) { onUnauthorized() }
        client = api
        _state.update { it.copy(maxUploadBytes = api.maxUploadBytes) }
        return api
    }

    private fun onUnauthorized() {
        if (_state.value.phase != Phase.INBOX) return
        logoutLocalOnly()
        _state.update {
            it.copy(
                phase = Phase.LOGIN,
                items = emptyList(),
                nextCursor = "",
                conn = ConnState.CLOSED,
                password = "",
                loggingIn = false,
                loginError = app.getString(R.string.error_unauthorized),
            )
        }
        deliverPendingShare()
    }

    private fun enterInbox() {
        discovery.clear()
        _state.update {
            it.copy(
                phase = Phase.INBOX,
                loginError = null,
                loggingIn = false,
                deviceName = prefs.deviceName,
            )
        }
        startEventStream()
        resync()
        deliverPendingShare()
    }

    // ---------- realtime ----------

    private fun startEventStream() {
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
        ).also { it.start() }
    }

    /** Called from the Activity when it becomes visible again. */
    fun onForeground() {
        if (_state.value.phase != Phase.INBOX) return
        eventStream?.let { it.restart() } ?: startEventStream()
        resync()
    }

    /** Called when the Activity goes to the background: sockets are not kept. */
    fun onBackground() {
        eventStream?.stop()
        // No background discovery: stop NSD and release the multicast lock.
        discovery.stop()
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
        discovery.release()
        images.clear()
    }

    companion object {
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
