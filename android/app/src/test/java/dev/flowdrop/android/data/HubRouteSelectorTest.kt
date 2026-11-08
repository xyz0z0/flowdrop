package dev.flowdrop.android.data

import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HubRouteSelectorTest {

    private fun lan(url: String) = RouteCandidate(url, RouteCandidate.KIND_LAN)
    private fun tailscale(url: String) = RouteCandidate(url, RouteCandidate.KIND_TAILSCALE)
    private fun other(url: String) = RouteCandidate(url, RouteCandidate.KIND_OTHER)

    @Test
    fun `lan wins over tailscale and other regardless of stored order`() = runTest {
        val probed = mutableListOf<String>()
        val selector = HubRouteSelector(probe = { candidate ->
            probed += candidate.url
            "hub-1"
        })

        val selected = selector.select(
            "hub-1",
            listOf(
                other("http://203.0.113.9:8080"),
                tailscale("http://hub.tailnet.ts.net:8080"),
                lan("http://192.168.1.50:8080"),
            ),
        )

        assertEquals(lan("http://192.168.1.50:8080"), selected)
        assertEquals(listOf("http://192.168.1.50:8080"), probed)
    }

    @Test
    fun `falls back to tailscale when lan is unavailable`() = runTest {
        val probed = mutableListOf<String>()
        val selector = HubRouteSelector(probe = { candidate ->
            probed += candidate.url
            if (candidate.kind == RouteCandidate.KIND_LAN) null else "hub-1"
        })

        val selected = selector.select(
            "hub-1",
            listOf(
                other("http://203.0.113.9:8080"),
                tailscale("http://hub.tailnet.ts.net:8080"),
                lan("http://192.168.1.50:8080"),
            ),
        )

        assertEquals(tailscale("http://hub.tailnet.ts.net:8080"), selected)
        assertEquals(
            listOf("http://192.168.1.50:8080", "http://hub.tailnet.ts.net:8080"),
            probed,
        )
    }

    @Test
    fun `a different hub id is never selected`() = runTest {
        val selector = HubRouteSelector(probe = { candidate ->
            when (candidate.kind) {
                RouteCandidate.KIND_LAN -> "some-other-hub"
                else -> "hub-1"
            }
        })

        val selected = selector.select(
            "hub-1",
            listOf(
                lan("http://192.168.1.50:8080"),
                tailscale("http://hub.tailnet.ts.net:8080"),
            ),
        )

        assertEquals(tailscale("http://hub.tailnet.ts.net:8080"), selected)
    }

    @Test
    fun `no candidate returns null when every hub id differs`() = runTest {
        val selector = HubRouteSelector(probe = { "some-other-hub" })

        assertNull(
            selector.select(
                "hub-1",
                listOf(lan("http://192.168.1.50:8080"), tailscale("http://hub.tailnet.ts.net:8080")),
            ),
        )
    }

    @Test
    fun `no candidate returns null when nothing answers`() = runTest {
        val selector = HubRouteSelector(probe = { null })

        assertNull(selector.select("hub-1", listOf(lan("http://192.168.1.50:8080"))))
    }

    @Test
    fun `duplicate origins are probed once`() = runTest {
        val probed = mutableListOf<String>()
        val selector = HubRouteSelector(probe = { candidate ->
            probed += candidate.url
            "hub-1"
        })

        val selected = selector.select(
            "hub-1",
            listOf(
                lan("http://hub.lan:8080"),
                lan("http://hub.lan:8080/"),
                tailscale("http://hub.lan:8080"),
                tailscale("http://hub.tailnet.ts.net:8080"),
            ),
        )

        assertEquals(lan("http://hub.lan:8080"), selected)
        assertEquals(listOf("http://hub.lan:8080"), probed)
    }

    @Test
    fun `invalid addresses are filtered before probing`() = runTest {
        val probed = mutableListOf<String>()
        val selector = HubRouteSelector(probe = { candidate ->
            probed += candidate.url
            "hub-1"
        })

        val selected = selector.select(
            "hub-1",
            listOf(
                lan("ftp://hub.lan"),
                lan("file:///etc/hostname"),
                lan("http://hub.lan:8080/api"),
                lan("   "),
                lan("not a url at all"),
                lan("http://"),
                tailscale("http://hub.tailnet.ts.net:8080"),
            ),
        )

        assertEquals(tailscale("http://hub.tailnet.ts.net:8080"), selected)
        assertEquals(listOf("http://hub.tailnet.ts.net:8080"), probed)
    }

    @Test
    fun `probe exceptions mark the address unusable instead of failing the selection`() = runTest {
        val selector = HubRouteSelector(probe = { candidate ->
            when (candidate.kind) {
                RouteCandidate.KIND_LAN -> throw IOException("connection refused")
                else -> "hub-1"
            }
        })

        val selected = selector.select(
            "hub-1",
            listOf(
                lan("http://192.168.1.50:8080"),
                tailscale("http://hub.tailnet.ts.net:8080"),
            ),
        )

        assertEquals(tailscale("http://hub.tailnet.ts.net:8080"), selected)
    }

    @Test
    fun `an unresponsive probe times out and the next candidate is used`() = runTest {
        // runTest virtual time: the 60 s delay costs no wall clock.
        val selector = HubRouteSelector(
            probe = { candidate ->
                if (candidate.kind == RouteCandidate.KIND_LAN) {
                    delay(60_000)
                    "hub-1"
                } else {
                    "hub-1"
                }
            },
            timeoutMillis = 1_500L,
        )

        val selected = selector.select(
            "hub-1",
            listOf(
                lan("http://192.168.1.50:8080"),
                tailscale("http://hub.tailnet.ts.net:8080"),
            ),
        )

        assertEquals(tailscale("http://hub.tailnet.ts.net:8080"), selected)
    }

    @Test
    fun `timeout budget is per candidate`() = runTest {
        val selector = HubRouteSelector(probe = { delay(60_000); "hub-1" }, timeoutMillis = 100L)

        assertNull(
            selector.select(
                "hub-1",
                listOf(lan("http://192.168.1.50:8080"), tailscale("http://hub.tailnet.ts.net:8080")),
            ),
        )
    }

    @Test
    fun `caller cancellation propagates out of select`() = runTest {
        lateinit var self: Job
        var thrown: CancellationException? = null
        val selector = HubRouteSelector(
            probe = {
                self.cancel(CancellationException("caller went away"))
                delay(1L)
                "hub-1"
            },
            timeoutMillis = 5_000L,
        )

        val job = launch(start = CoroutineStart.UNDISPATCHED) {
            self = currentCoroutineContext()[Job]!!
            try {
                selector.select("hub-1", listOf(lan("http://192.168.1.50:8080")))
            } catch (cancelled: CancellationException) {
                thrown = cancelled
                throw cancelled
            }
        }
        job.join()

        assertTrue("job must end cancelled", job.isCancelled)
        assertNotNull("select must rethrow cancellation, not return null", thrown)
    }

    @Test
    fun `at most maxCandidates addresses are probed`() = runTest {
        val probed = mutableListOf<String>()
        val selector = HubRouteSelector(
            probe = { candidate ->
                probed += candidate.url
                "some-other-hub"
            },
            maxCandidates = 2,
        )

        val selected = selector.select(
            "hub-1",
            listOf(
                lan("http://10.0.0.1:8080"),
                lan("http://10.0.0.2:8080"),
                lan("http://10.0.0.3:8080"),
            ),
        )

        assertNull(selected)
        assertEquals(listOf("http://10.0.0.1:8080", "http://10.0.0.2:8080"), probed)
        assertEquals(HubRouteSelector.DEFAULT_MAX_CANDIDATES, HubRouteSelector(probe = { null }).plan(
            (1..20).map { lan("http://10.0.0.$it:8080") },
        ).size)
    }

    @Test
    fun `plan returns canonical candidates with contract kinds`() {
        val selector = HubRouteSelector(probe = { null })

        assertEquals(
            listOf(
                RouteCandidate("http://hub.lan", "lan"),
                RouteCandidate("http://hub.tailnet.ts.net:8080", "tailscale"),
                RouteCandidate("http://203.0.113.9:8080", "other"),
            ),
            selector.plan(
                listOf(
                    RouteCandidate("http://203.0.113.9:8080", "wan"),
                    RouteCandidate("http://hub.tailnet.ts.net:8080", "Tailscale"),
                    RouteCandidate("http://Hub.LAN:80", "LAN"),
                ),
            ),
        )
    }

    @Test
    fun `probe receives the canonical candidate`() = runTest {
        val seen = mutableListOf<RouteCandidate>()
        val selector = HubRouteSelector(probe = { candidate ->
            seen += candidate
            if (seen.size == 1) null else "hub-1"
        })

        val selected = selector.select(
            "hub-1",
            listOf(lan("http://Hub.LAN:80"), tailscale("http://hub.tailnet.ts.net:8080")),
        )

        assertEquals(tailscale("http://hub.tailnet.ts.net:8080"), selected)
        assertEquals(
            listOf(
                RouteCandidate("http://hub.lan", "lan"),
                RouteCandidate("http://hub.tailnet.ts.net:8080", "tailscale"),
            ),
            seen,
        )
    }

    @Test
    fun `https addresses are accepted`() = runTest {
        val selector = HubRouteSelector(probe = { "hub-1" })

        assertEquals(
            RouteCandidate("https://hub.example", "lan"),
            selector.select("hub-1", listOf(lan("https://hub.example/"))),
        )
    }
}
