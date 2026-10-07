package dev.droidline.agent.a11y

import android.accessibilityservice.AccessibilityService
import android.os.Bundle
import android.view.accessibility.AccessibilityNodeInfo
import dev.droidline.agent.cmd.CmdError
import dev.droidline.agent.ime.DroidKeyboard
import dev.droidline.agent.ime.KeyAction
import dev.droidline.agent.ime.KeyNames
import kotlinx.coroutines.delay
import org.json.JSONArray
import org.json.JSONObject

/** Selector, gesture and input commands built on the accessibility service. */
object UiCommands {
    private const val POLL_MS = 200L

    fun svc(): DroidAccessibilityService = DroidAccessibilityService.instance ?: throw CmdError("NO_ACCESSIBILITY")

    fun selector(cmd: String, p: JSONObject): Selector {
        val by = p.optString("by")
        if (by !in Selector.KINDS) throw CmdError.badArgs(cmd, "by must be one of ${Selector.KINDS.joinToString(", ")}")
        val value = p.opt("value") as? String ?: throw CmdError.badArgs(cmd, "value must be a string")
        return Selector(by, value)
    }

    private fun nth(p: JSONObject) = p.optInt("nth", 0).coerceAtLeast(0)
    private fun timeoutSec(p: JSONObject, default: Double = 10.0) = p.optDouble("timeout", default).coerceIn(0.0, 3600.0)

    /** Formats seconds the way a person wrote them: 10, not 10.0. */
    fun secText(s: Double): Any = if (s == Math.floor(s)) s.toLong() else s

    fun notFound(sel: Selector, timeout: Double) = CmdError(
        "NOT_FOUND",
        mapOf("target" to sel.describe(), "timeout" to secText(timeout), "screen" to svc().screenText()),
    )

    fun findNow(sel: Selector, nth: Int): UiNode? = Matcher.nth(svc().selectorRoots(), sel, nth)

    suspend fun await(sel: Selector, nth: Int, timeout: Double): UiNode {
        val deadline = System.currentTimeMillis() + (timeout * 1000).toLong()
        while (true) {
            findNow(sel, nth)?.let { return it }
            if (System.currentTimeMillis() >= deadline) throw notFound(sel, timeout)
            delay(POLL_MS)
        }
    }

    private fun act(n: UiNode?, action: Int, args: Bundle? = null): Boolean {
        val info = n?.handle as? AccessibilityNodeInfo ?: return false
        return runCatching { if (args == null) info.performAction(action) else info.performAction(action, args) }.getOrDefault(false)
    }

    private fun clampToScreen(x: Int, y: Int): Pair<Int, Int> {
        val (w, h) = svc().screenSize()
        return x.coerceIn(0, maxOf(0, w - 1)) to y.coerceIn(0, maxOf(0, h - 1))
    }

    /** Node click, then the nearest clickable ancestor, then a tap at the bounds center. */
    suspend fun click(n: UiNode): String? {
        if (n.clickable && n.enabled && act(n, AccessibilityNodeInfo.ACTION_CLICK)) return "node"
        Matcher.clickableAncestor(n)?.let { if (act(it, AccessibilityNodeInfo.ACTION_CLICK)) return "parent" }
        if (!n.bounds.isEmpty) {
            val (x, y) = clampToScreen(n.bounds.cx, n.bounds.cy)
            if (svc().tapAt(x, y)) return "gesture"
        }
        return null
    }

    private suspend fun longClick(n: UiNode, ms: Long): String? {
        if (n.longClickable && n.enabled && act(n, AccessibilityNodeInfo.ACTION_LONG_CLICK)) return "node"
        Matcher.longClickableAncestor(n)?.let { if (act(it, AccessibilityNodeInfo.ACTION_LONG_CLICK)) return "parent" }
        if (!n.bounds.isEmpty) {
            val (x, y) = clampToScreen(n.bounds.cx, n.bounds.cy)
            if (svc().tapAt(x, y, ms)) return "gesture"
        }
        return null
    }

