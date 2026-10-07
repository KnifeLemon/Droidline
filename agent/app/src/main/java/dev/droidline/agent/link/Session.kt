package dev.droidline.agent.link

import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.util.concurrent.atomic.AtomicInteger

/**
 * State that outlives a single connection: message numbering, the resume buffer and the command cache.
 * Every numbered message goes through the buffer; a per-link pump streams it out in n order.
 */
class Session(
    val buffer: ResumeBuffer = ResumeBuffer(),
    val cache: CommandCache = CommandCache(),
) {
    private val lock = Any()
    private var nextN = 1L
    private var highestAck = 0L
    private val wake = Channel<Unit>(Channel.CONFLATED)

    private val pumps = AtomicInteger(0)

    /** True while some link is streaming; a pump that is still winding down does not clear it for the next one. */
    val online: Boolean get() = pumps.get() > 0

    /** Highest n written to any link, used to flush `accepted` before cutting the network. */
    val written = MutableStateFlow(0L)

    fun send(msg: JSONObject, kind: String? = null): Long {
        val n: Long
        synchronized(lock) {
            n = nextN++
            msg.put("n", n)
            buffer.add(n, msg.toString(), kind)
            if (kind == KIND_NOTIFICATION && !online) buffer.capKind(KIND_NOTIFICATION, MAX_OFFLINE_NOTIFICATIONS)
        }
        wake.trySend(Unit)
        return n
    }

    fun ack(n: Long) {
        synchronized(lock) { if (n > highestAck) highestAck = n }
        buffer.ack(n)
    }

    /**
     * Applies welcome.ack. An ack lower than one already seen means the server lost its state
     * (it restarted), so command ids may repeat and the cache is dropped.
     */
    fun onWelcome(ack: Long) {
        synchronized(lock) {
            if (ack < highestAck) cache.clear()
            highestAck = ack
        }
        buffer.ack(ack)
    }

    /** Clears everything tied to the previous server after pairing with a new one. */
    fun resetForNewServer() {
        synchronized(lock) {
            buffer.clear()
            cache.clear()
            highestAck = 0
        }
    }

    /** Writes everything after [fromAck] and then each new message, until the link closes. */
    suspend fun pump(link: SecureLink, fromAck: Long) {
        var last = fromAck
        pumps.incrementAndGet()
        try {
            while (!link.closed) {
                val pending = buffer.after(last)
                for (e in pending) {
                    link.send(e.line)
                    last = e.n
                    if (e.n > written.value) written.value = e.n
                }
                if (pending.isEmpty()) wake.receive()
            }
        } finally {
            pumps.decrementAndGet()
        }
    }

    suspend fun awaitWritten(n: Long, timeoutMs: Long): Boolean =
        withTimeoutOrNull(timeoutMs) { written.first { it >= n } } != null

    companion object {
        const val KIND_NOTIFICATION = "notification"
        const val MAX_OFFLINE_NOTIFICATIONS = 500
    }
}
