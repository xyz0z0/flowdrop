package dev.flowdrop.android.data

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/**
 * One stored address for a paired Hub, tagged with the discovery kind that
 * produced it.
 *
 * `kind` follows the frozen hub payload: [KIND_LAN], [KIND_TAILSCALE] or
 * [KIND_OTHER]. [HubRouteSelector] normalises unknown values to
 * [KIND_OTHER] and lower-cases known ones, so the selected candidate always
 * carries a contract-valid kind.
 *
 * [url] is an `http(s)` root address (`http://192.168.1.50:8080`,
 * `https://hub.example`). The selector normalises the winning candidate to its
 * canonical origin, which is exactly the value the app binds to and reports.
 */
data class RouteCandidate(
    val url: String,
    val kind: String,
) {
    companion object {
        const val KIND_LAN = "lan"
        const val KIND_TAILSCALE = "tailscale"
        const val KIND_OTHER = "other"
    }
}

/**
 * Picks the address to connect to out of a paired Hub's saved candidates.
 *
 * Rules (see docs/PAIRING.md): LAN first, then Tailscale, then anything else;
 * equal ranks keep the stored order; invalid addresses and duplicate origins
 * are dropped before probing; a candidate is only selected when a credential-
 * free probe reports the expected Hub ID within a short timeout; failures are
 * skipped instead of retried, and caller cancellation always propagates.
 *
 * The planned addresses are probed concurrently, and the result is the
 * best-ranked candidate that answered, returned as soon as no better-ranked one
 * can still answer. Probing them one after another made a wrong guess cost a
 * full timeout each: leaving the office LAN meant waiting for every dead
 * `192.168.x` address before the Tailscale address was even tried. The probes
 * are credential-free `GET /api/v1/health` calls, so running them at once costs
 * nothing but a few packets, while the preference order is unchanged.
 *
 * This class performs no I/O itself: [probe] owns the actual request, so the
 * integration layer decides transport, cookies and logging. The probe must not
 * send session cookies, CSRF or device tokens: the Hub ID is the only thing it
 * may learn from an unverified peer.
 *
 * @param probe credential-free probe of one candidate; returns the responding
 *   Hub's stable `hub_id`, or `null` when the address is unhealthy. Throwing
 *   an exception other than cancellation means "unavailable" and the selector
 *   moves on to the next candidate.
 * @param timeoutMillis per-candidate timeout, short by design (default 1500 ms).
 * @param maxCandidates upper bound on how many addresses are probed in one
 *   selection, so a long candidate list cannot stretch the wait without bound
 *   (default 8).
 */
