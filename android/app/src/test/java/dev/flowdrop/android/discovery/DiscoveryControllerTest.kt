package dev.flowdrop.android.discovery

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class DiscoveryControllerTest {

    private class FakeDriver : DiscoveryDriver {
        var starts = 0
        var stops = 0
        private var sink: ((DiscoveryEvent) -> Unit)? = null
        private var stoppedSink: ((DiscoveryEvent) -> Unit)? = null

        override fun start(onEvent: (DiscoveryEvent) -> Unit) {
            starts += 1
            sink = onEvent
            stoppedSink = onEvent
        }

        override fun stop() {
            stops += 1
            sink = null
        }

        fun emit(event: DiscoveryEvent) {
            sink?.invoke(event)
        }

        /** Simulates a platform callback that races in after stop(). */
        fun emitLate(event: DiscoveryEvent) {
            stoppedSink?.invoke(event)
        }
    }

    private fun raw(name: String = "FlowDrop-客厅", address: String = "192.168.1.50") = RawService(
        serviceName = name,
        hostAddresses = listOf(address),
        port = 8080,
        txt = mapOf("v" to "1", "scheme" to "http", "path" to "/"),
    )

    private fun controller(
        scope: TestScope,
        driver: FakeDriver,
        states: MutableList<DiscoveryState>,
        timeout: Long = 1_000,
    ) = DiscoveryController(driver, scope, timeoutMillis = timeout) { states += it }

    @Test
    fun `start publishes searching and the auto flag`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)

        controller.start(auto = true)

        assertEquals(DiscoveryStatus.SEARCHING, states.last().status)
        assertTrue(states.last().autoStarted)
        assertTrue(states.last().searching)
        assertEquals(1, driver.starts)
    }

    @Test
    fun `found and lost update the list`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()

        driver.emit(DiscoveryEvent.Started)
        driver.emit(DiscoveryEvent.Found(raw()))

        assertEquals(1, states.last().hubs.size)
        assertEquals("http://192.168.1.50:8080", states.last().hubs.single().baseUrl)

        driver.emit(DiscoveryEvent.Lost("FlowDrop-客厅"))
        assertTrue(states.last().hubs.isEmpty())
    }

    @Test
    fun `incompatible services are ignored`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()

        driver.emit(DiscoveryEvent.Found(raw().copy(txt = mapOf("v" to "2", "scheme" to "http", "path" to "/"))))
        driver.emit(DiscoveryEvent.Found(raw(address = "8.8.8.8")))
        driver.emit(DiscoveryEvent.Found(raw(name = "Printer")))

        assertTrue(states.last().hubs.isEmpty())
        assertEquals(DiscoveryStatus.SEARCHING, states.last().status)
    }

    @Test
    fun `timeout stops discovery and keeps results`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states, timeout = 1_000)
        controller.start()
        driver.emit(DiscoveryEvent.Found(raw()))

        advanceTimeBy(1_001)
        runCurrent()

        assertEquals(DiscoveryStatus.FINISHED, states.last().status)
        assertEquals(1, states.last().hubs.size)
        assertEquals(1, driver.stops)
    }

    @Test
    fun `late events from a stopped session are ignored`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()
        driver.emit(DiscoveryEvent.Found(raw()))
        val stopped = states.last()

        controller.stop()
        val afterStop = states.last()
        driver.emitLate(DiscoveryEvent.Found(raw(name = "FlowDrop-Late", address = "192.168.1.99")))
        driver.emitLate(DiscoveryEvent.Lost("FlowDrop-客厅"))
        driver.emitLate(DiscoveryEvent.Failed("late failure"))

        assertEquals(DiscoveryStatus.FINISHED, states.last().status)
        assertEquals(afterStop, states.last())
        assertEquals(1, states.last().hubs.size)
        assertEquals(stopped.hubs, states.last().hubs)
        advanceTimeBy(2_000)
        runCurrent()
        assertEquals(1, driver.stops)
    }

    @Test
    fun `auto search happens only once but manual search can repeat`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)

        controller.start(auto = true)
        controller.stop()
        controller.start(auto = true)
        assertEquals(1, driver.starts)

        controller.start()
        assertEquals(2, driver.starts)
    }

    @Test
    fun `restart clears old rows and starts a new session`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()
        driver.emit(DiscoveryEvent.Found(raw()))

        controller.restart()

        assertEquals(DiscoveryStatus.SEARCHING, states.last().status)
        assertTrue(states.last().hubs.isEmpty())
        assertEquals(2, driver.starts)
        assertEquals(1, driver.stops)
    }

    @Test
    fun `a start failure becomes an error and still allows retry`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()

        driver.emit(DiscoveryEvent.Failed("权限不足"))

        assertEquals(DiscoveryStatus.ERROR, states.last().status)
        assertEquals("权限不足", states.last().message)
        assertEquals(1, driver.stops)
        assertFalse(states.last().searching)

        controller.start()
        assertEquals(DiscoveryStatus.SEARCHING, states.last().status)
        assertEquals(2, driver.starts)
    }

    @Test
    fun `clear forgets rows and returns to idle`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()
        driver.emit(DiscoveryEvent.Found(raw()))

        controller.clear()

        assertEquals(DiscoveryStatus.IDLE, states.last().status)
        assertTrue(states.last().hubs.isEmpty())
        assertEquals(1, driver.stops)
    }

    @Test
    fun `release stops the driver and ignores later callbacks`() = runTest {
        val driver = FakeDriver()
        val states = mutableListOf<DiscoveryState>()
        val controller = controller(this, driver, states)
        controller.start()

        controller.release()
        driver.emitLate(DiscoveryEvent.Found(raw()))
        advanceTimeBy(2_000)
        runCurrent()

        assertEquals(1, driver.stops)
        assertEquals(DiscoveryStatus.SEARCHING, states.last().status)
        assertTrue(states.last().hubs.isEmpty())
    }
}
