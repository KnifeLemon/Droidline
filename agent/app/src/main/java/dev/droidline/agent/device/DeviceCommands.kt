package dev.droidline.agent.device

import android.app.KeyguardManager
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import android.os.BatteryManager
import android.os.Build
import android.os.PowerManager
import android.provider.Browser
import android.provider.Settings
import dev.droidline.agent.Agent
import dev.droidline.agent.a11y.DroidAccessibilityService
import dev.droidline.agent.cmd.CmdError
import dev.droidline.agent.ime.DroidKeyboard
import dev.droidline.agent.service.Readiness
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.json.JSONObject

/**
 * App and device commands that do not need the accessibility tree.
 * Starting activities from the background works because the bound accessibility service exempts the app.
 */
object DeviceCommands {
    private fun pkgParam(cmd: String, p: JSONObject): String =
        (p.opt("package") as? String)?.takeIf { it.isNotBlank() } ?: throw CmdError.badArgs(cmd, "package must be a string")

    private fun value(v: Any?) = JSONObject().put("value", v ?: JSONObject.NULL)

    suspend fun launch(ctx: Context, p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val pkg = pkgParam("launch", p)
        Apps.requireInstalled(ctx, pkg)
        val activity = (p.opt("activity") as? String).orEmpty()
        val pm = ctx.packageManager
        val intent = if (activity.isEmpty()) {
            (pm.getLaunchIntentForPackage(pkg) ?: pm.getLeanbackLaunchIntentForPackage(pkg))
                ?.addFlags(Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED)
                ?: throw CmdError.badArgs("launch", "$pkg has no launcher activity; pass one explicitly")
        } else {
            val cls = if (activity.startsWith(".")) pkg + activity else activity
            val cn = ComponentName(pkg, cls)
            val info = try {
                pm.getActivityInfo(cn, 0)
            } catch (e: PackageManager.NameNotFoundException) {
                throw CmdError.badArgs("launch", "$pkg has no activity $activity")
            }
            if (!info.exported) throw blocked(activity, pkg)
            Intent(Intent.ACTION_MAIN).setComponent(cn)
        }
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            withContext(Dispatchers.Main) { ctx.startActivity(intent) }
        } catch (e: SecurityException) {
            throw blocked(activity.ifEmpty { intent.component?.className.orEmpty() }, pkg)
        } catch (e: ActivityNotFoundException) {
            throw CmdError.badArgs("launch", "$pkg has no activity ${activity.ifEmpty { "to launch" }}")
        }
        awaitForeground(pkg, 5000)
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    private fun blocked(activity: String, pkg: String) = CmdError("ACTIVITY_BLOCKED", mapOf("activity" to activity, "package" to pkg))

