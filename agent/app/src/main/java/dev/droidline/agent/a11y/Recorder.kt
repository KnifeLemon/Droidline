package dev.droidline.agent.a11y

import android.graphics.Rect
import android.view.accessibility.AccessibilityEvent
import dev.droidline.agent.Agent
import dev.droidline.agent.cmd.CmdError
import org.json.JSONArray
import org.json.JSONObject

/**
 * Optional recording for droidline inspect. Off unless the record command turns it on; it turns
 * itself off after the time given. While on, every tap, long press and text change in another app
 * goes to the PC as an action event, with the screen it happened on. Password fields send no text.
 */
object Recorder {
    @Volatile private var until = 0L
    private var lastInput: Rect? = null

    val on: Boolean get() = System.currentTimeMillis() < until

    fun command(p: JSONObject): JSONObject {
        val enable = p.opt("on") as? Boolean ?: throw CmdError.badArgs("record", "on must be true or false")
        val minutes = p.optDouble("minutes", 30.0).coerceIn(0.1, 24 * 60.0)
        UiCommands.svc()
        until = if (enable) System.currentTimeMillis() + (minutes * 60_000).toLong() else 0L
        lastInput = null
        return JSONObject()
    }

    /** The first text inside a tapped row, so a row without its own text can still be named. */
    private fun firstText(n: UiNode): String {
        var found = ""
        n.walk { if (found.isEmpty() && !it.password) found = it.text }
        return found
    }

    fun onEvent(svc: DroidAccessibilityService, event: AccessibilityEvent) {
        val kind = when (event.eventType) {
            AccessibilityEvent.TYPE_VIEW_CLICKED -> "click"
            AccessibilityEvent.TYPE_VIEW_LONG_CLICKED -> "long_click"
            AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED -> "input"
            else -> return
        }
        val pkg = event.packageName?.toString() ?: return
        if (pkg == svc.packageName || svc.isKeyboardPackage(pkg)) return
        val src = event.source ?: return
        val r = Rect().also { src.getBoundsInScreen(it) }
        val password = event.isPassword || src.isPassword
        val target = JSONObject()
            .put("bounds", JSONArray().put(r.left).put(r.top).put(r.right).put(r.bottom))
            .put("class", src.className?.toString().orEmpty())
            .put("id", src.viewIdResourceName.orEmpty())
            .put("desc", src.contentDescription?.toString().orEmpty())
            .put("text", if (password) "" else src.text?.toString().orEmpty())
            .put("inner", if (password) "" else firstText(svc.snapshotOf(src)))
        val ev = JSONObject().put("event", "action").put("kind", kind).put("package", pkg).put("target", target)
        if (kind == "input") {
            ev.put("password", password)
            ev.put("text", if (password) "" else event.text.joinToString("") { it.toString() })
            // Typing sends one event per change; the screen is sent with the first one only.
            if (r == lastInput) {
                Agent.emit(ev)
                return
            }
            lastInput = Rect(r)
        } else {
            lastInput = null
        }
        // A tap can start opening the next screen before this event arrives; the window the
        // tapped element belongs to still holds the screen it was on.
        val own = runCatching { src.window?.root }.getOrNull()
        val roots = if (own != null) listOf(svc.snapshotOf(own)) else svc.selectorRoots()
        val (w, h) = svc.screenSize()
        val tree = if (roots.size == 1) roots[0] else UiNode(cls = "windows", bounds = Bounds(0, 0, w, h), children = roots)
        ev.put("screen", JSONObject().put("width", w).put("height", h).put("tree", tree.toJson()))
        Agent.emit(ev)
    }
}
