package dev.flowdrop.android

import android.app.Application
import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.HubVault
import dev.flowdrop.android.data.InMemorySecureStorage
import dev.flowdrop.android.data.KeystoreSecureStorage
import dev.flowdrop.android.data.ConnectivityNetworkWatcher
import dev.flowdrop.android.data.NetworkWatcher
import dev.flowdrop.android.data.OutboxStore
import dev.flowdrop.android.data.PersistentCookieJar
import dev.flowdrop.android.data.SecureStorage
import dev.flowdrop.android.data.SessionStore
import dev.flowdrop.android.discovery.DiscoveryDriver
import dev.flowdrop.android.discovery.NsdHubDiscovery
import java.io.File

class FlowDropApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}

/**
 * Process-wide singletons. Cookies are the only credential the app keeps and
 * they live encrypted at rest; nothing else in this container is sensitive.
 *
 * Paired computers are kept apart by [vault]: every Hub gets its own
 * [SessionStore] and its own [PersistentCookieJar], so a client bound to one Hub
 * cannot read or write another Hub's credentials, whatever the user switches to.
 */
class AppContainer(context: Context) {
    val appContext: Context = context.applicationContext
    val preferences = AppPreferences(appContext)

    /**
     * Multi-computer vault in its own Keystore-encrypted preference slot.
     *
     * The previous single-session blob (`flowdrop.session` / `blob`) is left
     * untouched instead of being read, migrated or overwritten: this build does
     * not reuse that layout, and silently rewriting it would destroy the only
     * copy of the old pairing. A user upgrading from a single-computer build
     * therefore pairs again, once, and nothing of the old state is lost.
     */
    val secureStorage: SecureStorage = KeystoreSecureStorage(appContext, blobKey = HUB_VAULT_BLOB_KEY)
    val vault = HubVault(secureStorage)

    /**
     * Durable queue of items sent while their Hub was unreachable.
     *
     * It lives in app-private, persistent (non-cache, non-backed-up) storage so
     * neither the system cache trimmer nor a backup/restore can drop unsent
     * content.
     */
    val outbox = OutboxStore(File(appContext.noBackupFilesDir, OUTBOX_DIR))

    /** Guards the one-time queue recovery; only touched under [outboxPrepareLock]. */
    private val outboxPrepareLock = Any()
    private var outboxPrepared = false

    /**
     * Prepares the queue for use: entries left in SENDING by a *previous* process
     * become UNCONFIRMED instead of being replayed.
     *
     * Synchronous and serial: the first caller runs the recovery, every later
     * call in the same process returns immediately — so an Activity/ViewModel
     * rebuild can never turn a send that is still in flight into UNCONFIRMED. A
     * failure is not cached (the store only commits complete state): it is thrown
     * to the caller, which runs on `Dispatchers.IO` and surfaces the message.
     * Never call this from the main thread.
     */
    fun prepareOutbox() {
        synchronized(outboxPrepareLock) {
            if (outboxPrepared) return
            outbox.recoverInterrupted()
            outboxPrepared = true
        }
    }

    /**
     * A client bound to one saved Hub and only that Hub's credentials.
     *
     * Nothing here switches a shared store: building a client for Hub B leaves
     * Hub A's cookies, CSRF token and device token exactly where they were, so a
     * failed or abandoned switch never costs the user their working session.
     */
    @Synchronized
    fun newHubClient(
        hubId: String,
        endpoint: HubEndpoint,
        onUnauthorized: (ApiClient) -> Unit,
    ): ApiClient {
        val store = vault.store(hubId)
        val client = ApiClient(endpoint, store, PersistentCookieJar(store))
        // The callback carries its own client instance: a late 401 of a replaced
        // client can be told apart from the current one without trusting hubId.
        client.onUnauthorized = { onUnauthorized(client) }
        return client
    }

    /**
     * An isolated, in-memory attempt used while the user pairs a *new* computer.
     *
     * A pairing attempt must not be able to touch an already saved pairing: a
     * wrong code, a cancelled form or a Hub that answers with a different ID may
     * only end this attempt. Only a successful pair (which reveals the Hub ID)
     * is copied into [vault].
     */
    @Synchronized
    fun newPairingAttempt(endpoint: HubEndpoint, onUnauthorized: (ApiClient) -> Unit): PairingAttempt {
        val store = SessionStore(InMemorySecureStorage())
        store.switchTo(endpoint)
        val client = ApiClient(endpoint, store, PersistentCookieJar(store))
        client.onUnauthorized = { onUnauthorized(client) }
        return PairingAttempt(store, client)
    }

    /**
     * A fresh foreground-only NSD session. The ViewModel owns its lifecycle;
     * the driver holds no background service and is stopped on background.
     */
    fun newDiscoveryDriver(): DiscoveryDriver = NsdHubDiscovery(appContext)

    /**
     * A fresh foreground-only default-network watcher. Like the discovery
     * driver it is started and stopped by the ViewModel, so no callback arrives
     * while the app is in the background.
     */
    fun newNetworkWatcher(): NetworkWatcher = ConnectivityNetworkWatcher(appContext)

    private companion object {
        /** Encrypted multi-Hub registry, separate from the legacy session blob. */
        const val HUB_VAULT_BLOB_KEY = "hubs_v1"

        /** App-private, persistent queue directory (never the cache dir). */
        const val OUTBOX_DIR = "outbox"
    }
}

/**
 * A single pairing attempt: its own throwaway store and the client bound to it.
 * Nothing is persisted until the Hub accepts the device and reports its ID.
 */
class PairingAttempt(val store: SessionStore, val client: ApiClient)

/**
 * Non-sensitive UI preferences: last used address and name, the device nickname
 * and the address form. Pairing codes, device tokens and cookies live in the
 * Keystore-backed [HubVault] instead; nothing here is a credential.
 */
class AppPreferences(context: Context) {
    private val prefs = context.getSharedPreferences("flowdrop.prefs", Context.MODE_PRIVATE)

    var hubAddress: String
        get() = prefs.getString(KEY_HUB, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_HUB, value).apply()

    var hubName: String
        get() = prefs.getString(KEY_HUB_NAME, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_HUB_NAME, value.take(120)).apply()

    var deviceName: String
        get() = prefs.getString(KEY_DEVICE, null) ?: defaultDeviceName()
        set(value) = prefs.edit().putString(KEY_DEVICE, value.take(64)).apply()

    private fun defaultDeviceName(): String = try {
        val model = android.os.Build.MODEL?.trim().orEmpty()
        if (model.isEmpty()) "Android" else "Android · $model"
    } catch (_: Exception) {
        "Android"
    }

    private companion object {
        const val KEY_HUB = "hub_address"
        const val KEY_HUB_NAME = "hub_name"
        const val KEY_DEVICE = "device_name"
    }
}

/** ViewModel factory wiring the app container without a DI framework. */
class AppViewModelFactory(private val app: FlowDropApp) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        if (modelClass.isAssignableFrom(AppViewModel::class.java)) {
            return AppViewModel(app, app.container) as T
        }
        throw IllegalArgumentException("unknown view model: ${modelClass.name}")
    }
}
