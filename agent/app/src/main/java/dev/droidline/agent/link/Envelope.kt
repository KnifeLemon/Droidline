package dev.droidline.agent.link

import org.json.JSONObject

object Envelope {
    const val MAX_PART = 512 * 1024

    /** Lines for one envelope; a blob over [maxPart] characters is split into parts sharing [seq]. */
    fun lines(seq: Long, blob: String, maxPart: Int = MAX_PART): List<String> {
        if (blob.length <= maxPart) return listOf(line(seq, blob, false))
        val out = ArrayList<String>(blob.length / maxPart + 1)
        var i = 0
        while (i < blob.length) {
            val end = minOf(i + maxPart, blob.length)
            out += line(seq, blob.substring(i, end), end < blob.length)
            i = end
        }
        return out
    }

    // Base64url never needs JSON escaping, so the line is built by hand.
    private fun line(seq: Long, part: String, more: Boolean): String =
        if (more) "{\"seq\":$seq,\"blob\":\"$part\",\"more\":true}" else "{\"seq\":$seq,\"blob\":\"$part\"}"

    /** Joins parts back into one blob and enforces that seq advances by exactly one per envelope. */
    class Assembler(private val maxBlob: Int = 48 * 1024 * 1024) {
        var expectedSeq = 0L
            private set
        private val parts = StringBuilder()

        /** Returns the complete blob, or null while more parts are pending. */
        fun feed(line: String): String? {
            val obj = JSONObject(line)
            val seq = obj.getLong("seq")
            if (seq != expectedSeq) throw ProtocolException("seq $seq, expected $expectedSeq")
            val part = obj.getString("blob")
            if (parts.length + part.length > maxBlob) throw ProtocolException("envelope too large")
            parts.append(part)
            if (obj.optBoolean("more", false)) return null
            val blob = parts.toString()
            parts.setLength(0)
            expectedSeq++
            return blob
        }
    }
}

class ProtocolException(message: String) : Exception(message)
