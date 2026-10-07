package dev.droidline.agent.link

/**
 * Relay pacing (PROTOCOL 4.8): [burst] lines at once, then [perSecond] per second, under the relay's 50/s limit.
 * Every frame counts, including the parts of a split envelope.
 */
class LinePacer(
    private val burst: Int = 150,
    private val perSecond: Int = 40,
    private val nanoTime: () -> Long = System::nanoTime,
    private val sleepMs: (Long) -> Unit = { Thread.sleep(it) },
) {
    private var tokens = burst.toDouble()
    private var last = nanoTime()

    @Synchronized
    fun acquire() {
        refill()
        if (tokens < 1.0) {
            val waitMs = Math.ceil((1.0 - tokens) * 1000.0 / perSecond).toLong().coerceAtLeast(1)
            sleepMs(waitMs)
            refill()
        }
        tokens -= 1.0
    }

    private fun refill() {
        val now = nanoTime()
        tokens = minOf(burst.toDouble(), tokens + (now - last) / 1e9 * perSecond)
        last = now
    }
}
