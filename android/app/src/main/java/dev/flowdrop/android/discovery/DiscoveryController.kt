package dev.flowdrop.android.discovery

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

enum class DiscoveryStatus { IDLE, SEARCHING, FINISHED, ERROR }

/**
 * What the login screen shows about nearby-Hub discovery.
 *
 * [hubs] are untrusted address hints. [message] carries a diagnostic reason
 * when discovery itself failed; it never contains credentials.
 */
data class DiscoveryState(
    val status: DiscoveryStatus = DiscoveryStatus.IDLE,
    val hubs: List<DiscoveredHub> = emptyList(),
    val message: String? = null,
    val autoStarted: Boolean = false,
) {
    val searching: Boolean get() = status == DiscoveryStatus.SEARCHING
}

/** Events emitted by a discovery driver (Android NSD in production). */
sealed interface DiscoveryEvent {
    data object Started : DiscoveryEvent
    data class Found(val raw: RawService) : DiscoveryEvent
    data class Lost(val serviceName: String) : DiscoveryEvent
    data class Failed(val reason: String?) : DiscoveryEvent
}

/**
 * Thin abstraction over NsdManager so the discovery lifecycle can be tested
 * with a fake driver. Implementations must call the listener at most until
 * [stop] returns and never after it has been stopped.
 */
interface DiscoveryDriver {
    fun start(onEvent: (DiscoveryEvent) -> Unit)
    fun stop()
}

/**
 * Foreground-only discovery session with a hard timeout.
 *
 * Lifecycle rules:
 * - explicit search starts a session; only the first automatic start is
 *   allowed per controller lifetime (entering the login screen once);
 * - timeout, leaving the login screen, or backgrounding calls [stop], which
 *   stops the driver, releases its platform resources and keeps results;
 * - late events from a stopped session are ignored (generation check);
 * - every start clears previous results, so a refresh cannot mix old rows
 *   with a new network state.
 */
class DiscoveryController(
    private val driver: DiscoveryDriver,
    private val scope: CoroutineScope,
    private val timeoutMillis: Long = DEFAULT_TIMEOUT_MS,
    private val onState: (DiscoveryState) -> Unit,
) {
    private val registry = HubDiscoveryRegistry()
    private var state = DiscoveryState()
    private var timeoutJob: Job? = null
    private var generation = 0
    private var active = false
    private var autoStarted = false

    fun current(): DiscoveryState = state

    /** Starts a search; [auto] is the one-time login-screen auto search. */
    fun start(auto: Boolean = false) {
        if (active) return
        if (auto) {
            if (autoStarted) return
            autoStarted = true
        }
        active = true
        generation += 1
        val runGeneration = generation
        registry.clear()
        publish(
            DiscoveryState(
                status = DiscoveryStatus.SEARCHING,
                autoStarted = autoStarted,
            ),
        )
        timeoutJob = scope.launch {
            delay(timeoutMillis)
            if (runGeneration == generation && active) {
                endSearch(DiscoveryStatus.FINISHED, message = null)
            }
        }
        driver.start { event ->
            if (runGeneration == generation && active) handle(event)
        }
    }

    /** Stops an active search, keeps the rows found so far. */
    fun stop() {
        if (!active) return
        endSearch(DiscoveryStatus.FINISHED, message = null)
    }

    /** Stops and immediately starts over (the UI "刷新" action). */
    fun restart() {
        stop()
        start()
    }

    /** Stops and forgets everything (leaving the login screen). */
    fun clear() {
        stop()
        registry.clear()
        publish(DiscoveryState(status = DiscoveryStatus.IDLE, autoStarted = autoStarted))
    }

    /** Called by the ViewModel when it is destroyed. */
    fun release() {
        if (active) {
            active = false
            generation += 1
            timeoutJob?.cancel()
            timeoutJob = null
            driver.stop()
        }
        registry.clear()
    }

    private fun handle(event: DiscoveryEvent) {
        when (event) {
            DiscoveryEvent.Started -> publish(state.copy(status = DiscoveryStatus.SEARCHING, message = null))
            is DiscoveryEvent.Found -> {
                registry.found(event.raw)
                publish(state.copy(hubs = registry.hubs(), message = null))
            }
            is DiscoveryEvent.Lost -> {
                registry.lost(event.serviceName)
                publish(state.copy(hubs = registry.hubs()))
            }
            is DiscoveryEvent.Failed -> endSearch(DiscoveryStatus.ERROR, event.reason)
        }
    }

    private fun endSearch(status: DiscoveryStatus, message: String?) {
        if (!active) return
        active = false
        generation += 1
        timeoutJob?.cancel()
        timeoutJob = null
        driver.stop()
        publish(
            state.copy(
                status = status,
                hubs = registry.hubs(),
                message = message,
            ),
        )
    }

    private fun publish(next: DiscoveryState) {
        state = next
        onState(next)
    }

    companion object {
        const val DEFAULT_TIMEOUT_MS: Long = 12_000L
    }
}
