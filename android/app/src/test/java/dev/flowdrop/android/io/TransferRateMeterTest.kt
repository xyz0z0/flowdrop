package dev.flowdrop.android.io

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The inbox shows a speed label only when the meter has measured one: a rate
 * must come from bytes over a real elapsed window on a monotonic clock, never
 * from a single progress callback.
 */
class TransferRateMeterTest {

    private var now = 1_000_000_000L

    private fun meter(interval: Long = 500_000_000L) = TransferRateMeter(interval) { now }

    @Test
    fun `the first sample only opens the window`() {
        val meter = meter()
        assertNull(meter.sample(0))
        now += 100_000_000L
        assertNull(meter.sample(1024))
    }

    @Test
    fun `no rate before the interval has elapsed`() {
        val meter = meter()
        assertNull(meter.sample(0))
        now += 499_000_000L
        assertNull(meter.sample(1_000_000))
    }

    @Test
    fun `a full window reports bytes over elapsed time`() {
        val meter = meter()
        assertNull(meter.sample(0))
        now += 500_000_000L
        assertEquals(2_000_000L, meter.sample(1_000_000)!!)
    }

    @Test
    fun `the next window starts at the last sample`() {
        val meter = meter()
        assertNull(meter.sample(0))
        now += 500_000_000L
        assertEquals(2_000_000L, meter.sample(1_000_000)!!)
        // Only half an interval later nothing new is emitted.
        now += 250_000_000L
        assertNull(meter.sample(1_500_000))
        now += 250_000_000L
        assertEquals(2_000_000L, meter.sample(2_000_000)!!)
    }

    @Test
    fun `a stalled window reports zero rather than the old rate`() {
        val meter = meter()
        assertNull(meter.sample(0))
        now += 500_000_000L
        assertEquals(2_000_000L, meter.sample(1_000_000)!!)
        now += 600_000_000L
        assertEquals(0L, meter.sample(1_000_000)!!)
    }

    @Test
    fun `a counter that moves backwards restarts the window`() {
        val meter = meter()
        assertNull(meter.sample(1_000_000))
        now += 500_000_000L
        assertNull(meter.sample(500_000))
        now += 500_000_000L
        assertEquals(1_000_000L, meter.sample(1_000_000)!!)
    }

    @Test
    fun `a clock that does not advance produces no rate`() {
        val meter = meter()
        assertNull(meter.sample(0))
        assertNull(meter.sample(1_000_000))
    }
}
