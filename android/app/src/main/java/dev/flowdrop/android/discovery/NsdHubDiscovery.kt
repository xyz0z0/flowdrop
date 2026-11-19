package dev.flowdrop.android.discovery

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log
import java.nio.charset.StandardCharsets

/**
 * Android NSD (mDNS/DNS-SD) implementation of [DiscoveryDriver].
 *
 * Design constraints:
 * - one discovery session at a time; all NsdManager calls happen on the main
 *   looper and are torn down by [stop];
 * - a found record is used directly when the discovery callback already
 *   carries a usable host/port/TXT; otherwise `resolveService` is queued and
 *   serialized (one in-flight resolve, short watchdog) because the legacy API
 *   rejects concurrent resolves and can silently drop a callback;
 * - `resolveService` is never called synchronously from `onServiceFound`, and
 *   the same instance is never queued twice; a resolve is retried at most
 *   [ResolveAttemptTracker.MAX_ATTEMPTS] times before the record is dropped,
 *   and a "successful" callback whose record still fails validation counts as
 *   a failed attempt (some devices return an incomplete record);
 * - only FlowDrop instances are resolved, and `NsdServiceInfo` is converted to
 *   a plain [RawService] before the pure validation in [DiscoveryProtocol];
 * - mDNS reception on Wi-Fi may need a multicast lock on Android 12 and older
 *   (and Android 13 devices without the T extension). It is acquired only
 *   while the foreground login screen is searching and always released;
 * - diagnostics go to logcat under [TAG] and never contain TXT values, tokens
 *   or pairing codes;
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
    /** Instances with a queued (delayed) resolve, so one is never queued twice. */
    private val queuedNames = HashSet<String>()
    private val attempts = ResolveAttemptTracker()
    private var resolving = false
    private var resolveName: String? = null
    private var resolveStale = false
    private var resolveToken = 0
    private var startAttempt = 0
    private var multicastLock: WifiManager.MulticastLock? = null

    override fun start(onEvent: (DiscoveryEvent) -> Unit) {
        runOnMain { startOnMain(onEvent) }
    }

    override fun stop() {
        runOnMain { stopOnMain("stop requested") }
    }

    private fun startOnMain(onEvent: (DiscoveryEvent) -> Unit) {
        // A refresh is stop+start; make sure no previous callback survives.
        stopOnMain("restart")

        runId += 1
        val id = runId
        active = true
        eventSink = onEvent
        startAttempt = 0
        seenNames.clear()
        pending.clear()
        queuedNames.clear()
        attempts.reset()

        Log.d(TAG, "run=$id start")

        val manager = nsdManager
        if (manager == null) {
            fail(id, "此设备不支持网络服务发现")
            return
        }

        acquireMulticastLock()

        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {
                if (!isCurrent(id)) return
                Log.d(TAG, "run=$id discovery started type=$serviceType")
                dispatch(DiscoveryEvent.Started)
            }

            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                if (!isCurrent(id)) return
                val name = serviceInfo.serviceName ?: return
                // Cheap local filter: never resolve unrelated mDNS services.
                if (!name.startsWith(DiscoveryProtocol.INSTANCE_PREFIX)) return
                if (!seenNames.add(name)) return

                Log.d(TAG, "run=$id found service=$name")

                // Some devices fill the discovery record with the full SRV/TXT
                // data; reusing it avoids a resolve that may never answer.
                val direct = directRaw(serviceInfo)
                if (direct != null) {
                    Log.d(
                        TAG,
                        "run=$id use discovery record directly service=$name " +
                            "reason=onServiceFound already carried RFC1918 host, port and TXT; resolve skipped",
                    )
                    dispatch(DiscoveryEvent.Found(direct))
                    return
                }

                // Never resolve synchronously inside onServiceFound: the legacy
                // NsdManager drops the request when it is re-entered here.
                scheduleResolve(id, serviceInfo, RESOLVE_SETTLE_MS)
            }

            override fun onServiceLost(serviceInfo: NsdServiceInfo) {
                if (!isCurrent(id)) return
                val name = serviceInfo.serviceName ?: return
                Log.d(TAG, "run=$id lost service=$name")
                seenNames.remove(name)
                pending.removeAll { it.serviceName == name }
                attempts.clear(name)
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
                    Log.d(TAG, "run=$id discovery start busy, retrying ($startAttempt/$MAX_START_RETRIES)")
                    val self = this
                    main.postDelayed({
                        if (isCurrent(id)) attemptDiscovery(id, self)
                    }, START_RETRY_DELAY_MS)
                    return
                }
                Log.d(TAG, "run=$id discovery start failed errorCode=$errorCode")
                fail(id, "服务发现启动失败（错误码 $errorCode）")
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                // Best effort: the session is already considered stopped.
                Log.d(TAG, "run=$id discovery stop failed errorCode=$errorCode")
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

    private fun stopOnMain(reason: String) {
        if (active) Log.d(TAG, "run=$runId stop reason=$reason")

        active = false
        runId += 1
        // Delayed resolve/retry callbacks from this run are no-ops because
        // isCurrent(id) compares against the advanced runId. They are not
        // removed with removeCallbacksAndMessages(null) (that would also drop
        // a stop() posted from another thread right after start()).
        pending.clear()
        seenNames.clear()
        queuedNames.clear()
        attempts.reset()
        resolving = false
        resolveName = null
        resolveStale = false
        resolveToken += 1

        val listener = discoveryListener
        discoveryListener = null
        if (listener != null) {
            runCatching { nsdManager?.stopServiceDiscovery(listener) }
        }
        releaseMulticastLock()
        eventSink = null
    }

    /**
     * Queues one resolve for [info] after a short main-thread delay.
     * Repeated calls for the same instance while it is queued are dropped.
     */
    private fun scheduleResolve(id: Int, info: NsdServiceInfo, delayMs: Long) {
        val name = info.serviceName ?: return
        if (!queuedNames.add(name)) return
        main.postDelayed({
            if (!isCurrent(id)) return@postDelayed
            queuedNames.remove(name)
            // A lost instance must not be resolved again; onServiceLost drops
            // it from seenNames, so a queued retry is simply skipped.
            if (!seenNames.contains(name)) {
                Log.d(TAG, "run=$id resolve skipped service=$name reason=service lost")
                return@postDelayed
            }
            pending.addLast(info)
            pump(id)
        }, delayMs)
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
        val token = ++resolveToken
        val attempt = attempts.begin(name)
        Log.d(TAG, "run=$id resolve start service=$name attempt=$attempt/${ResolveAttemptTracker.MAX_ATTEMPTS}")

        val watchdog = Runnable {
            if (isCurrent(id) && resolving && resolveToken == token) {
                Log.d(TAG, "run=$id resolve watchdog timeout service=$name attempt=$attempt after ${RESOLVE_TIMEOUT_MS}ms")
                onAttemptFailed(id, next)
            }
        }
        main.postDelayed(watchdog, RESOLVE_TIMEOUT_MS)

        try {
            manager.resolveService(next, object : NsdManager.ResolveListener {
                override fun onServiceResolved(serviceInfo: NsdServiceInfo) {
                    if (!isCurrent(id) || !resolving || resolveToken != token) return
                    main.removeCallbacks(watchdog)
                    val raw = toRaw(serviceInfo)
                    val hub = raw?.let { DiscoveryProtocol.parse(it) }
                    // type, TXT key names and TXT values make an unusable
                    // result diagnosable: a type mismatch shows up as type=...,
                    // and a non-empty TXT with a wrong value shows up as
                    // txt=v=1,scheme=http,path=/. This protocol's TXT carries no
                    // secret (only v/scheme/path), so values are logged too.
                    Log.d(
                        TAG,
                        "run=$id resolve success service=${serviceInfo.serviceName ?: name} " +
                            "type=${raw?.serviceType ?: "-"} " +
                            "txtKeys=${raw?.txt?.keys?.sorted()?.joinToString(",") ?: "-"} " +
                            "txt=${raw?.txt?.entries?.sortedBy { it.key }?.joinToString(",") { "${it.key}=${it.value}" } ?: "-"} " +
                            "host=${hub?.host ?: raw?.hostAddresses?.firstOrNull() ?: "-"} " +
                            "port=${raw?.port ?: 0} usable=${hub != null}",
                    )
                    onAttemptResolved(id, next, raw, resolveStale)
                }

                override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                    if (!isCurrent(id) || !resolving || resolveToken != token) return
                    main.removeCallbacks(watchdog)
                    Log.d(
                        TAG,
                        "run=$id resolve failed service=${serviceInfo.serviceName ?: name} " +
                            "errorCode=$errorCode attempt=$attempt",
                    )
                    onAttemptFailed(id, next)
                }
            })
        } catch (e: Exception) {
            main.removeCallbacks(watchdog)
            if (isCurrent(id) && resolving && resolveToken == token) {
                Log.d(TAG, "run=$id resolve threw service=$name attempt=$attempt ${e.javaClass.simpleName}")
                onAttemptFailed(id, next)
            }
        }
    }

    /** Releases the single-flight slot, then rounds the queue again. */
    private fun onAttemptResolved(id: Int, info: NsdServiceInfo, raw: RawService?, stale: Boolean) {
        val name = resolveName
        resolving = false
        resolveName = null
        resolveStale = false

        val hub = raw?.let { DiscoveryProtocol.parse(it) }
        if (raw != null && hub != null) {
            attempts.clear(hub.serviceName)
            if (stale) {
                Log.d(TAG, "run=$id resolve result dropped service=${hub.serviceName} reason=service lost")
            } else {
                dispatch(DiscoveryEvent.Found(raw))
            }
            pump(id)
            return
        }

        if (stale) {
            if (name != null) attempts.clear(name)
            Log.d(TAG, "run=$id resolve unusable result dropped service=${name ?: "-"} reason=service lost")
            pump(id)
            return
        }

        // Some devices (e.g. HyperOS on Android 16) answer onServiceResolved
        // with an incomplete record such as an empty host. That is not a
        // usable success, so it takes the failure path: bounded retries by the
        // same instance name, then dropped so the rest of the queue proceeds.
        Log.d(TAG, "run=$id resolve unusable service=${name ?: "-"} reason=host/port/TXT rejected by DiscoveryProtocol")
        onAttemptFailed(id, info)
    }

    /**
     * Releases the single-flight slot and either queues a bounded retry for the
     * same instance or abandons it so the rest of the queue can proceed.
     */
    private fun onAttemptFailed(id: Int, info: NsdServiceInfo) {
        val name = info.serviceName ?: resolveName ?: return
        resolving = false
        resolveName = null
        resolveStale = false

        if (attempts.shouldRetryAfterFailure(name)) {
            Log.d(
                TAG,
                "run=$id resolve retry service=$name " +
                    "next=${attempts.attemptsUsed(name) + 1}/${ResolveAttemptTracker.MAX_ATTEMPTS} " +
                    "delay=${RESOLVE_RETRY_DELAY_MS}ms",
            )
            scheduleResolve(id, info, RESOLVE_RETRY_DELAY_MS)
        } else {
            Log.d(TAG, "run=$id resolve abandoned service=$name attempts=${attempts.attemptsUsed(name)}")
            attempts.clear(name)
        }
        pump(id)
    }

    /** The discovery record itself, when it already passes the frozen rules. */
    private fun directRaw(info: NsdServiceInfo): RawService? {
        val raw = toRaw(info) ?: return null
        return if (DiscoveryProtocol.parse(raw) != null) raw else null
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
        Log.d(TAG, "multicast lock acquired")
    }

    private fun releaseMulticastLock() {
        val lock = multicastLock ?: return
        multicastLock = null
        runCatching { if (lock.isHeld) lock.release() }
        Log.d(TAG, "multicast lock released")
    }

    private fun fail(id: Int, reason: String?) {
        if (!isCurrent(id)) return
        val sink = eventSink
        stopOnMain("failure")
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
        const val TAG = "FlowDropDiscovery"
        const val MULTICAST_LOCK_TAG = "flowdrop-mdns"
        const val RESOLVE_SETTLE_MS = 250L
        const val RESOLVE_RETRY_DELAY_MS = 300L
        const val RESOLVE_TIMEOUT_MS = 2_500L
        const val START_RETRY_DELAY_MS = 400L
        const val MAX_START_RETRIES = 3
    }
}
