package dev.flowdrop.android.data

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities

/**
 * Reports when the phone's default network changes in a way that can make a
 * different Hub address reachable: office Wi-Fi coming back, mobile data taking
 * over after Wi-Fi is switched off, or the Tailscale VPN being turned on.
 *
 * The hint is deliberately coarse. The caller only uses it to re-run route
 * selection immediately instead of waiting for the next failed request or for
 * the socket's backoff; which address actually works is still decided by
 * probing, never by the platform's report.
 *
 * Implementations must only call back while started and must survive being
 * started twice (the foreground callback fires on every resume).
 */
interface NetworkWatcher {
    fun start(onChanged: () -> Unit)

    /** Stops delivery. Safe when not started, and no callback arrives after it. */
    fun stop()
}

/**
 * Production watcher over [ConnectivityManager.registerDefaultNetworkCallback].
 *
 * Two moments matter:
 *
 * - `onAvailable`: a new default network exists. This is the usual "Wi-Fi came
 *   back" / "mobile data took over" case;
 * - `onCapabilitiesChanged` gaining `NET_CAPABILITY_VALIDATED`: a VPN such as
 *   Tailscale publishes its interface slightly before traffic can pass, so the
 *   first event alone can arrive a moment too early. Refiring on validation
 *   costs one extra probe round and removes a whole backoff period.
 *
 * The Hub is a LAN/tailnet peer, so an unvalidated network is still worth
 * probing: the callback fires, and the probe decides. Capabilities are not used
 * to filter candidates.
 */
class ConnectivityNetworkWatcher(context: Context) : NetworkWatcher {

    private val manager: ConnectivityManager? =
        context.applicationContext.getSystemService(ConnectivityManager::class.java)

    private var callback: ConnectivityManager.NetworkCallback? = null

    /** Guards [callback] so repeated foreground calls register once. */
    private val lock = Any()

    @Volatile
    private var validated = false

    override fun start(onChanged: () -> Unit) {
        synchronized(lock) {
            if (callback != null) return
            val service = manager ?: return
            val registered = object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) {
                    validated = false
                    onChanged()
                }

                override fun onCapabilitiesChanged(
                    network: Network,
                    capabilities: NetworkCapabilities,
                ) {
                    if (!capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) {
                        return
                    }
                    // Only the transition matters: many capability updates arrive
                    // per second, and each one triggering a selection round would
                    // burn battery for nothing.
                    if (validated) return
                    validated = true
                    onChanged()
                }

                override fun onLost(network: Network) {
                    // No route to offer: the replacement network reports itself
                    // through onAvailable, and a socket failure already asks for
                    // a reselection. Firing here would only probe dead addresses.
                    validated = false
                }
            }
            // ACCESS_NETWORK_STATE is a normal permission, but a broken OEM ROM
            // can still refuse the registration; watching networks is an
            // optimisation, never a requirement, so it must not crash the app.
            val ok = try {
                service.registerDefaultNetworkCallback(registered)
                true
            } catch (_: RuntimeException) {
                false
            }
            if (ok) callback = registered
        }
    }

    override fun stop() {
        synchronized(lock) {
            val registered = callback ?: return
            callback = null
            validated = false
            try {
                manager?.unregisterNetworkCallback(registered)
            } catch (_: RuntimeException) {
                // Already unregistered (process teardown or an OEM quirk).
            }
        }
    }
}
