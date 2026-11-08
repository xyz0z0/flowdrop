package dev.flowdrop.android.data

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
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
     * Returns the first healthy candidate whose Hub ID equals [expectedHubId],
     * or `null` when none answers correctly within budget.
     *
     * Returning `null` is "no usable address right now", not "not paired":
     * the caller keeps its stored pairing and may retry later. Credentials are
     * only restored after a non-null result.
     */
    suspend fun select(
        expectedHubId: String,
        candidates: List<RouteCandidate>,
    ): RouteCandidate? {
        for (candidate in plan(candidates)) {
            val reportedHubId = probeOrUnavailable(candidate) ?: continue
            if (reportedHubId == expectedHubId) return candidate
        }
        return null
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
