package dev.droidline.agent

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Test

/** The copies inside the agent must match ../spec whenever the monorepo is checked out around it. */
class SpecDriftTest {
    /** Key-sorted rendering, so formatting and key order do not count as drift. */
    private fun canon(v: Any?): String = when (v) {
        is JSONObject -> v.keys().asSequence().sorted().joinToString(",", "{", "}") { JSONObject.quote(it) + ":" + canon(v.get(it)) }
        is JSONArray -> (0 until v.length()).joinToString(",", "[", "]") { canon(v.get(it)) }
        is String -> JSONObject.quote(v)
        null, JSONObject.NULL -> "null"
        is Number -> if (v.toDouble() == Math.floor(v.toDouble())) v.toLong().toString() else v.toString()
        else -> v.toString()
    }

    private fun same(copy: String, specName: String) {
        val spec = TestFiles.spec(specName)
        assumeTrue("spec/$specName not present", spec.isFile)
        assertEquals(
            "$specName drifted from spec; copy it again",
            canon(JSONObject(spec.readText(Charsets.UTF_8))),
            canon(JSONObject(copy)),
        )
    }

    @Test
    fun testVectorsMatchSpec() = same(TestFiles.resource("test-vectors.json"), "test-vectors.json")

    @Test
    fun commandsJsonMatchesSpec() = same(TestFiles.asset("commands.json").readText(Charsets.UTF_8), "commands.json")
}
