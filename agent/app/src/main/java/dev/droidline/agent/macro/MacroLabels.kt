package dev.droidline.agent.macro

import dev.droidline.agent.a11y.UiNode
import org.json.JSONObject

class TargetSpec(val step: String, val ids: List<String>, val genericIds: List<String>, val labels: List<String>)

object MacroLabels {
    val LANGS = listOf("en", "ko", "zh")

    fun parse(json: String): Map<String, TargetSpec> {
        val steps = JSONObject(json).getJSONObject("steps")
        val out = LinkedHashMap<String, TargetSpec>()
        for (name in steps.keys()) {
            val o = steps.getJSONObject(name)
            val labels = o.optJSONObject("labels")
            val merged = LinkedHashSet<String>()
            for (lang in LANGS) {
                val arr = labels?.optJSONArray(lang) ?: continue
                for (i in 0 until arr.length()) merged += arr.getString(i)
            }
            out[name] = TargetSpec(name, strings(o, "ids"), strings(o, "generic_ids"), merged.toList())
        }
        return out
    }

    private fun strings(o: JSONObject, key: String): List<String> {
        val a = o.optJSONArray(key) ?: return emptyList()
        return (0 until a.length()).map { a.getString(it) }
    }

    /** Trusted ids first, then generic ids that also show a label, then labels in list order. */
    fun find(roots: List<UiNode>, spec: TargetSpec): UiNode? {
        val all = ArrayList<UiNode>()
        for (r in roots) r.walk { all += it }
        for (id in spec.ids) all.firstOrNull { it.id == id }?.let { return it }
        for (id in spec.genericIds) {
            all.firstOrNull { it.id == id && showsLabel(it, spec.labels) }?.let { return it }
        }
        for (label in spec.labels) {
            all.firstOrNull { same(it.text, label) }?.let { return it }
            all.firstOrNull { same(it.desc, label) }?.let { return it }
        }
        return null
    }

    private fun showsLabel(n: UiNode, labels: List<String>): Boolean {
        var hit = false
        n.walk { if (!hit && labels.any { l -> same(it.text, l) || same(it.desc, l) }) hit = true }
        return hit
    }

    private fun same(a: String, b: String): Boolean = a.trim().equals(b, ignoreCase = true)

    /**
     * The checkable node that shows a toggle's state: the target itself, or the first checkable in its row.
     * The search stops at a scrollable list so it never picks up another row's switch.
     */
    fun toggleOf(target: UiNode): UiNode? {
        if (target.checkable) return target
        var row: UiNode? = target
        repeat(4) {
            val r = row ?: return null
            if (r.scrollable) return null
            var found: UiNode? = null
            r.walk { if (found == null && it.checkable) found = it }
            if (found != null) return found
            row = r.parent
        }
        return null
    }
}
