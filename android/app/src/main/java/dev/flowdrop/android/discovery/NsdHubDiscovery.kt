package dev.flowdrop.android.discovery

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.nio.charset.StandardCharsets

/**
 * Android NSD (mDNS/DNS-SD) implementation of [DiscoveryDriver].
 *
 * Design constraints:
 * - one discovery session at a time; all NsdManager calls happen on the main
 *   looper and are torn down by [stop];
 * - `resolveService` is serialized (one in-flight resolve, then a watchdog)
 *   because the legacy API rejects concurrent resolves and can silently drop
 *   a callback; a per-resolve timeout keeps the queue moving;
 * - only FlowDrop instances are resolved, and `NsdServiceInfo` is converted to
 *   a plain [RawService] before the pure validation in [DiscoveryProtocol];
 * - mDNS reception on Wi-Fi may need a multicast lock on Android 12 and older
 *   (and Android 13 devices without the T extension). It is acquired only
 *   while the foreground login screen is searching and always released;
 * - no background service, no Wi-Fi scan, no location permission.
 */
class NsdHubDiscovery(context: Context) : DiscoveryDriver {

    private val appContext = context.applicationContext
    private val nsdManager = appContext.getSystemService(Context.NSD_SERVICE) as? NsdManager
    private val wifiManager = appContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
    private val main = Handler(Looper.getMainLooper())

    private var eventSink: ((DiscoveryEvent) -> Unit)? = null
    private var active = false
    private var runId = 0
    private var discoveryListener: NsdManager.DiscoveryListener? = null

    private val seenNames = HashSet<String>()
    private val pending = ArrayDeque<NsdServiceInfo>()
    private var resolving = false
    private var resolveName: String? = null
    private var resolveStale = false
    private var resolveAttempt = 0
    private var startAttempt = 0
    private var multicastLock: WifiManager.MulticastLock? = null

    override fun start(onEvent: (DiscoveryEvent) -> Unit) {
        runOnMain { startOnMain(onEvent) }
    }

    override fun stop() {
        runOnMain { stopOnMain() }
    }

    private fun startOnMain(onEvent: (DiscoveryEvent) -> Unit) {
        // A refresh is stop+start; make sure no previous callback survives.
        stopOnMain()

        runId += 1
        val id = runId
        active = true
        eventSink = onEvent
        startAttempt = 0
        seenNames.clear()
        pending.clear()

        val manager = nsdManager
        if (manager == null) {
            fail(id, "此设备不支持网络服务发现")
            return
        }

        acquireMulticastLock()

        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {
                if (!isCurrent(id)) return
                dispatch(DiscoveryEvent.Started)
            }

            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                if (!isCurrent(id)) return
                val name = serviceInfo.serviceName ?: return
                // Cheap local filter: never resolve unrelated mDNS services.
                if (!name.startsWith(DiscoveryProtocol.INSTANCE_PREFIX)) return
                if (!seenNames.add(name)) return
                pending.addLast(serviceInfo)
                pump(id)
            }

            override fun onServiceLost(serviceInfo: NsdServiceInfo) {
                if (!isCurrent(id)) return
                val name = serviceInfo.serviceName ?: return
                seenNames.remove(name)
                pending.removeAll { it.serviceName == name }
                if (resolving && resolveName == name) resolveStale = true
                dispatch(DiscoveryEvent.Lost(name))
            }

