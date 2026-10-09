package dev.droidline.agent.a11y

import dev.droidline.agent.TestFiles
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test

/** Runs spec/query-vectors.json, the cases the Go matcher in spec/query.go also passes. */
class QueryVectorsTest {
    private val vectors = JSONObject(TestFiles.spec("query-vectors.json").readText(Charsets.UTF_8))

    private fun node(o: JSONObject): UiNode {
        val b = o.optJSONArray("bounds") ?: JSONArray("[0,0,0,0]")
        val kids = o.optJSONArray("children") ?: JSONArray()
        return UiNode(
            text = o.optString("text"),
            id = o.optString("id"),
            desc = o.optString("desc"),
            cls = o.optString("class"),
            pkg = o.optString("package"),
            bounds = Bounds(b.getInt(0), b.getInt(1), b.getInt(2), b.getInt(3)),
            clickable = o.optBoolean("clickable"),
            checkable = o.optBoolean("checkable"),
            checked = o.optBoolean("checked"),
            enabled = o.optBoolean("enabled", true),
            scrollable = o.optBoolean("scrollable"),
            editable = o.optBoolean("editable"),
            visible = o.optBoolean("visible", true),
            children = (0 until kids.length()).map { node(kids.getJSONObject(it)) },
        )
    }

    @Test
    fun everyCaseMatchesLikeTheServer() {
        val roots = listOf(node(vectors.getJSONObject("tree")))
        val cases = vectors.getJSONArray("cases")
        for (i in 0 until cases.length()) {
            val c = cases.getJSONObject(i)
            val target: Target = if (c.has("query")) Query.parse(c.getJSONObject("query")) else Selector(c.getString("by"), c.getString("value"))
            val got = Matcher.findAll(roots, target).map { listOf(it.bounds.l, it.bounds.t, it.bounds.r, it.bounds.b) }
            val exp = c.getJSONArray("expect")
            val want = (0 until exp.length()).map { j -> exp.getJSONArray(j).let { a -> (0 until 4).map { a.getInt(it) } } }
            assertEquals(c.getString("name"), want, got)
        }
    }

    @Test
    fun invalidQueriesAreRejected() {
        val bad = vectors.getJSONArray("invalid")
        for (i in 0 until bad.length()) {
            try {
                Query.parse(bad.get(i))
                fail("accepted ${bad.get(i)}")
            } catch (_: IllegalArgumentException) {
            }
        }
    }

    @Test
    fun badRegexIsRejected() {
        try {
            Query.parse(JSONObject("""{"textMatches":"(unclosed"}"""))
            fail("accepted a broken regex")
        } catch (_: IllegalArgumentException) {
        }
    }
}
