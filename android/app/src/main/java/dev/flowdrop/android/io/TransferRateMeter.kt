package dev.flowdrop.android.io

/**
 * Turns the cumulative byte counter of a download into a throttled throughput
 * figure.
 *
 * [dev.flowdrop.android.data.ApiClient.download] reports progress on every
 * 64 KiB buffer, which is far too often for a readable speed label and too
 * noisy to compute a rate from a single chunk. This meter keeps a window on a
 * monotonic clock ([clock], never wall time: the user or an NTP update may
 * change the wall clock mid-transfer) and emits a new rate only once
 * [intervalNanos] has really elapsed.
 *
 * Bytes, not timestamps, are the source of truth: the rate is the byte delta
 * over the measured window, so a slow socket does not look fast just because
 * progress callbacks are frequent.
 */
class TransferRateMeter(
    private val intervalNanos: Long = DEFAULT_INTERVAL_NANOS,
    private val clock: () -> Long = System::nanoTime,
) {
    private var started = false
    private var windowStart = 0L
    private var windowBytes = 0L

    /**
     * Feeds the current byte count and returns the new rate in bytes/second,
     * or null while the first window is still open or when the clock did not
     * advance (the caller then keeps the previous value).
     */
    fun sample(loaded: Long): Long? {
        val now = clock()
        if (!started) {
            started = true
            windowStart = now
            windowBytes = loaded
            return null
        }
        // A counter that moves backwards means a new transfer reused the meter;
        // restart the window instead of reporting a negative rate.
        if (loaded < windowBytes) {
            windowStart = now
            windowBytes = loaded
            return null
        }
        val elapsed = now - windowStart
        if (elapsed < intervalNanos || elapsed <= 0L) return null
        val bytes = loaded - windowBytes
        windowStart = now
        windowBytes = loaded
        return (bytes.toDouble() * NANOS_PER_SECOND / elapsed.toDouble()).toLong()
    }

    companion object {
        /**
         * Long enough for a stable figure, short enough that the label feels
         * live while a large APK streams.
         */
        const val DEFAULT_INTERVAL_NANOS = 500_000_000L

        private const val NANOS_PER_SECOND = 1_000_000_000.0
    }
}
