package dev.droidline.agent.link

import dev.droidline.agent.crypto.SessionKeys
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.LinkedBlockingQueue

class EnvelopeTest {
    @Test
    fun smallBlobIsOneLine() {
        val lines = Envelope.lines(7, "abc")
        assertEquals(listOf("{\"seq\":7,\"blob\":\"abc\"}"), lines)
    }

    @Test
    fun largeBlobSplitsIntoPartsWithSameSeq() {
        val blob = "x".repeat(10)
        val lines = Envelope.lines(3, blob, maxPart = 4)
        assertEquals(3, lines.size)
        val parts = lines.map { JSONObject(it) }
        assertTrue(parts.all { it.getLong("seq") == 3L })
        assertTrue(parts[0].getBoolean("more"))
        assertTrue(parts[1].getBoolean("more"))
        assertFalse(parts[2].has("more"))
        assertEquals(blob, parts.joinToString("") { it.getString("blob") })
    }

    @Test
    fun splitHappensOnlyAbove512KiB() {
        assertEquals(1, Envelope.lines(0, "a".repeat(Envelope.MAX_PART)).size)
        val lines = Envelope.lines(0, "a".repeat(Envelope.MAX_PART + 1))
        assertEquals(2, lines.size)
        assertTrue(lines.all { it.length <= Envelope.MAX_PART + 64 })
    }

    @Test
    fun assemblerJoinsPartsAndAdvancesSeq() {
        val a = Envelope.Assembler()
        val first = Envelope.lines(0, "0123456789", maxPart = 3)
        val out = first.map { a.feed(it) }
        assertEquals(listOf(null, null, null, "0123456789"), out)
        assertEquals(1L, a.expectedSeq)
        assertEquals("next", a.feed(Envelope.lines(1, "next").single()))
    }

    @Test(expected = ProtocolException::class)
    fun assemblerRejectsSkippedSeq() {
        val a = Envelope.Assembler()
        a.feed(Envelope.lines(0, "a").single())
        a.feed(Envelope.lines(2, "b").single())
    }

    @Test(expected = ProtocolException::class)
    fun assemblerRejectsSeqChangeInsideParts() {
        val a = Envelope.Assembler()
        a.feed("{\"seq\":0,\"blob\":\"ab\",\"more\":true}")
        a.feed("{\"seq\":1,\"blob\":\"cd\"}")
    }

    private class Pipe : LineTransport {
        val out = LinkedBlockingQueue<String>()
        var peer: Pipe? = null
        override fun readLine(): String? = out.take().takeIf { it != EOF }
        override fun writeLine(line: String) { peer!!.out.put(line) }
        override fun close() { out.put(EOF) }
        companion object { const val EOF = "\u0000eof" }
    }

    @Test
    fun secureLinkRoundTripsLargeMessagesAcrossParts() {
        val up = ByteArray(32) { 1 }
        val down = ByteArray(32) { 2 }
        val a = Pipe()
        val b = Pipe()
        a.peer = b
        b.peer = a
        val phone = SecureLink(a, SessionKeys(ByteArray(32), up, down, "000000"), Route.LAN, "srv")
        val server = SecureLink(b, SessionKeys(ByteArray(32), down, up, "000000"), Route.LAN, "srv")
        val big = JSONObject().put("data", "z".repeat(900_000)).toString()
        phone.send("{\"event\":\"hello\"}")
        phone.send(big)
        assertTrue("big message must travel in parts", b.out.size >= 3)
        assertEquals("{\"event\":\"hello\"}", server.receive())
        assertEquals(big, server.receive())
        server.send("{\"event\":\"welcome\"}")
        assertEquals("{\"event\":\"welcome\"}", phone.receive())
        a.close()
        assertNull(phone.receive())
    }
}
