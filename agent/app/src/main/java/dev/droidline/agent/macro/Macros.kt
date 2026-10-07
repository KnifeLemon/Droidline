package dev.droidline.agent.macro

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Build
import android.provider.Settings
import dev.droidline.agent.Agent
import dev.droidline.agent.a11y.UiCommands
import dev.droidline.agent.a11y.UiNode
import dev.droidline.agent.cmd.CmdError
import dev.droidline.agent.device.Apps
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** Settings macros: kill, clear_data, data, wifi, airplane. They drive the Settings app through accessibility. */
object Macros {
    private val specs: Map<String, TargetSpec> by lazy {
        MacroLabels.parse(Agent.app.assets.open("macro_labels.json").bufferedReader().use { it.readText() })
    }

    private fun spec(name: String) = specs[name] ?: throw CmdError.internal("macro_labels.json has no step $name")

    private fun fail(cmd: String, step: String) =
        CmdError("MACRO_FAILED", mapOf("cmd" to cmd, "step" to step, "screen" to UiCommands.svc().screenText()))

    private fun done(t0: Long) = JSONObject().put("via", "settings_macro").put("ms", System.currentTimeMillis() - t0)

    private suspend fun open(ctx: Context, intent: Intent) {
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
        withContext(Dispatchers.Main) { ctx.startActivity(intent) }
        delay(600)
    }

    private fun roots(): List<UiNode> = UiCommands.svc().selectorRoots()

    private suspend fun waitFor(spec: TargetSpec, timeoutMs: Long): UiNode? {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (true) {
            MacroLabels.find(roots(), spec)?.let { return it }
            if (System.currentTimeMillis() >= deadline) return null
            delay(250)
        }
    }

    /** A disabled button can carry its state on the clickable container rather than on the text. */
    private fun effectivelyEnabled(n: UiNode): Boolean =
        n.enabled && (n.ancestors().firstOrNull { it.clickable }?.enabled ?: true)

    private fun windowKeys(r: List<UiNode>) = r.map { "${it.pkg}|${it.cls}|${it.bounds}" }.toSet()

    /**
     * A confirm button in a window that was not there before the click, or the dialog positive button anywhere.
     * Keeps label matches like "OK" from hitting the screen underneath.
     */
    private fun findConfirm(before: Set<String>): UiNode? {
        val r = roots()
        val fresh = r.filter { "${it.pkg}|${it.cls}|${it.bounds}" !in before }
        val confirm = spec("confirm")
        return MacroLabels.find(fresh, confirm)
            ?: MacroLabels.find(r, TargetSpec("confirm", confirm.ids, emptyList(), emptyList()))
    }

    private suspend fun leave(back: Boolean) {
        if (back) {
            UiCommands.global("back")
            delay(300)
        }
        UiCommands.global("home")
        delay(300)
    }

    private fun pkg(cmd: String, p: JSONObject): String =
        (p.opt("package") as? String)?.takeIf { it.isNotBlank() } ?: throw CmdError.badArgs(cmd, "package must be a string")

    private fun detailsIntent(pkg: String) =
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", pkg, null))

    suspend fun kill(ctx: Context, p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val pkg = pkg("kill", p)
        UiCommands.svc()
        Apps.requireInstalled(ctx, pkg)
        open(ctx, detailsIntent(pkg))
        val button = waitFor(spec("force_stop"), 15000) ?: throw fail("kill", "force_stop")
        // A disabled Force stop button means the app is not running.
        if (!effectivelyEnabled(button)) {
            leave(false)
            return done(t0)
        }
        val before = windowKeys(roots())
        UiCommands.click(button) ?: throw fail("kill", "force_stop")
        if (!confirmOrSettle(before, 4000) { findForceStop()?.let { !effectivelyEnabled(it) } == true }) {
            throw fail("kill", "confirm")
        }
        val deadline = System.currentTimeMillis() + 3000
        while (System.currentTimeMillis() < deadline && findForceStop()?.let(::effectivelyEnabled) == true) delay(250)
        leave(false)
        return done(t0)
    }

    private fun findForceStop(): UiNode? = MacroLabels.find(roots(), spec("force_stop"))

