package dev.droidline.agent.cmd

import android.content.Context
import dev.droidline.agent.Agent
import dev.droidline.agent.a11y.UiCommands
import dev.droidline.agent.device.Capture
import dev.droidline.agent.device.DeviceCommands
import dev.droidline.agent.link.CommandCache
import dev.droidline.agent.macro.Macros
import dev.droidline.agent.notif.DroidNotificationListener
import dev.droidline.agent.store.toStringList
import dev.droidline.agent.vpn.ProxyController
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.Executors

/**
 * Runs commands one at a time in arrival order (PROTOCOL 4.7) and answers resent ids from the cache (4.8).
 * Commands that cut the network answer `accepted` first and send the outcome later as a `result` event.
 */
class CommandRunner(private val ctx: Context, private val scope: CoroutineScope) {
    private val queue = Channel<JSONObject>(Channel.UNLIMITED)
    private val dispatcher = Executors.newSingleThreadExecutor { Thread(it, "droidline-cmd") }.asCoroutineDispatcher()
    private var job: Job? = null

    fun start() {
        if (job != null) return
        job = scope.launch(dispatcher) { for (c in queue) runOne(c) }
    }

    fun stop() {
        job?.cancel()
        job = null
        dispatcher.close()
    }

    fun submit(cmd: JSONObject) {
        val session = Agent.session
        val id = cmd.opt("id")
        when (val st = session.cache.begin(id)) {
            null -> queue.trySend(cmd)
            is CommandCache.State.Running -> Unit
            // Still buffered means resume will deliver it; otherwise send the cached answer again.
            is CommandCache.State.Done -> if (!session.buffer.contains(st.n)) session.send(JSONObject(st.body))
        }
    }

    private suspend fun runOne(cmd: JSONObject) {
        val session = Agent.session
        val id = cmd.opt("id")
        val normalized = runCatching { Agent.spec.normalize(cmd) }.getOrElse { cmd }
        if (Agent.spec.cutsNetwork(normalized)) {
            val n = session.send(JSONObject().put("id", id).put("ok", true).put("accepted", true))
            session.awaitWritten(n, 3000)
            delay(300)
            val event = JSONObject().put("event", "result").put("id", id)
            merge(event, execute(normalized))
            val n2 = session.send(event, "result")
            session.cache.finish(id, event, n2)
        } else {
            val response = JSONObject().put("id", id)
            merge(response, execute(normalized))
            val n = session.send(response)
            session.cache.finish(id, response, n)
        }
    }

    private fun merge(into: JSONObject, from: JSONObject) {
        for (k in from.keys()) into.put(k, from.get(k))
    }

    suspend fun execute(cmd: JSONObject, inBatch: Boolean = false): JSONObject {
        val name = cmd.optString("cmd")
        return try {
            val fields = dispatch(name, cmd, inBatch)
            JSONObject().put("ok", true).also { merge(it, fields) }
        } catch (e: CmdError) {
            errorJson(name, e)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            errorJson(name, CmdError.internal(e.toString()))
        }
    }

    private fun errorJson(cmd: String, e: CmdError): JSONObject {
        val fields = LinkedHashMap<String, Any?>(e.fields)
        if (!fields.containsKey("cmd")) fields["cmd"] = cmd
        val o = JSONObject()
            .put("ok", false)
            .put("error", e.code)
            .put("msg", Agent.spec.render(e.code, fields))
            .put("retryable", Agent.spec.retryable(e.code))
        for ((k, v) in fields) {
            o.put(k, when (v) {
                null -> JSONObject.NULL
                is Collection<*> -> JSONArray(v)
                else -> v
            })
        }
        return o
    }

    private fun precheck(name: String, p: JSONObject, inBatch: Boolean) {
        val spec = Agent.spec
        if (inBatch && name == "sleep") return
        val c = spec.deviceCommand(name) ?: throw CmdError("UNKNOWN_CMD", mapOf("cmd" to name, "agent" to Agent.version))
        spec.missingRequired(p)?.let { throw CmdError.badArgs(name, "missing $it") }
        c.minSdk?.let { min ->
            if (android.os.Build.VERSION.SDK_INT < min) {
                throw CmdError("UNSUPPORTED", mapOf("min_release" to releaseFor(min), "release" to android.os.Build.VERSION.RELEASE))
            }
        }
        if ("a11y" in c.requires) UiCommands.svc()
        if ("notif" in c.requires && DroidNotificationListener.instance == null) throw CmdError.noPermission("notif")
    }

    private fun releaseFor(sdk: Int) = when (sdk) {
        28 -> "9"; 29 -> "10"; 30 -> "11"; 31 -> "12"; 32 -> "12L"; 33 -> "13"; 34 -> "14"; 35 -> "15"; 36 -> "16"
        else -> "API $sdk"
    }

    private fun str(cmd: String, p: JSONObject, key: String): String =
        p.opt(key) as? String ?: throw CmdError.badArgs(cmd, "$key must be a string")

    private fun value(v: Any?) = JSONObject().put("value", v ?: JSONObject.NULL)

