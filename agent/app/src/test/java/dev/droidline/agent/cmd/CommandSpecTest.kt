package dev.droidline.agent.cmd

import dev.droidline.agent.TestFiles
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CommandSpecTest {
    private val spec = CommandSpec(JSONObject(TestFiles.asset("commands.json").readText(Charsets.UTF_8)))

    @Test
    fun deviceCommandsAreKnownAndServerOnesAreNot() {
        for (name in listOf("touch", "dump", "batch", "chrome.go", "proxy", "notify_filter", "clipboard")) {
            assertTrue(name, spec.deviceCommand(name) != null)
        }
        for (name in listOf("wait_notification", "on_notification", "pair", "devices")) {
            assertNull(name, spec.deviceCommand(name))
        }
    }

    @Test
    fun listStepsMapOntoParameterOrderAndFillDefaults() {
        val s = spec.normalize(JSONArray().put("touch").put("id").put("login"), inBatch = true)
        assertEquals("touch", s.getString("cmd"))
        assertEquals("id", s.getString("by"))
        assertEquals("login", s.getString("value"))
        assertEquals(0, s.getInt("nth"))
        assertEquals(10, s.getInt("timeout"))
        assertFalse(s.has("args"))
    }

    @Test
    fun clientOnlyParamsAreSkippedInPositions() {
        val s = spec.normalize(JSONArray().put("dump").put(true), inBatch = true)
        assertTrue(s.getBoolean("all_windows"))
        assertFalse(s.has("path"))
    }

    @Test
    fun sleepIsOnlyKnownInsideBatch() {
        val s = spec.normalize(JSONArray().put("sleep").put(3000), inBatch = true)
        assertEquals(3000, s.getInt("ms"))
        val outside = spec.normalize(JSONArray().put("sleep").put(3000), inBatch = false)
        assertFalse(outside.has("ms"))
    }

    @Test
    fun networkCuttingFollowsCutsNetwork() {
        fun cmd(name: String, on: Boolean) = JSONObject().put("cmd", name).put("on", on)
        assertTrue(spec.cutsNetwork(cmd("data", false)))
        assertFalse(spec.cutsNetwork(cmd("data", true)))
        assertTrue(spec.cutsNetwork(cmd("wifi", false)))
        assertTrue(spec.cutsNetwork(cmd("airplane", true)))
        assertFalse(spec.cutsNetwork(cmd("airplane", false)))
        assertFalse(spec.cutsNetwork(JSONObject().put("cmd", "touch")))
        val batch = JSONObject().put("cmd", "batch").put("steps", JSONArray()
            .put(JSONObject().put("cmd", "airplane").put("on", true))
            .put(JSONArray().put("sleep").put(3000))
            .put(JSONArray().put("airplane").put(false)))
        assertTrue(spec.cutsNetwork(batch))
        val harmless = JSONObject().put("cmd", "batch").put("steps", JSONArray().put(JSONArray().put("home")))
        assertFalse(spec.cutsNetwork(harmless))
    }

    @Test
    fun missingRequiredParamIsReported() {
        assertEquals("value", spec.missingRequired(JSONObject().put("cmd", "touch").put("by", "text")))
        assertNull(spec.missingRequired(JSONObject().put("cmd", "proxy").put("url", JSONObject.NULL)))
        assertEquals("ms", spec.missingRequired(JSONObject().put("cmd", "sleep")))
    }

    @Test
    fun englishMessagesRenderFromTemplates() {
        assertEquals(
            "Could not find text '아이디' within 10s. Current screen: com.kakao.talk/.LoginActivity",
            spec.render("NOT_FOUND", mapOf("target" to "text '아이디'", "timeout" to 10, "screen" to "com.kakao.talk/.LoginActivity")),
        )
        assertEquals("Unknown command foo. Agent version 0.1.0", spec.render("UNKNOWN_CMD", mapOf("cmd" to "foo", "agent" to "0.1.0")))
        assertEquals(
            "Package com.x is not installed. Similar: com.y, com.z",
            spec.render("APP_NOT_FOUND", mapOf("package" to "com.x", "similar" to JSONArray().put("com.y").put("com.z"))),
        )
        assertTrue(spec.retryable("NOT_FOUND"))
        assertFalse(spec.retryable("BAD_ARGS"))
    }
}