    suspend fun touch(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val sel = selector("touch", p)
        val node = await(sel, nth(p), timeoutSec(p))
        val via = click(node) ?: throw CmdError("NOT_CLICKABLE", mapOf("target" to sel.describe()))
        return JSONObject().put("via", via).put("ms", System.currentTimeMillis() - t0)
    }

    suspend fun longTouch(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val sel = selector("long_touch", p)
        val node = await(sel, nth(p), timeoutSec(p))
        val via = longClick(node, p.optLong("ms", 800)) ?: throw CmdError("NOT_CLICKABLE", mapOf("target" to sel.describe()))
        return JSONObject().put("via", via).put("ms", System.currentTimeMillis() - t0)
    }

    private fun intParam(cmd: String, p: JSONObject, name: String): Int {
        val v = p.opt(name)
        if (v !is Number) throw CmdError.badArgs(cmd, "$name must be an integer")
        return v.toInt()
    }

    suspend fun tap(p: JSONObject, long: Boolean): JSONObject {
        val cmd = if (long) "long_tap" else "tap"
        val t0 = System.currentTimeMillis()
        val x = intParam(cmd, p, "x")
        val y = intParam(cmd, p, "y")
        val ok = svc().tapAt(x, y, if (long) p.optLong("ms", 800) else 50)
        if (!ok) throw CmdError("NOT_CLICKABLE", mapOf("target" to "point ($x, $y)"))
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    /** Direction swipes run across the middle 60% of the screen; "up" moves the finger up. */
    fun directionLine(dir: String, w: Int, h: Int): IntArray = when (dir) {
        "up" -> intArrayOf(w / 2, h * 8 / 10, w / 2, h * 2 / 10)
        "down" -> intArrayOf(w / 2, h * 2 / 10, w / 2, h * 8 / 10)
        "left" -> intArrayOf(w * 8 / 10, h / 2, w * 2 / 10, h / 2)
        "right" -> intArrayOf(w * 2 / 10, h / 2, w * 8 / 10, h / 2)
        else -> throw IllegalArgumentException(dir)
    }

    suspend fun swipe(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val ms = p.optLong("ms", 300)
        val x1 = p.opt("x1")
        val line = if (x1 is String) {
            if (x1 !in setOf("up", "down", "left", "right")) throw CmdError.badArgs("swipe", "direction must be up, down, left or right")
            val (w, h) = svc().screenSize()
            directionLine(x1, w, h)
        } else {
            intArrayOf(intParam("swipe", p, "x1"), intParam("swipe", p, "y1"), intParam("swipe", p, "x2"), intParam("swipe", p, "y2"))
        }
        if (!svc().swipe(line[0], line[1], line[2], line[3], ms)) {
            throw CmdError("NOT_CLICKABLE", mapOf("target" to "swipe (${line.joinToString(", ")})"))
        }
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    /** "down" reveals content further down, the usual way to look for a list item below the fold. */
    suspend fun scrollTo(p: JSONObject): JSONObject {
        val sel = selector("scroll_to", p)
        val nth = nth(p)
        val dir = p.optString("direction", "down")
        if (dir !in setOf("down", "up", "left", "right")) throw CmdError.badArgs("scroll_to", "direction must be down, up, left or right")
        val maxSwipes = p.optInt("max_swipes", 20).coerceAtLeast(0)
        val t0 = System.currentTimeMillis()
        var swipes = 0
        var lastSig: Int? = null
        var unchanged = 0
        while (true) {
            val roots = svc().selectorRoots()
            if (Matcher.nth(roots, sel, nth) != null) return JSONObject().put("swipes", swipes)
            val sig = Matcher.signature(roots)
            unchanged = if (sig == lastSig) unchanged + 1 else 0
            lastSig = sig
            if (swipes >= maxSwipes || unchanged >= 2) break
            scrollOnce(roots, dir)
            swipes++
            delay(450)
        }
        val elapsed = Math.round((System.currentTimeMillis() - t0) / 100.0) / 10.0
        throw CmdError("NOT_FOUND", mapOf("target" to sel.describe(), "timeout" to secText(elapsed), "screen" to svc().screenText(), "swipes" to swipes))
    }

    private suspend fun scrollOnce(roots: List<UiNode>, dir: String) {
        val forward = dir == "down" || dir == "right"
        val action = if (forward) AccessibilityNodeInfo.ACTION_SCROLL_FORWARD else AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD
        if (act(Matcher.mainScrollable(roots), action)) return
        val (w, h) = svc().screenSize()
        // Content moving down means the finger moves up.
        val finger = when (dir) { "down" -> "up"; "up" -> "down"; "right" -> "left"; else -> "right" }
        val l = directionLine(finger, w, h)
        svc().swipe(l[0], l[1], l[2], l[3], 300)
    }

    private fun setText(n: UiNode, text: String): Boolean =
        act(n, AccessibilityNodeInfo.ACTION_SET_TEXT, Bundle().apply {
            putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, text)
        })

    private fun currentText(n: UiNode): String {
        val info = n.handle as? AccessibilityNodeInfo ?: return n.text
        runCatching { info.refresh() }
        if (info.isShowingHintText) return ""
        return info.text?.toString().orEmpty()
    }

    suspend fun input(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val sel = selector("input", p)
        val text = p.opt("text") as? String ?: throw CmdError.badArgs("input", "text must be a string")
        val append = p.optBoolean("append", false)
        val node = Matcher.editableTarget(await(sel, nth(p), timeoutSec(p)))
        val before = currentText(node)
        val wanted = if (append) before + text else text
        if (setText(node, wanted)) {
            val after = currentText(node)
            // Some fields return true but ignore the action; those fall through to the keyboard.
            if (node.password || after != before || before == wanted) {
                return JSONObject().put("via", "set_text").put("ms", System.currentTimeMillis() - t0)
            }
        }
        val kb = DroidKeyboard.current(svc()) ?: throw CmdError("NO_IME")
        click(node)
        delay(300)
        val ok = if (append) kb.appendText(text) else kb.replaceAll(text)
        if (!ok) throw CmdError("NO_IME")
        return JSONObject().put("via", "ime").put("ms", System.currentTimeMillis() - t0)
    }

    suspend fun clear(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val sel = selector("clear", p)
        val node = Matcher.editableTarget(await(sel, nth(p), timeoutSec(p)))
        if (setText(node, "") && (node.password || currentText(node).isEmpty())) {
            return JSONObject().put("ms", System.currentTimeMillis() - t0)
        }
        val kb = DroidKeyboard.current(svc()) ?: throw CmdError("NO_IME")
        click(node)
        delay(300)
        if (!kb.replaceAll("")) throw CmdError("NO_IME")
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    fun global(name: String): Boolean {
        val action = when (name) {
            "back" -> AccessibilityService.GLOBAL_ACTION_BACK
            "home" -> AccessibilityService.GLOBAL_ACTION_HOME
            "recents" -> AccessibilityService.GLOBAL_ACTION_RECENTS
            "notifications" -> AccessibilityService.GLOBAL_ACTION_NOTIFICATIONS
            "quick_settings" -> AccessibilityService.GLOBAL_ACTION_QUICK_SETTINGS
            "lock" -> AccessibilityService.GLOBAL_ACTION_LOCK_SCREEN
            else -> return false
        }
        return svc().performGlobalAction(action)
    }

    fun globalCommand(cmd: String, name: String): JSONObject {
        if (!global(name)) throw CmdError.internal("global action $name was refused")
        return JSONObject()
    }

    suspend fun sendkey(p: JSONObject): JSONObject {
        val text = p.opt("text") as? String
        val key = p.opt("key").takeUnless { it == JSONObject.NULL }
        return when (val a = KeyNames.resolve(key, text)) {
            null -> throw CmdError.badArgs("sendkey", "pass a key name, a key code, or text")
            is KeyAction.Global -> {
                svc()
                if (!global(a.name)) throw CmdError.internal("global action ${a.name} was refused")
                JSONObject().put("via", "global")
            }
            is KeyAction.Code -> {
                val kb = DroidKeyboard.current(svc()) ?: throw CmdError("NO_IME")
                if (!kb.sendKey(a.code)) throw CmdError("NO_IME")
                JSONObject().put("via", "ime")
            }
            is KeyAction.Text -> {
                val kb = DroidKeyboard.current(svc()) ?: throw CmdError("NO_IME")
                if (!kb.commit(a.text)) throw CmdError("NO_IME")
                JSONObject().put("via", "ime")
            }
        }
    }

    fun dump(p: JSONObject): JSONObject {
        val s = svc()
        val (pkg, act) = s.current()
        val (w, h) = s.screenSize()
        val roots = if (p.optBoolean("all_windows", false)) s.allWindowRoots() else s.selectorRoots()
        val tree = if (roots.size == 1) roots[0] else UiNode(cls = "windows", bounds = Bounds(0, 0, w, h), children = roots)
        return JSONObject().put("package", pkg).put("activity", act).put("width", w).put("height", h).put("tree", tree.toJson())
    }

    fun current(): JSONObject {
        val (pkg, act) = svc().currentSettled()
        return JSONObject().put("package", pkg).put("activity", act)
    }

    fun value(v: Any?): JSONObject = JSONObject().put("value", v ?: JSONObject.NULL)

    fun exists(p: JSONObject) = value(findNow(selector("exists", p), nth(p)) != null)

    fun getText(p: JSONObject) = value(findNow(selector("get_text", p), nth(p))?.let { if (it.password) "" else it.text } ?: "")

    fun flag(cmd: String, p: JSONObject, pick: (UiNode) -> Boolean) = value(findNow(selector(cmd, p), nth(p))?.let(pick) ?: false)

    fun count(p: JSONObject) = value(Matcher.count(svc().selectorRoots(), selector("count", p)))

    suspend fun which(p: JSONObject): JSONObject {
        val arr = p.optJSONArray("candidates") ?: throw CmdError.badArgs("which", "candidates must be a list of [by, value] pairs")
        val sels = (0 until arr.length()).map { i ->
            val item = arr.get(i)
            val (by, v) = when (item) {
                is JSONArray -> item.optString(0) to item.opt(1)
                is JSONObject -> item.optString("by") to item.opt("value")
                else -> throw CmdError.badArgs("which", "candidate $i must be [by, value]")
            }
            if (by !in Selector.KINDS || v !is String) throw CmdError.badArgs("which", "candidate $i must be [by, value]")
            Selector(by, v)
        }
        val deadline = System.currentTimeMillis() + (timeoutSec(p) * 1000).toLong()
        while (true) {
            val roots = svc().selectorRoots()
            val idx = sels.indexOfFirst { Matcher.findAll(roots, it).isNotEmpty() }
            if (idx >= 0) return value(idx)
            if (System.currentTimeMillis() >= deadline) return value(-1)
            delay(POLL_MS)
        }
    }

    suspend fun wait(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        await(selector("wait", p), nth(p), timeoutSec(p))
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    suspend fun waitGone(p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val sel = selector("wait_gone", p)
        val timeout = timeoutSec(p)
        val deadline = t0 + (timeout * 1000).toLong()
        while (findNow(sel, 0) != null) {
            if (System.currentTimeMillis() >= deadline) {
                throw CmdError("TIMEOUT", mapOf("cmd" to "wait_gone", "timeout" to secText(timeout), "target" to sel.describe()))
            }
            delay(POLL_MS)
        }
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    fun inApp(p: JSONObject): JSONObject {
        val pkg = p.opt("package") as? String ?: throw CmdError.badArgs("in_app", "package must be a string")
        return value(svc().currentSettled().first == pkg)
    }

    fun keyboardShown() = value(svc().keyboardShown())

    fun lastToast(p: JSONObject): JSONObject {
        svc()
        val maxAge = p.optDouble("max_age", 30.0)
        val age = System.currentTimeMillis() - DroidAccessibilityService.lastToastAt
        return value(if (DroidAccessibilityService.lastToastAt > 0 && age <= maxAge * 1000) DroidAccessibilityService.lastToast else "")
    }
}
