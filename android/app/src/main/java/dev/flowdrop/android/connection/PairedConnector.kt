package dev.flowdrop.android.connection

import dev.flowdrop.android.data.RouteCandidate
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.async
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/** The part of the stored pairing this controller needs: one Hub, its addresses. */
data class SavedPairing(
    val hubId: String,
    val candidates: List<RouteCandidate>,
)

/** What asked for a (re)connection. Only automatic triggers are rate limited. */
enum class ConnectTrigger {
    STARTUP,
    FOREGROUND,
    MANUAL,
    STREAM_FAILED,
    NETWORK_LOST,
    ;

    /** Failure-driven triggers may fire in bursts, so they are throttled. */
    val automatic: Boolean get() = this == STREAM_FAILED || this == NETWORK_LOST
}

/** Outcome of one selection round. Credentials are only touched on [Bound]. */
sealed interface ConnectResult {
    /** A verified address of the paired Hub is bound and its session restored. */
    data class Bound(val candidate: RouteCandidate) : ConnectResult

    /** Nothing is paired: show the discovery/pair screen. */
    data object NotPaired : ConnectResult

    /**
     * The pairing exists but no address answered with the expected Hub ID, or
     * the restore failed for a temporary reason. Credentials are untouched.
     */
    data object NoRoute : ConnectResult

    /** The Hub answered 200/authenticated=false for our device token: revoked. */
    data object PairingRejected : ConnectResult

    /** Background, throttled, superseded, or already coalesced into another round. */
    data object Deferred : ConnectResult
}

/**
 * Single-flight controller for "reconnect the paired Hub".
 *
 * Responsibilities are deliberately narrow so the ViewModel does not grow an
 * untestable loop:
 * - at most one selection round at a time (foreground + socket failure + manual
 *   retry coalesce into the single in-flight round instead of racing);
 * - failure-driven triggers are throttled by [automaticRetryIntervalMillis];
 * - background stops/ignores rounds, and a round that has been superseded never
 *   publishes a result;
 * - [pick] and [bind] are the only effects, so a reselect can never replay a
 *   write (upload/delete/text): there is no hook through which it could.
 *
 * All I/O lives in the injected lambdas, which keeps this class pure enough for
 * plain JVM tests.
 */
class PairedConnector(
    private val scope: CoroutineScope,
    private val pairing: () -> SavedPairing?,
    private val pick: suspend (SavedPairing) -> RouteCandidate?,
    private val bind: suspend (RouteCandidate) -> ConnectResult,
    private val isForeground: () -> Boolean,
    private val now: () -> Long = System::currentTimeMillis,
    private val automaticRetryIntervalMillis: Long = DEFAULT_RETRY_INTERVAL_MILLIS,
) {
    private val lock = Any()
    private var inFlight: Deferred<ConnectResult>? = null
    private var lastAutomaticAttemptAt: Long? = null

    /** Bumped by every round and by [cancel]; late rounds compare against it. */
    @Volatile
    var generation: Int = 0
        private set

    suspend fun connect(trigger: ConnectTrigger): ConnectResult {
        val deferred = synchronized(lock) {
            inFlight?.let { return@synchronized it }
            val saved = pairing()
            if (saved == null || saved.hubId.isBlank() || saved.candidates.isEmpty()) {
                return ConnectResult.NotPaired
            }
            if (!isForeground()) return ConnectResult.Deferred
            if (trigger.automatic) {
                val previous = lastAutomaticAttemptAt
                if (previous != null && now() - previous < automaticRetryIntervalMillis) {
                    return ConnectResult.Deferred
                }
                lastAutomaticAttemptAt = now()
            }
            generation++
            val round = scope.async(start = CoroutineStart.LAZY) { runRound(generation, saved) }
            inFlight = round
            round.invokeOnCompletion {
                synchronized(lock) { if (inFlight === round) inFlight = null }
            }
            round
        }
        deferred.start()
        val result = try {
            deferred.await()
        } catch (cancelled: CancellationException) {
            // The round itself was cancelled (background/superseded); the caller
            // is still alive, so report "deferred" instead of failing it.
            currentCoroutineContext().ensureActive()
            ConnectResult.Deferred
        }
        if (result is ConnectResult.Bound) {
            synchronized(lock) { lastAutomaticAttemptAt = null }
        }
        return result
    }

    /** Stops the current round and discards its result. Called on background. */
    fun cancel() {
        val stale = synchronized(lock) {
            generation++
            inFlight.also { inFlight = null }
        }
        stale?.cancel()
    }

    private suspend fun runRound(gen: Int, saved: SavedPairing): ConnectResult {
        val candidate = try {
            pick(saved)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        }
        if (gen != generation) return ConnectResult.Deferred
        if (candidate == null) return ConnectResult.NoRoute
        val result = try {
            bind(candidate)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            ConnectResult.NoRoute
        }
        if (gen != generation) return ConnectResult.Deferred
        return result
    }

    companion object {
        /** Long enough to swallow a burst of socket failures, short enough to recover. */
        const val DEFAULT_RETRY_INTERVAL_MILLIS: Long = 3_000L
    }
}
