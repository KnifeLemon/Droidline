package dev.droidline.agent.a11y

import org.json.JSONArray
import org.json.JSONObject
import kotlin.math.hypot

/**
 * A selector written as an object: several conditions on one element, all of which must hold.
 * spec/query.go implements the same rules for the server; spec/query-vectors.json keeps them in step.
 */
class Query private constructor(private val source: JSONObject, private val conds: List<Cond>) : Target {

    private sealed interface Cond
    private class Field(val key: String, val value: String, val regex: Regex?) : Cond
    private class Flag(val key: String, val value: Boolean) : Cond
    private class Exact(val bounds: Bounds) : Cond
    private class Relation(val key: String, val sub: Query) : Cond

    override fun findAll(roots: List<UiNode>): List<UiNode> {
        val anchors = HashMap<String, UiNode>()
        for (c in conds) {
            if (c is Relation && c.key in DIRECTIONS) anchors[c.key] = c.sub.findAll(roots).firstOrNull() ?: return emptyList()
        }
        val out = ArrayList<UiNode>()
        for (r in roots) r.walk { if (matches(roots, it, anchors)) out += it }
        val first = conds.firstOrNull { it is Relation && it.key in DIRECTIONS } as Relation?
        val anchor = first?.let { anchors[it.key] } ?: return out
        return out.sortedBy { dist(it, anchor) }
    }

    override fun describe(): String = source.toString()

    private fun anchorsFor(roots: List<UiNode>): Map<String, UiNode> {
        val anchors = HashMap<String, UiNode>()
        for (c in conds) {
            if (c is Relation && c.key in DIRECTIONS) c.sub.findAll(roots).firstOrNull()?.let { anchors[c.key] = it }
        }
        return anchors
    }

    private fun matches(roots: List<UiNode>, n: UiNode, anchors: Map<String, UiNode>): Boolean = conds.all { c ->
        when (c) {
            is Field -> Selector.matchField(c.key, c.value, n) { c.regex!! }
            is Flag -> flag(n, c.key) == c.value
            is Exact -> n.bounds == c.bounds
            is Relation -> when (c.key) {
                "has" -> c.sub.subtreeHas(roots, n, self = false)
                "row" -> c.sub.subtreeHas(roots, rowOf(n), self = true)
                "inside" -> {
                    val sa = c.sub.anchorsFor(roots)
                    n.ancestors().any { c.sub.matches(roots, it, sa) }
                }
                else -> {
                    val a = anchors[c.key]
                    if (a == null || n.bounds.isEmpty || n === a) {
                        false
                    } else {
                        when (c.key) {
                            "below" -> centerY(n) > a.bounds.b
                            "above" -> centerY(n) < a.bounds.t
                            "left_of" -> centerX(n) < a.bounds.l
                            else -> centerX(n) > a.bounds.r
                        }
                    }
                }
            }
        }
    }

    private fun subtreeHas(roots: List<UiNode>, root: UiNode, self: Boolean): Boolean {
        val anchors = anchorsFor(roots)
        var found = false
        root.walk { if (!found && (self || it !== root)) found = matches(roots, it, anchors) }
        return found
    }

    companion object {
        val STRING_KEYS = listOf("text", "textContains", "textMatches", "id", "desc", "descContains", "descMatches", "class", "package")
        val BOOL_KEYS = listOf("clickable", "long_clickable", "checkable", "checked", "enabled", "focused", "selected", "scrollable", "editable", "password", "visible")
        val RELATIONS = listOf("has", "inside", "row", "below", "above", "left_of", "right_of")
        val DIRECTIONS = listOf("below", "above", "left_of", "right_of")
        private const val MAX_DEPTH = 8

        /** Checks a query object the way spec.CheckQuery does; the message becomes BAD_ARGS. */
        fun parse(o: Any?, depth: Int = 0): Query {
            require(depth <= MAX_DEPTH) { "query is nested more than $MAX_DEPTH levels" }
            require(o is JSONObject) { "a query must be an object" }
            require(o.length() > 0) { "a query needs at least one condition" }
            val conds = ArrayList<Cond>()
            for (k in o.keys()) {
                val v = o.get(k)
                when (k) {
                    in STRING_KEYS -> {
                        require(v is String) { "$k must be a string" }
                        val regex = if (k.endsWith("Matches")) {
                            try {
                                Regex(v)
                            } catch (e: IllegalArgumentException) {
                                throw IllegalArgumentException("$k is not a valid regular expression: ${e.message}")
                            }
                        } else {
                            null
                        }
                        conds += Field(k, v, regex)
                    }
                    in BOOL_KEYS -> {
                        require(v is Boolean) { "$k must be true or false" }
                        conds += Flag(k, v)
                    }
                    "bounds" -> {
                        require(v is JSONArray && v.length() == 4 && (0 until 4).all { v.get(it) is Number }) { "bounds must be [left, top, right, bottom]" }
                        conds += Exact(Bounds(v.getInt(0), v.getInt(1), v.getInt(2), v.getInt(3)))
                    }
                    in RELATIONS -> {
                        val sub = try {
                            parse(v, depth + 1)
                        } catch (e: IllegalArgumentException) {
                            throw IllegalArgumentException("$k: ${e.message}")
                        }
                        conds += Relation(k, sub)
                    }
                    else -> throw IllegalArgumentException("unknown query key \"$k\"")
                }
            }
            return Query(o, conds)
        }

        fun flag(n: UiNode, key: String): Boolean = when (key) {
            "clickable" -> n.clickable
            "long_clickable" -> n.longClickable
            "visible" -> n.visible
            "checkable" -> n.checkable
            "checked" -> n.checked
            "enabled" -> n.enabled
            "focused" -> n.focused
            "selected" -> n.selected
            "scrollable" -> n.scrollable
            "editable" -> n.editable
            "password" -> n.password
            else -> false
        }

        /** The list item holding n; outside a list, the nearest clickable ancestor, else the parent. */
        fun rowOf(n: UiNode): UiNode {
            var a: UiNode = n
            while (true) {
                val p = a.parent ?: break
                if (isList(p)) return a
                a = p
            }
            return n.ancestors().firstOrNull { it.clickable } ?: n.parent ?: n
        }

        /** A list widget, or a scrollable view with several children. A short list that fits on screen is not scrollable. */
        private fun isList(n: UiNode): Boolean =
            listOf("RecyclerView", "ListView", "GridView").any { n.cls.endsWith(it) } || (n.scrollable && n.children.size > 1)

        private fun centerX(n: UiNode) = (n.bounds.l + n.bounds.r) / 2.0
        private fun centerY(n: UiNode) = (n.bounds.t + n.bounds.b) / 2.0
        private fun dist(n: UiNode, a: UiNode) = hypot(centerX(n) - centerX(a), centerY(n) - centerY(a))
    }
}