    /** Clicks a confirm dialog when one appears; true once it was clicked or [settled] holds without one. */
    private suspend fun confirmOrSettle(before: Set<String>, timeoutMs: Long, settled: () -> Boolean): Boolean {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            findConfirm(before)?.let {
                UiCommands.click(it) ?: return false
                delay(500)
                return true
            }
            if (settled()) return true
            delay(250)
        }
        return false
    }

    suspend fun clearData(ctx: Context, p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val pkg = pkg("clear_data", p)
        UiCommands.svc()
        Apps.requireInstalled(ctx, pkg)
        open(ctx, detailsIntent(pkg))
        val storage = waitFor(spec("storage"), 15000) ?: throw fail("clear_data", "storage")
        UiCommands.click(storage) ?: throw fail("clear_data", "storage")
        val clear = waitFor(spec("clear_storage"), 10000) ?: run {
            // Apps with their own data screen (Chrome, for one) show "Manage space" instead.
            if (MacroLabels.find(roots(), spec("manage_space")) != null) throw fail("clear_data", "manage_space")
            throw fail("clear_data", "clear_storage")
        }
        if (!effectivelyEnabled(clear)) {
            leave(false)
            return done(t0)
        }
        val before = windowKeys(roots())
        UiCommands.click(clear) ?: throw fail("clear_data", "clear_storage")
        if (!confirmOrSettle(before, 4000) { false }) throw fail("clear_data", "confirm")
        delay(1000)
        leave(false)
        return done(t0)
    }

    private class Toggle(val step: String, val intents: List<Intent>, val systemState: ((Context) -> Boolean)?)

    private fun toggleFor(cmd: String): Toggle = when (cmd) {
        "wifi" -> Toggle(
            "wifi",
            listOfNotNull(
                if (Build.VERSION.SDK_INT >= 29) Intent(Settings.Panel.ACTION_WIFI) else null,
                Intent(Settings.ACTION_WIFI_SETTINGS),
            ),
        ) { c -> c.getSystemService(WifiManager::class.java).isWifiEnabled }
        "data" -> Toggle(
            "mobile_data",
            listOfNotNull(
                if (Build.VERSION.SDK_INT >= 29) Intent(Settings.Panel.ACTION_INTERNET_CONNECTIVITY) else null,
                Intent(Settings.ACTION_DATA_ROAMING_SETTINGS),
            ),
            null,
        )
        "airplane" -> Toggle("airplane", listOf(Intent(Settings.ACTION_AIRPLANE_MODE_SETTINGS))) { c ->
            Settings.Global.getInt(c.contentResolver, Settings.Global.AIRPLANE_MODE_ON, 0) != 0
        }
        else -> throw IllegalArgumentException(cmd)
    }

    suspend fun toggle(ctx: Context, cmd: String, p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val want = p.opt("on") as? Boolean ?: throw CmdError.badArgs(cmd, "on must be true or false")
        UiCommands.svc()
        val t = toggleFor(cmd)
        if (t.systemState?.invoke(ctx) == want) return done(t0)
        val target = spec(t.step)
        var reachedTarget = false
        for (intent in t.intents) {
            val isPanel = intent.action?.startsWith("android.settings.panel") == true
            open(ctx, intent)
            val node = waitFor(target, 10000)
            if (node == null) {
                leave(isPanel)
                continue
            }
            reachedTarget = true
            val sw = MacroLabels.toggleOf(node)
            if (sw != null && sw.checked == want) {
                leave(isPanel)
                return done(t0)
            }
            val before = windowKeys(roots())
            UiCommands.click(sw ?: node) ?: throw fail(cmd, t.step)
            var confirmed = false
            val deadline = System.currentTimeMillis() + 12000
            while (System.currentTimeMillis() < deadline) {
                if (t.systemState?.invoke(ctx) == want) {
                    leave(isPanel)
                    return done(t0)
                }
                val now = MacroLabels.find(roots(), target)?.let(MacroLabels::toggleOf)
                if (now != null && now.checked == want) {
                    leave(isPanel)
                    return done(t0)
                }
                if (!confirmed) {
                    findConfirm(before)?.let {
                        UiCommands.click(it)
                        confirmed = true
                    }
                }
                delay(300)
            }
            throw fail(cmd, "verify")
        }
        throw fail(cmd, if (reachedTarget) "verify" else t.step)
    }
}