    private suspend fun awaitForeground(pkg: String, timeoutMs: Long) {
        val svc = DroidAccessibilityService.instance ?: return
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (svc.current().first == pkg) return
            delay(200)
        }
    }

    suspend fun openUrl(ctx: Context, p: JSONObject): JSONObject {
        val url = (p.opt("url") as? String)?.takeIf { it.isNotBlank() } ?: throw CmdError.badArgs("open_url", "url must be a string")
        val pkg = (p.opt("package") as? String)?.takeIf { it.isNotBlank() }
        if (pkg != null) Apps.requireInstalled(ctx, pkg)
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (pkg != null) intent.setPackage(pkg)
        try {
            withContext(Dispatchers.Main) { ctx.startActivity(intent) }
        } catch (e: ActivityNotFoundException) {
            throw CmdError.badArgs("open_url", if (pkg != null) "$pkg cannot open $url" else "no app can open $url")
        } catch (e: SecurityException) {
            throw CmdError.badArgs("open_url", "the app that handles $url does not allow other apps to open it")
        }
        return JSONObject()
    }

    private const val CHROME = "com.android.chrome"

    fun chromeUrl(raw: String): String {
        val u = raw.trim()
        return if (Regex("^[a-zA-Z][a-zA-Z0-9+.-]*:").containsMatchIn(u) && !Regex("^[^/]+:\\d").containsMatchIn(u)) u else "https://$u"
    }

    suspend fun chromeGo(ctx: Context, p: JSONObject): JSONObject {
        val t0 = System.currentTimeMillis()
        val raw = (p.opt("url") as? String)?.takeIf { it.isNotBlank() } ?: throw CmdError.badArgs("chrome.go", "url must be a string")
        Apps.requireInstalled(ctx, CHROME)
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(chromeUrl(raw)))
            .setPackage(CHROME)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        // Chrome reuses the tab it opened for the same application id; a new tab is asked for explicitly.
        if (p.optBoolean("new_tab", false)) intent.putExtra(Browser.EXTRA_CREATE_NEW_TAB, true)
        else intent.putExtra(Browser.EXTRA_APPLICATION_ID, ctx.packageName)
        try {
            withContext(Dispatchers.Main) { ctx.startActivity(intent) }
        } catch (e: ActivityNotFoundException) {
            throw CmdError("APP_NOT_FOUND", mapOf("package" to CHROME, "similar" to org.json.JSONArray()))
        }
        awaitForeground(CHROME, 5000)
        return JSONObject().put("ms", System.currentTimeMillis() - t0)
    }

    fun apps(ctx: Context, p: JSONObject) = value(Apps.list(ctx, p.optBoolean("system", false)))

    fun installed(ctx: Context, p: JSONObject) = value(Apps.versionName(ctx, pkgParam("installed", p)) ?: "")

    fun screenOn(ctx: Context) = value(ctx.getSystemService(PowerManager::class.java).isInteractive)

    fun locked(ctx: Context) = value(ctx.getSystemService(KeyguardManager::class.java).isKeyguardLocked)

    suspend fun wake(ctx: Context): JSONObject {
        withContext(Dispatchers.Main) {
            ctx.startActivity(Intent(ctx, WakeActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION))
        }
        val pm = ctx.getSystemService(PowerManager::class.java)
        val km = ctx.getSystemService(KeyguardManager::class.java)
        val deadline = System.currentTimeMillis() + 2500
        while (System.currentTimeMillis() < deadline && (!pm.isInteractive || km.isKeyguardLocked)) delay(150)
        return JSONObject().put("screen_on", pm.isInteractive).put("locked", km.isKeyguardLocked)
    }

    fun battery(ctx: Context): JSONObject {
        val i = ctx.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val level = i?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
        val scale = i?.getIntExtra(BatteryManager.EXTRA_SCALE, 100) ?: 100
        val status = i?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val temp = i?.getIntExtra(BatteryManager.EXTRA_TEMPERATURE, 0) ?: 0
        val pct = if (level >= 0 && scale > 0) level * 100 / scale else -1
        val charging = status == BatteryManager.BATTERY_STATUS_CHARGING || status == BatteryManager.BATTERY_STATUS_FULL
        return JSONObject().put("level", pct).put("charging", charging).put("temperature", temp / 10.0)
    }

    fun orientation(ctx: Context) =
        value(if (ctx.resources.configuration.orientation == Configuration.ORIENTATION_LANDSCAPE) "landscape" else "portrait")

    fun screenSize(ctx: Context): Pair<Int, Int> {
        DroidAccessibilityService.instance?.let { return it.screenSize() }
        val dm = android.util.DisplayMetrics()
        @Suppress("DEPRECATION")
        ctx.getSystemService(android.hardware.display.DisplayManager::class.java)
            .getDisplay(android.view.Display.DEFAULT_DISPLAY).getRealMetrics(dm)
        return dm.widthPixels to dm.heightPixels
    }

    fun info(ctx: Context): JSONObject {
        val (w, h) = screenSize(ctx)
        return JSONObject()
            .put("model", Build.MODEL)
            .put("manufacturer", Build.MANUFACTURER)
            .put("sdk", Build.VERSION.SDK_INT)
            .put("release", Build.VERSION.RELEASE)
            .put("agent", Agent.version)
            .put("width", w)
            .put("height", h)
            .put("ready", Readiness.compute(ctx).toJson())
    }

    /** Ethernet is reported as wifi: the spec only knows wifi, mobile and none. */
    fun network(ctx: Context): JSONObject {
        val cm = ctx.getSystemService(ConnectivityManager::class.java)
        val caps = cm.activeNetwork?.let { cm.getNetworkCapabilities(it) }
        val type = when {
            caps == null -> "none"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) || caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "wifi"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "mobile"
            else -> "none"
        }
        val airplane = Settings.Global.getInt(ctx.contentResolver, Settings.Global.AIRPLANE_MODE_ON, 0) != 0
        val metered = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false
        return JSONObject().put("type", type).put("airplane", airplane).put("metered", metered)
    }

    suspend fun clipboard(ctx: Context, p: JSONObject): JSONObject {
        val text = if (p.isNull("text")) null else p.opt("text") as? String
        if (text != null) {
            withContext(Dispatchers.Main) {
                ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("droidline", text))
            }
            return value(text)
        }
        if (Build.VERSION.SDK_INT < 29) {
            return withContext(Dispatchers.Main) {
                val clip = ctx.getSystemService(ClipboardManager::class.java).primaryClip
                value(if (clip == null || clip.itemCount == 0) "" else clip.getItemAt(0).coerceToText(ctx).toString())
            }
        }
        val kb = DroidKeyboard.current(ctx) ?: throw CmdError("NO_IME")
        return value(kb.readClipboard())
    }
}
