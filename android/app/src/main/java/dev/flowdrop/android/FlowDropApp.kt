package dev.flowdrop.android

import android.app.Application
import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import dev.flowdrop.android.data.ApiClient
import dev.flowdrop.android.data.HubEndpoint
import dev.flowdrop.android.data.KeystoreSecureStorage
import dev.flowdrop.android.data.PersistentCookieJar
import dev.flowdrop.android.data.SecureStorage
import dev.flowdrop.android.data.SessionStore
import dev.flowdrop.android.discovery.DiscoveryDriver
import dev.flowdrop.android.discovery.NsdHubDiscovery

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
 */
class AppContainer(context: Context) {
    val appContext: Context = context.applicationContext
    val preferences = AppPreferences(appContext)
    val secureStorage: SecureStorage = KeystoreSecureStorage(appContext)
    val sessionStore = SessionStore(secureStorage)
    val cookieJar = PersistentCookieJar(sessionStore)

    @Volatile
    private var activeClient: ApiClient? = null

    @Synchronized
    fun activate(endpoint: HubEndpoint, onUnauthorized: () -> Unit): ApiClient {
        // Clears cookies automatically if the origin changed.
        sessionStore.switchTo(endpoint)
        val client = ApiClient(endpoint, sessionStore, cookieJar).also {
            it.onUnauthorized = onUnauthorized
        }
        activeClient = client
        return client
    }

    fun currentClient(): ApiClient? = activeClient

    /**
     * A fresh foreground-only NSD session. The ViewModel owns its lifecycle;
     * the driver holds no background service and is stopped on background.
     */
    fun newDiscoveryDriver(): DiscoveryDriver = NsdHubDiscovery(appContext)

    @Synchronized
    fun deactivate() {
        activeClient?.discardLocalSession()
        activeClient = null
    }
}

/**
 * Non-sensitive UI preferences: last Hub address, username and device nickname.
 * The password is never written anywhere.
 */
class AppPreferences(context: Context) {
    private val prefs = context.getSharedPreferences("flowdrop.prefs", Context.MODE_PRIVATE)

    var hubAddress: String
        get() = prefs.getString(KEY_HUB, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_HUB, value).apply()

    var username: String
        get() = prefs.getString(KEY_USER, "admin").orEmpty()
        set(value) = prefs.edit().putString(KEY_USER, value).apply()

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
        const val KEY_USER = "username"
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
