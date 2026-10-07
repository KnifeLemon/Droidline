package dev.droidline.agent.link

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ResumeBufferTest {
    @Test
    fun ackTrimsUpToAndIncludingN() {
        val b = ResumeBuffer()
        for (n in 1L..5L) b.add(n, "m$n")
        b.ack(3)
        assertEquals(listOf(4L, 5L), b.after(0).map { it.n })
        assertEquals(listOf(5L), b.after(4).map { it.n })
        b.ack(10)
        assertEquals(0, b.size)
    }

    @Test
    fun countLimitDropsOldest() {
        val b = ResumeBuffer(maxCount = 3)
        for (n in 1L..5L) b.add(n, "m$n")
        assertEquals(listOf(3L, 4L, 5L), b.after(0).map { it.n })
    }

    @Test
    fun byteLimitDropsOldestButKeepsNewest() {
        val b = ResumeBuffer(maxBytes = 10)
        b.add(1, "aaaa")
        b.add(2, "bbbb")
        assertEquals(8L, b.bytes)
        b.add(3, "cccc")
        assertEquals(listOf(2L, 3L), b.after(0).map { it.n })
        b.add(4, "x".repeat(50))
        assertEquals(listOf(4L), b.after(0).map { it.n })
    }

    @Test
    fun byteCountUsesUtf8() {
        val b = ResumeBuffer()
        b.add(1, "한")
        assertEquals(3L, b.bytes)
    }

    @Test
    fun capKindKeepsNewestOfThatKindOnly() {
        val b = ResumeBuffer()
        b.add(1, "n1", "notification")
        b.add(2, "r", null)
        b.add(3, "n2", "notification")
        b.add(4, "n3", "notification")
        b.capKind("notification", 2)
        assertEquals(listOf(2L, 3L, 4L), b.after(0).map { it.n })
        assertTrue(b.contains(2))
        assertFalse(b.contains(1))
    }
}

class CommandCacheTest {
    @Test
    fun beginMarksRunningThenFinishStoresAnswer() {
        val c = CommandCache()
        assertNull(c.begin(17))
        assertTrue(c.begin(17) is CommandCache.State.Running)
        c.finish(17, JSONObject().put("id", 17).put("ok", true), 42)
        val done = c.begin(17) as CommandCache.State.Done
        assertEquals(42L, done.n)
        assertTrue(JSONObject(done.body).getBoolean("ok"))
    }

    @Test
    fun numericIdsMatchAcrossTypesButNotStrings() {
        val c = CommandCache()
        c.begin(5)
        assertTrue(c.begin(5L) is CommandCache.State.Running)
        assertTrue(c.begin(5.0) is CommandCache.State.Running)
        assertNull(c.begin("5"))
    }

    @Test
    fun keepsOnlyTheLast2000Ids() {
        val c = CommandCache()
        for (i in 1..2001) c.begin(i)
        assertEquals(2000, c.size)
        assertNull(c.get(1))
        assertTrue(c.get(2) is CommandCache.State.Running)
        assertTrue(c.get(2001) is CommandCache.State.Running)
    }
}

class SessionTest {
    @Test
    fun numbersStartAtOneAndAreBuffered() {
        val s = Session()
        assertEquals(1L, s.send(JSONObject().put("event", "screen")))
        val n = s.send(JSONObject().put("id", 3).put("ok", true))
        assertEquals(2L, n)
        assertEquals(2L, JSONObject(s.buffer.after(1).single().line).getLong("n"))
    }

    @Test
    fun lowerWelcomeAckMeansServerRestartedAndDropsCache() {
        val s = Session()
        s.cache.begin(1)
        s.ack(10)
        s.onWelcome(10)
        assertEquals(1, s.cache.size)
        s.onWelcome(0)
        assertEquals(0, s.cache.size)
    }

    @Test
    fun offlineNotificationsAreCappedAt500() {
        val s = Session()
        repeat(510) { s.send(JSONObject().put("event", "notification"), Session.KIND_NOTIFICATION) }
        s.send(JSONObject().put("event", "result"), "result")
        assertEquals(501, s.buffer.size)
        assertEquals(11L, s.buffer.firstN)
    }
}

class LinePacerTest {
    @Test
    fun burstOf150ThenFortyPerSecond() {
        var now = 0L
        var slept = 0L
        val p = LinePacer(nanoTime = { now }, sleepMs = { ms -> slept += ms; now += ms * 1_000_000 })
        repeat(150) { p.acquire() }
        assertEquals(0L, slept)
        repeat(40) { p.acquire() }
        assertTrue("40 more lines take about one second, slept $slept ms", slept in 990..1040)
    }

    @Test
    fun refillsWhileIdle() {
        var now = 0L
        var slept = 0L
        val p = LinePacer(nanoTime = { now }, sleepMs = { ms -> slept += ms; now += ms * 1_000_000 })
        repeat(150) { p.acquire() }
        now += 10_000_000_000L
        repeat(150) { p.acquire() }
        assertEquals(0L, slept)
    }
}
