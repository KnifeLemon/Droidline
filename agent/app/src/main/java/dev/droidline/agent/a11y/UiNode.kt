package dev.droidline.agent.a11y

import org.json.JSONArray
import org.json.JSONObject

data class Bounds(val l: Int, val t: Int, val r: Int, val b: Int) {
    val cx: Int get() = (l + r) / 2
    val cy: Int get() = (t + b) / 2
    val area: Long get() = if (r > l && b > t) (r - l).toLong() * (b - t) else 0L
    val isEmpty: Boolean get() = area == 0L

    fun toJson() = JSONArray().put(l).put(t).put(r).put(b)
}

/** A snapshot of one screen element. [handle] carries the platform node for actions and stays out of JSON. */
class UiNode(
    val text: String = "",
    val id: String = "",
    val desc: String = "",
    val cls: String = "",
    val pkg: String = "",
    val bounds: Bounds = Bounds(0, 0, 0, 0),
    val clickable: Boolean = false,
    val longClickable: Boolean = false,
    val checkable: Boolean = false,
    val checked: Boolean = false,
    val enabled: Boolean = true,
    val focused: Boolean = false,
    val selected: Boolean = false,
    val scrollable: Boolean = false,
    val editable: Boolean = false,
    val password: Boolean = false,
    val children: List<UiNode> = emptyList(),
    val handle: Any? = null,
) {
    var parent: UiNode? = null
        private set

    init {
        for (c in children) c.parent = this
    }

    /** Pre-order walk, which is the document order nth counts in. */
    fun walk(visit: (UiNode) -> Unit) {
        visit(this)
        for (c in children) c.walk(visit)
    }

    fun ancestors(): Sequence<UiNode> = generateSequence(parent) { it.parent }

    fun toJson(): JSONObject = JSONObject()
        .put("text", if (password) "" else text)
        .put("id", id)
        .put("desc", desc)
        .put("class", cls)
        .put("package", pkg)
        .put("bounds", bounds.toJson())
        .put("clickable", clickable)
        .put("long_clickable", longClickable)
        .put("checkable", checkable)
        .put("checked", checked)
        .put("enabled", enabled)
        .put("focused", focused)
        .put("selected", selected)
        .put("scrollable", scrollable)
        .put("editable", editable)
        .put("password", password)
        .put("children", JSONArray().also { a -> children.forEach { a.put(it.toJson()) } })
}

data class Selector(val by: String, val value: String) {
    fun matches(n: UiNode): Boolean = when (by) {
        "text" -> n.text == value
        "textContains" -> n.text.contains(value)
        "id" -> n.id == value || (!value.contains(':') && n.id.endsWith(":id/$value"))
        "desc" -> n.desc == value
        "descContains" -> n.desc.contains(value)
        "class" -> n.cls == value
        else -> false
    }

    /** The `target` field of NOT_FOUND and friends: `<by> '<value>'`. */
    fun describe(): String = "$by '$value'"

    companion object {
        val KINDS = setOf("text", "textContains", "id", "desc", "descContains", "class")
    }
}

object Matcher {
    fun findAll(roots: List<UiNode>, sel: Selector): List<UiNode> {
        val out = ArrayList<UiNode>()
        for (r in roots) r.walk { if (sel.matches(it)) out += it }
        return out
    }

    fun nth(roots: List<UiNode>, sel: Selector, nth: Int): UiNode? = findAll(roots, sel).getOrNull(nth)

    fun count(roots: List<UiNode>, sel: Selector): Int = findAll(roots, sel).size

    /** Nearest ancestor that accepts a click, used when the matched node itself refuses it. */
    fun clickableAncestor(n: UiNode): UiNode? = n.ancestors().firstOrNull { it.clickable && it.enabled }

    fun longClickableAncestor(n: UiNode): UiNode? = n.ancestors().firstOrNull { it.longClickable && it.enabled }

    /** The editable node itself or the first editable descendant, for wrappers like TextInputLayout. */
    fun editableTarget(n: UiNode): UiNode {
        if (n.editable) return n
        var found: UiNode? = null
        n.walk { if (found == null && it.editable) found = it }
        return found ?: n
    }

    /** Largest scrollable node, the usual list or page container. */
    fun mainScrollable(roots: List<UiNode>): UiNode? {
        var best: UiNode? = null
        for (r in roots) r.walk { if (it.scrollable && it.bounds.area > (best?.bounds?.area ?: -1L)) best = it }
        return best
    }

    /** A cheap fingerprint of visible text, to notice when scrolling stopped moving the content. */
    fun signature(roots: List<UiNode>): Int {
        var h = 17
        for (r in roots) r.walk { h = h * 31 + it.text.hashCode() + it.desc.hashCode() * 7 + it.bounds.hashCode() }
        return h
    }
}