            override fun onDiscoveryStopped(serviceType: String) {
                // The controller already moved on; nothing to do.
            }

            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                if (!isCurrent(id)) return
                // stopServiceDiscovery is asynchronous. A quick refresh can
                // still see FAILURE_ALREADY_ACTIVE; retry briefly instead of
                // giving up and disabling discovery for the whole session.
                if (errorCode == NsdManager.FAILURE_ALREADY_ACTIVE && startAttempt < MAX_START_RETRIES) {
                    startAttempt += 1
                    val self = this
                    main.postDelayed({
                        if (isCurrent(id)) attemptDiscovery(id, self)
                    }, START_RETRY_DELAY_MS)
                    return
                }
                fail(id, "服务发现启动失败（错误码 $errorCode）")
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                // Best effort: the session is already considered stopped.
            }
        }
        discoveryListener = listener
        attemptDiscovery(id, listener)
    }

    private fun attemptDiscovery(id: Int, listener: NsdManager.DiscoveryListener) {
        if (!isCurrent(id)) return
        val manager = nsdManager ?: return
        try {
            manager.discoverServices(
                DiscoveryProtocol.SERVICE_TYPE,
                NsdManager.PROTOCOL_DNS_SD,
                listener,
            )
        } catch (e: Exception) {
            fail(id, e.message ?: "服务发现启动失败")
        }
    }

    private fun stopOnMain() {
        active = false
        runId += 1
        // Delayed retry/resolve callbacks from this run are no-ops because
        // isCurrent(id) compares against the advanced runId. They are not
        // removed with removeCallbacksAndMessages(null) (that would also drop
        // a stop() posted from another thread right after start()).
        pending.clear()
        seenNames.clear()
        resolving = false
        resolveName = null
        resolveStale = false
        resolveAttempt += 1

        val listener = discoveryListener
        discoveryListener = null
        if (listener != null) {
            runCatching { nsdManager?.stopServiceDiscovery(listener) }
        }
        releaseMulticastLock()
        eventSink = null
    }

    /**
     * Serial resolve queue. Only one `resolveService` call is outstanding;
     * the next starts on success, failure, watchdog expiry or a synchronous
     * exception, so one bad record cannot stall discovery.
     */
    @Suppress("DEPRECATION")
    private fun pump(id: Int) {
        if (!isCurrent(id) || resolving) return
        val manager = nsdManager ?: return
        val next = pending.removeFirstOrNull() ?: return

        val name = next.serviceName ?: return
        resolving = true
        resolveName = name
        resolveStale = false
        val attempt = ++resolveAttempt

        val watchdog = Runnable {
            if (isCurrent(id) && resolving && resolveAttempt == attempt) {
                resolving = false
                resolveName = null
                resolveStale = false
                pump(id)
            }
        }
        main.postDelayed(watchdog, RESOLVE_TIMEOUT_MS)

        try {
            manager.resolveService(next, object : NsdManager.ResolveListener {
                override fun onServiceResolved(serviceInfo: NsdServiceInfo) {
                    if (!isCurrent(id) || resolveAttempt != attempt) return
                    main.removeCallbacks(watchdog)
                    val stale = resolveStale
                    resolving = false
                    resolveName = null
                    resolveStale = false
                    if (!stale) {
                        toRaw(serviceInfo)?.let { dispatch(DiscoveryEvent.Found(it)) }
                    }
                    pump(id)
                }

                override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                    if (!isCurrent(id) || resolveAttempt != attempt) return
                    main.removeCallbacks(watchdog)
                    resolving = false
                    resolveName = null
                    resolveStale = false
                    pump(id)
                }
            })
        } catch (e: Exception) {
            main.removeCallbacks(watchdog)
            resolving = false
            resolveName = null
            resolveStale = false
            pump(id)
        }
    }

    @Suppress("DEPRECATION")
    private fun toRaw(info: NsdServiceInfo): RawService? {
        val name = info.serviceName ?: return null
        val hostAddresses = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            // API 34+ can expose every address of a multi-homed host. The
            // pure parser picks the first explicit RFC1918 IPv4.
            info.hostAddresses.mapNotNull { it.hostAddress }
        } else {
            listOfNotNull(info.host?.hostAddress)
        }
        val txt = info.attributes?.entries?.associate { (key, value) ->
            key to String(value ?: ByteArray(0), StandardCharsets.UTF_8)
        } ?: emptyMap()
        return RawService(
            serviceName = name,
            serviceType = info.serviceType ?: DiscoveryProtocol.SERVICE_TYPE,
            hostAddresses = hostAddresses,
            port = info.port,
            txt = txt,
        )
    }

    private fun acquireMulticastLock() {
        if (multicastLock?.isHeld == true) return
        val lock = runCatching {
            wifiManager?.createMulticastLock(MULTICAST_LOCK_TAG)
        }.getOrNull() ?: return
        lock.setReferenceCounted(false)
        runCatching { lock.acquire() }
        multicastLock = lock
    }

    private fun releaseMulticastLock() {
        val lock = multicastLock ?: return
        multicastLock = null
        runCatching { if (lock.isHeld) lock.release() }
    }

    private fun fail(id: Int, reason: String?) {
        if (!isCurrent(id)) return
        val sink = eventSink
        stopOnMain()
        sink?.invoke(DiscoveryEvent.Failed(reason))
    }

    private fun dispatch(event: DiscoveryEvent) {
        eventSink?.invoke(event)
    }

    private fun isCurrent(id: Int): Boolean = active && id == runId

    private inline fun runOnMain(crossinline block: () -> Unit) {
        if (Looper.myLooper() == Looper.getMainLooper()) {
            block()
        } else {
            main.post { block() }
        }
    }

    private companion object {
        const val MULTICAST_LOCK_TAG = "flowdrop-mdns"
        const val RESOLVE_TIMEOUT_MS = 5_000L
        const val START_RETRY_DELAY_MS = 400L
        const val MAX_START_RETRIES = 3
    }
}
