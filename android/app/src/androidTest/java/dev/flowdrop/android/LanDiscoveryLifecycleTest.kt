package dev.flowdrop.android

import android.content.Context
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.flowdrop.android.discovery.DiscoveryEvent
import dev.flowdrop.android.discovery.NsdHubDiscovery
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Platform smoke test for the real NsdManager driver on the emulator.
 *
 * It does not assert that a Hub is found (an emulator usually has no mDNS
 * peer): it asserts that starting discovery reaches a terminal callback
 * (started or a handled failure), and that stop/restart is safe and releases
 * the session. Address filtering and result handling are covered by the JVM
 * tests; the UI tests cover the login-screen interaction.
 */
@RunWith(AndroidJUnit4::class)
class LanDiscoveryLifecycleTest {

    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context: Context get() = instrumentation.targetContext

    private fun awaitTerminal(events: List<DiscoveryEvent>, latch: CountDownLatch): Boolean {
        val terminal = latch.await(10, TimeUnit.SECONDS)
        assertTrue("NSD must report started or failed, got $events", terminal)
        assertTrue(
            "first terminal event must be Started or Failed, got $events",
            events.firstOrNull() is DiscoveryEvent.Started || events.firstOrNull() is DiscoveryEvent.Failed,
        )
        return terminal
    }

    @Test
    fun startThenDoubleStopIsSafe() {
        val events = CopyOnWriteArrayList<DiscoveryEvent>()
        val terminal = CountDownLatch(1)
        lateinit var driver: NsdHubDiscovery

        instrumentation.runOnMainSync {
            driver = NsdHubDiscovery(context)
            driver.start { event ->
                events += event
                if (event is DiscoveryEvent.Started || event is DiscoveryEvent.Failed) {
                    terminal.countDown()
                }
            }
        }

        awaitTerminal(events, terminal)
        instrumentation.runOnMainSync {
            driver.stop()
            driver.stop()
        }
        // A short wait gives any racing platform callback a chance to crash
        // the test if teardown were incorrect; no callback is required here.
        Thread.sleep(300)
    }

    @Test
    fun stopThenRestartIsSafe() {
        val first = CopyOnWriteArrayList<DiscoveryEvent>()
        val firstTerminal = CountDownLatch(1)
        lateinit var driver: NsdHubDiscovery

        instrumentation.runOnMainSync {
            driver = NsdHubDiscovery(context)
            driver.start { event ->
                first += event
                if (event is DiscoveryEvent.Started || event is DiscoveryEvent.Failed) {
                    firstTerminal.countDown()
                }
            }
        }
        awaitTerminal(first, firstTerminal)

        val second = CopyOnWriteArrayList<DiscoveryEvent>()
        val secondTerminal = CountDownLatch(1)
        instrumentation.runOnMainSync {
            driver.stop()
            driver.start { event ->
                second += event
                if (event is DiscoveryEvent.Started || event is DiscoveryEvent.Failed) {
                    secondTerminal.countDown()
                }
            }
        }

        awaitTerminal(second, secondTerminal)
        instrumentation.runOnMainSync { driver.stop() }
        Thread.sleep(300)
    }
}