    private suspend fun dispatch(name: String, p: JSONObject, inBatch: Boolean): JSONObject {
        precheck(name, p, inBatch)
        return when (name) {
            "sleep" -> {
                val ms = (p.opt("ms") as? Number)?.toLong() ?: throw CmdError.badArgs("sleep", "ms must be an integer")
                if (ms < 0 || ms > 3_600_000) throw CmdError.badArgs("sleep", "ms must be between 0 and 3600000")
                delay(ms)
                JSONObject()
            }
            "dump" -> UiCommands.dump(p)
            "screenshot" -> Capture.screenshot(ctx, p)
            "current" -> UiCommands.current()
            "tap" -> UiCommands.tap(p, long = false)
            "long_tap" -> UiCommands.tap(p, long = true)
            "swipe" -> UiCommands.swipe(p)
            "touch" -> UiCommands.touch(p)
            "long_touch" -> UiCommands.longTouch(p)
            "scroll_to" -> UiCommands.scrollTo(p)
            "input" -> UiCommands.input(p)
            "clear" -> UiCommands.clear(p)
            "sendkey" -> UiCommands.sendkey(p)
            "exists" -> UiCommands.exists(p)
            "wait" -> UiCommands.wait(p)
            "wait_gone" -> UiCommands.waitGone(p)
            "get_text" -> UiCommands.getText(p)
            "checked" -> UiCommands.flag("checked", p) { it.checked }
            "enabled" -> UiCommands.flag("enabled", p) { it.enabled }
            "selected" -> UiCommands.flag("selected", p) { it.selected }
            "count" -> UiCommands.count(p)
            "which" -> UiCommands.which(p)
            "in_app" -> UiCommands.inApp(p)
            "keyboard_shown" -> UiCommands.keyboardShown()
            "last_toast" -> UiCommands.lastToast(p)
            "color" -> Capture.color(ctx, p)
            "screen_on" -> DeviceCommands.screenOn(ctx)
            "locked" -> DeviceCommands.locked(ctx)
            "wake" -> DeviceCommands.wake(ctx)
            "lock" -> UiCommands.globalCommand(name, "lock")
            "battery" -> DeviceCommands.battery(ctx)
            "orientation" -> DeviceCommands.orientation(ctx)
            "info" -> DeviceCommands.info(ctx)
            "network" -> DeviceCommands.network(ctx)
            "launch" -> DeviceCommands.launch(ctx, p)
            "open_url" -> DeviceCommands.openUrl(ctx, p)
            "kill" -> Macros.kill(ctx, p)
            "clear_data" -> Macros.clearData(ctx, p)
            "apps" -> DeviceCommands.apps(ctx, p)
            "installed" -> DeviceCommands.installed(ctx, p)
            "back" -> UiCommands.globalCommand(name, "back")
            "home" -> UiCommands.globalCommand(name, "home")
            "recents" -> UiCommands.globalCommand(name, "recents")
            "open_notifications" -> UiCommands.globalCommand(name, "notifications")
            "quick_settings" -> UiCommands.globalCommand(name, "quick_settings")
            "data", "wifi", "airplane" -> Macros.toggle(ctx, name, p)
            "clipboard" -> DeviceCommands.clipboard(ctx, p)
            "batch" -> batch(p, inBatch)
            "chrome.go" -> DeviceCommands.chromeGo(ctx, p)
            "proxy" -> ProxyController.proxy(ctx, p)
            "proxy_check" -> ProxyController.check(p)
            "notifications" -> value(DroidNotificationListener.list((p.opt("package") as? String)?.ifEmpty { null }))
            "has_notification" -> value(DroidNotificationListener.has(str(name, p, "by"), str(name, p, "value")))
            "notification_reply" -> { DroidNotificationListener.reply(str(name, p, "key"), str(name, p, "text")); JSONObject() }
            "notification_click" -> { DroidNotificationListener.click(str(name, p, "key")); JSONObject() }
            "notification_dismiss" -> { DroidNotificationListener.dismiss(str(name, p, "key")); JSONObject() }
            "notify_filter" -> notifyFilter(p)
            else -> throw CmdError("UNKNOWN_CMD", mapOf("cmd" to name, "agent" to Agent.version))
        }
    }

    private fun notifyFilter(p: JSONObject): JSONObject {
        if (p.has("packages") && !p.isNull("packages")) {
            val arr = p.optJSONArray("packages") ?: throw CmdError.badArgs("notify_filter", "packages must be a list of strings")
            Agent.settings.notifyAllowlist = arr.toStringList().filter { it.isNotBlank() }.toSet()
        }
        return value(JSONArray(Agent.settings.notifyAllowlist.sorted()))
    }

    private suspend fun batch(p: JSONObject, nested: Boolean): JSONObject {
        if (nested) throw CmdError.badArgs("batch", "batch cannot run inside a batch")
        val steps = p.optJSONArray("steps") ?: throw CmdError.badArgs("batch", "steps must be a list")
        val stopOnError = p.optBoolean("stop_on_error", true)
        val results = JSONArray()
        for (i in 0 until steps.length()) {
            val step = try {
                Agent.spec.normalize(steps.get(i), inBatch = true)
            } catch (e: Exception) {
                null
            }
            val r = when {
                step == null -> errorJson("batch", CmdError.badArgs("batch", "step $i must be [cmd, args...] or {\"cmd\": ...}"))
                step.optString("cmd") == "batch" -> errorJson("batch", CmdError.badArgs("batch", "batch cannot run inside a batch"))
                else -> execute(step, inBatch = true)
            }
            results.put(r)
            if (stopOnError && !r.optBoolean("ok")) break
        }
        return JSONObject().put("results", results)
    }
}
