package dev.flowdrop.android.connection

import dev.flowdrop.android.data.HubRouteSelector
import dev.flowdrop.android.data.RouteCandidate
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pins the reconnect control flow that the ViewModel delegates to
 * [PairedConnector]:
 *
 * - startup/foreground/manual triggers run one selection round and bind the
 *   address the selector picked (LAN first, Tailscale as the fallback);
 * - concurrent triggers coalesce instead of opening parallel rounds/links;
 * - failure-driven triggers are throttled;
 * - an unreachable Hub keeps the pairing (the round only reads it);
 * - a reselect never replays a write: the controller has exactly two effects,
 *   `pick` and `bind`, and the recorded log proves nothing else ran;
 * - background/cancel abandons a round and its late result.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PairedConnectorTest {

    private val lan = RouteCandidate("http://192.168.1.50:8080", RouteCandidate.KIND_LAN)
    private val tailscale = RouteCandidate("http://hub.tailnet.ts.net:8080", RouteCandidate.KIND_TAILSCALE)
    private val saved = SavedPairing("hub-1", listOf(lan, tailscale))

    private fun connector(
        scope: kotlinx.coroutines.CoroutineScope,
        effects: MutableList<String>,
        pairing: () -> SavedPairing? = { saved },
        foreground: () -> Boolean = { true },
        now: () -> Long = { 0L },
        pick: suspend (SavedPairing) -> RouteCandidate? = { effects += "pick"; lan },
        bind: suspend (RouteCandidate) -> ConnectResult = { effects += "bind:${it.kind}"; ConnectResult.Bound(it) },
    ) = PairedConnector(
        scope = scope,
        pairing = pairing,
        pick = pick,
        bind = bind,
        isForeground = foreground,
        now = now,
    )

    @Test
    fun `startup binds the selected address and never writes`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(backgroundScope, effects)

        val result = controller.connect(ConnectTrigger.STARTUP)

        assertEquals(ConnectResult.Bound(lan), result)
        // Exactly one pick and one bind: nothing is replayed or written.
        assertEquals(listOf("pick", "bind:lan"), effects)
    }

    @Test
    fun `tailscale is used when no lan address answers`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pick = { effects += "pick"; tailscale },
        )

        val result = controller.connect(ConnectTrigger.STARTUP)

        assertEquals(ConnectResult.Bound(tailscale), result)
        assertEquals(listOf("pick", "bind:tailscale"), effects)
    }

    @Test
    fun `foreground trigger coalesces into an in-flight round`() = runTest {
        val gate = CompletableDeferred<Unit>()
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pick = { effects += "pick"; gate.await(); lan },
        )

        val startup = async { controller.connect(ConnectTrigger.STARTUP) }
        runCurrent()
        val foreground = async { controller.connect(ConnectTrigger.FOREGROUND) }
        runCurrent()
        gate.complete(Unit)

        assertEquals(ConnectResult.Bound(lan), startup.await())
        assertEquals(ConnectResult.Bound(lan), foreground.await())
        assertEquals(listOf("pick", "bind:lan"), effects)
    }

    @Test
    fun `offline hub keeps the pairing and does not clear anything`() = runTest {
        var pairingReads = 0
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pairing = { pairingReads++; saved },
            pick = { effects += "pick"; null },
        )

        val result = controller.connect(ConnectTrigger.STARTUP)

        assertEquals(ConnectResult.NoRoute, result)
        // Only the read happened: no "clear"/"deactivate" hook exists at all, so
        // an unreachable Hub cannot drop the stored pairing.
        assertEquals(listOf("pick"), effects)
        assertTrue(pairingReads >= 1)
    }

    @Test
    fun `unpaired device is reported without probing`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(backgroundScope, effects, pairing = { null })

        assertEquals(ConnectResult.NotPaired, controller.connect(ConnectTrigger.STARTUP))
        assertEquals(emptyList<String>(), effects)
    }

    @Test
    fun `real selector falls back from lan to tailscale`() = runTest {
        val probed = mutableListOf<String>()
        val selector = HubRouteSelector(probe = { candidate ->
            probed += candidate.url
            if (candidate.kind == RouteCandidate.KIND_TAILSCALE) "hub-1" else null
        })
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pick = { saved -> effects += "pick"; selector.select(saved.hubId, saved.candidates) },
        )

        assertEquals(ConnectResult.Bound(tailscale), controller.connect(ConnectTrigger.STARTUP))
        assertEquals(listOf(lan.url, tailscale.url), probed)
        assertEquals(listOf("pick", "bind:tailscale"), effects)
    }

    @Test
    fun `failure-driven triggers are throttled and can retry later`() = runTest {
        var clock = 0L
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            now = { clock },
            pick = { effects += "pick"; null },
        )

        assertEquals(ConnectResult.NoRoute, controller.connect(ConnectTrigger.STREAM_FAILED))
        clock = 1_000L
        assertEquals(ConnectResult.Deferred, controller.connect(ConnectTrigger.STREAM_FAILED))
        clock = PairedConnector.DEFAULT_RETRY_INTERVAL_MILLIS + 1_000L
        assertEquals(ConnectResult.NoRoute, controller.connect(ConnectTrigger.STREAM_FAILED))
        // Two rounds ran, the throttled attempt did not probe.
        assertEquals(listOf("pick", "pick"), effects)
    }

    @Test
    fun `manual retry bypasses the automatic throttle`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            now = { 5L },
            pick = { effects += "pick"; null },
        )

        assertEquals(ConnectResult.NoRoute, controller.connect(ConnectTrigger.NETWORK_LOST))
        assertEquals(ConnectResult.NoRoute, controller.connect(ConnectTrigger.MANUAL))
        // A foreground return must reselect immediately (LAN first at home), not
        // wait out the failure throttle.
        assertEquals(ConnectResult.NoRoute, controller.connect(ConnectTrigger.FOREGROUND))
        assertEquals(listOf("pick", "pick", "pick"), effects)
    }

    @Test
    fun `background never probes`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(backgroundScope, effects, foreground = { false })

        assertEquals(ConnectResult.Deferred, controller.connect(ConnectTrigger.NETWORK_LOST))
        assertEquals(ConnectResult.Deferred, controller.connect(ConnectTrigger.MANUAL))
        assertEquals(emptyList<String>(), effects)
    }

    @Test
    fun `cancel abandons the round and its late result`() = runTest {
        val gate = CompletableDeferred<Unit>()
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pick = { effects += "pick"; gate.await(); lan },
        )

        val pending = async { controller.connect(ConnectTrigger.STARTUP) }
        runCurrent()
        controller.cancel()
        gate.complete(Unit)

        assertEquals(ConnectResult.Deferred, pending.await())
        assertFalse(effects.any { it.startsWith("bind") })
        assertEquals(listOf("pick"), effects)
    }

    @Test
    fun `cancel then foreground starts a fresh selection`() = runTest {
        val gate = CompletableDeferred<Unit>()
        var foreground = true
        var picks = 0
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            foreground = { foreground },
            pick = {
                picks++
                effects += "pick"
                if (picks == 1) gate.await()
                lan
            },
        )

        // Boot round, interrupted by background.
        val boot = async { controller.connect(ConnectTrigger.STARTUP) }
        runCurrent()
        foreground = false
        controller.cancel()
        gate.complete(Unit)
        assertEquals(ConnectResult.Deferred, boot.await())

        // Returning to the foreground must be able to restore, not stay stuck.
        foreground = true
        assertEquals(ConnectResult.Bound(lan), controller.connect(ConnectTrigger.FOREGROUND))
        assertEquals(listOf("pick", "pick", "bind:lan"), effects)
    }

    @Test
    fun `cancel during bind publishes no late binding`() = runTest {
        val bindGate = CompletableDeferred<Unit>()
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            bind = {
                effects += "bind:${it.kind}"
                bindGate.await()
                ConnectResult.Bound(it)
            },
        )

        val pending = async { controller.connect(ConnectTrigger.STARTUP) }
        runCurrent()
        controller.cancel()
        bindGate.complete(Unit)

        assertEquals(ConnectResult.Deferred, pending.await())
        assertEquals(listOf("pick", "bind:lan"), effects)
    }

    @Test
    fun `a superseded round cannot publish over a newer selection`() = runTest {
        val firstBindGate = CompletableDeferred<Unit>()
        var picks = 0
        var binds = 0
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pick = {
                picks++
                effects += "pick"
                if (picks == 1) lan else tailscale
            },
            bind = {
                binds++
                effects += "bind:${it.kind}"
                if (binds == 1) firstBindGate.await()
                ConnectResult.Bound(it)
            },
        )

        val stale = async { controller.connect(ConnectTrigger.STARTUP) }
        runCurrent()
        controller.cancel()
        val fresh = async { controller.connect(ConnectTrigger.FOREGROUND) }
        runCurrent()
        assertEquals(ConnectResult.Bound(tailscale), fresh.await())
        firstBindGate.complete(Unit)

        assertEquals(ConnectResult.Deferred, stale.await())
        assertEquals(listOf("pick", "bind:lan", "pick", "bind:tailscale"), effects)
    }

    @Test
    fun `pairing rejection is surfaced without clearing the pairing`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            bind = { effects += "bind:${it.kind}"; ConnectResult.PairingRejected },
        )

        assertEquals(ConnectResult.PairingRejected, controller.connect(ConnectTrigger.STARTUP))
        // Clearing the revoked credentials is the caller's decision; the
        // controller only reports what the Hub said.
        assertEquals(listOf("pick", "bind:lan"), effects)
    }

    @Test
    fun `a throwing pick counts as no route and is not retried`() = runTest {
        val effects = mutableListOf<String>()
        val controller = connector(
            backgroundScope,
            effects,
            pick = { effects += "pick"; throw IllegalStateException("probe crashed") },
        )

        assertEquals(ConnectResult.NoRoute, controller.connect(ConnectTrigger.STARTUP))
        assertEquals(listOf("pick"), effects)
    }
}