class HubRouteSelector(
    private val probe: suspend (RouteCandidate) -> String?,
    private val timeoutMillis: Long = DEFAULT_TIMEOUT_MILLIS,
    private val maxCandidates: Int = DEFAULT_MAX_CANDIDATES,
) {
    init {
        require(timeoutMillis > 0L) { "timeoutMillis must be positive" }
        require(maxCandidates > 0) { "maxCandidates must be positive" }
    }

    /**
     * Returns the best-ranked healthy candidate whose Hub ID equals
     * [expectedHubId], or `null` when none answers correctly within budget.
     *
     * [preferredUrl] is the address that was bound last time. It is probed alone
     * first, because after a cold start or a foreground return it is by far the
     * most likely to still answer: without it the caller waits for the whole
     * candidate set, which on a changed network means one timeout per dead
     * address. A failed preferred probe falls through to the normal plan.
     *
     * The remaining planned addresses are probed at once, and the answer is
     * published as soon as no better-ranked candidate can still reply, so a dead
     * address costs its own timeout only when it is the rank that matters.
     *
     * Returning `null` is "no usable address right now", not "not paired":
     * the caller keeps its stored pairing and may retry later. Credentials are
     * only restored after a non-null result.
     */
    suspend fun select(
        expectedHubId: String,
        candidates: List<RouteCandidate>,
        preferredUrl: String? = null,
    ): RouteCandidate? {
        val planned = plan(candidates).toMutableList()
        if (planned.isEmpty()) return null
        val preferredOrigin = preferredUrl
            ?.takeIf { it.isNotBlank() }
            ?.let { HubEndpoint.parse(it)?.origin }
        val preferred = preferredOrigin?.let { origin -> planned.firstOrNull { it.url == origin } }
        if (preferred != null) {
            if (probeOrUnavailable(preferred) == expectedHubId) return preferred
            // It answered with something else or not at all: do not probe it twice.
            planned.remove(preferred)
            if (planned.isEmpty()) return null
        }
        if (planned.size == 1) {
            val only = planned.single()
            return if (probeOrUnavailable(only) == expectedHubId) only else null
        }
        return coroutineScope {
            // One probe per planned address; [reported] is written before the
            // index is published through [finished], so reading it after a
            // receive is safe across dispatchers.
            val reported = arrayOfNulls<String>(planned.size)
            val done = BooleanArray(planned.size)
            val finished = Channel<Int>(Channel.UNLIMITED)
            val probes = planned.mapIndexed { index, candidate ->
                launch {
                    reported[index] = probeOrUnavailable(candidate)
                    finished.send(index)
                }
            }
            // Abandons the probes that can no longer change the answer. Without
            // this the scope would still wait for the slowest probe, and a dead
            // address would cost its full timeout even after the winner is known.
            fun stopRemainingProbes() = probes.forEach { it.cancel() }
            var best: Int? = null
            repeat(planned.size) {
                val index = finished.receive()
                done[index] = true
                val previousBest = best
                if (reported[index] == expectedHubId && (previousBest == null || index < previousBest)) {
                    best = index
                }
                val winner = best
                // Nothing ranked better may still answer: stop waiting instead of
                // blocking on the slowest probe of a dead address.
                if (winner != null && (0 until winner).all { done[it] }) {
                    stopRemainingProbes()
                    return@coroutineScope planned[winner]
                }
            }
            best?.let { planned[it] }
        }
    }

    /**
     * Orders, validates, normalises and de-duplicates [candidates] before any
     * probe happens. Public so the integration layer can log or show the plan
     * without duplicating the rules.
     */
    fun plan(candidates: List<RouteCandidate>): List<RouteCandidate> {
        val ranked = candidates
            .mapNotNull { candidate -> normalise(candidate) }
            .sortedBy { rankOf(it.kind) }
        val seenOrigins = HashSet<String>(ranked.size)
        val planned = ArrayList<RouteCandidate>(minOf(ranked.size, maxCandidates))
        for (candidate in ranked) {
            if (planned.size >= maxCandidates) break
            if (seenOrigins.add(candidate.url)) planned.add(candidate)
        }
        return planned
    }

    /**
     * Runs [probe] under this selector's timeout. Returns `null` for "not
     * usable" (timeout, exception, non-cancellation failure); cancellation is
     * rethrown so a cancelled caller never keeps probing.
     */
    private suspend fun probeOrUnavailable(candidate: RouteCandidate): String? {
        val reported = try {
            withTimeoutOrNull(timeoutMillis) { probe(candidate) }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        }
        // A probe may swallow its own cancellation; never continue in that case.
        currentCoroutineContext().ensureActive()
        return reported
    }

    /** Canonical candidate for a stored entry, or `null` if the address is unusable. */
    private fun normalise(candidate: RouteCandidate): RouteCandidate? {
        val endpoint = HubEndpoint.parse(candidate.url) ?: return null
        return RouteCandidate(url = endpoint.origin, kind = normaliseKind(candidate.kind))
    }

    private fun normaliseKind(kind: String): String = when (kind.trim().lowercase()) {
        RouteCandidate.KIND_LAN -> RouteCandidate.KIND_LAN
        RouteCandidate.KIND_TAILSCALE -> RouteCandidate.KIND_TAILSCALE
        else -> RouteCandidate.KIND_OTHER
    }

    private fun rankOf(kind: String): Int = when (kind) {
        RouteCandidate.KIND_LAN -> 0
        RouteCandidate.KIND_TAILSCALE -> 1
        else -> 2
    }

    companion object {
        const val DEFAULT_TIMEOUT_MILLIS: Long = 1_500L
        const val DEFAULT_MAX_CANDIDATES: Int = 8
    }
}
