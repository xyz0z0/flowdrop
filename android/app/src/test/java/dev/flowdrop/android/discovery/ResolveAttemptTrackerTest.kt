package dev.flowdrop.android.discovery

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Bounded retry bookkeeping for `NsdManager.resolveService`.
 *
 * The Android driver itself needs a looper and NsdManager, so the decision
 * ("retry this instance, or give up?") is kept as plain JVM logic.
 */
class ResolveAttemptTrackerTest {

    private val name = "FlowDrop-bob-hpelitedesk880g6"

    @Test
    fun `allows at most three attempts per instance`() {
        val tracker = ResolveAttemptTracker()

        assertEquals(1, tracker.begin(name))
        assertTrue(tracker.shouldRetryAfterFailure(name))

        assertEquals(2, tracker.begin(name))
        assertTrue(tracker.shouldRetryAfterFailure(name))

        assertEquals(3, tracker.begin(name))
        assertFalse(tracker.shouldRetryAfterFailure(name))
    }

    @Test
    fun `a successful resolve clears the counter`() {
        val tracker = ResolveAttemptTracker()
        tracker.begin(name)
        tracker.begin(name)

        tracker.clear(name)

        assertEquals(0, tracker.attemptsUsed(name))
        // A fresh failure-free state cannot trigger a retry on its own.
        assertFalse(tracker.shouldRetryAfterFailure(name))
        // A later re-find starts from attempt 1 again.
        assertEquals(1, tracker.begin(name))
    }

    @Test
    fun `stop resets so a late failure does not retry`() {
        val tracker = ResolveAttemptTracker()
        tracker.begin(name)

        tracker.reset()

        assertEquals(0, tracker.attemptsUsed(name))
        assertFalse(tracker.shouldRetryAfterFailure(name))
    }

    @Test
    fun `instances are tracked independently`() {
        val tracker = ResolveAttemptTracker()
        tracker.begin(name)
        tracker.begin(name)
        tracker.begin(name)

        assertFalse(tracker.shouldRetryAfterFailure(name))
        assertEquals(0, tracker.attemptsUsed("FlowDrop-other"))
        tracker.begin("FlowDrop-other")
        assertTrue(tracker.shouldRetryAfterFailure("FlowDrop-other"))
    }
}
