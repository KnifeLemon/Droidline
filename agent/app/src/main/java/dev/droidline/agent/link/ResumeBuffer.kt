package dev.droidline.agent.link

/** Outbound messages kept until the server acknowledges their n (PROTOCOL 4.8). */
class ResumeBuffer(
    private val maxCount: Int = 2000,
    private val maxBytes: Long = 8L * 1024 * 1024,
) {
    class Entry(val n: Long, val line: String, val kind: String?) {
        val bytes: Int = line.toByteArray(Charsets.UTF_8).size
    }

    private val entries = ArrayDeque<Entry>()
    private var totalBytes = 0L

    @Synchronized
    fun add(n: Long, line: String, kind: String? = null) {
        val e = Entry(n, line, kind)
        entries.addLast(e)
        totalBytes += e.bytes
        // The newest entry always stays, even when it alone is over the byte limit.
        while (entries.size > 1 && (entries.size > maxCount || totalBytes > maxBytes)) dropFirst()
    }

    @Synchronized
    fun ack(n: Long) {
        while (entries.isNotEmpty() && entries.first().n <= n) dropFirst()
    }

    @Synchronized
    fun after(n: Long): List<Entry> = entries.filter { it.n > n }

    @Synchronized
    fun contains(n: Long): Boolean = entries.any { it.n == n }

    /** Keeps only the newest [keep] entries of [kind], used to cap notifications queued while offline. */
    @Synchronized
    fun capKind(kind: String, keep: Int) {
        var excess = entries.count { it.kind == kind } - keep
        if (excess <= 0) return
        val it = entries.iterator()
        while (excess > 0 && it.hasNext()) {
            val e = it.next()
            if (e.kind == kind) {
                it.remove()
                totalBytes -= e.bytes
                excess--
            }
        }
    }

    @Synchronized
    fun clear() {
        entries.clear()
        totalBytes = 0
    }

    val size: Int @Synchronized get() = entries.size
    val bytes: Long @Synchronized get() = totalBytes
    val firstN: Long? @Synchronized get() = entries.firstOrNull()?.n

    private fun dropFirst() {
        totalBytes -= entries.removeFirst().bytes
    }
}
