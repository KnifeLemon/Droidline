package dev.droidline.agent.device

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.provider.CallLog
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import org.json.JSONArray
import org.json.JSONObject

/** Reads the phone app's call history. Needs READ_CALL_LOG, which the user grants. */
object Calls {
    fun granted(ctx: Context) =
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_CALL_LOG) == PackageManager.PERMISSION_GRANTED

    private fun typeName(t: Int) = when (t) {
        CallLog.Calls.INCOMING_TYPE, CallLog.Calls.ANSWERED_EXTERNALLY_TYPE -> "incoming"
        CallLog.Calls.OUTGOING_TYPE -> "outgoing"
        CallLog.Calls.MISSED_TYPE -> "missed"
        CallLog.Calls.VOICEMAIL_TYPE -> "voicemail"
        CallLog.Calls.REJECTED_TYPE -> "rejected"
        CallLog.Calls.BLOCKED_TYPE -> "blocked"
        else -> "other"
    }

    fun read(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("calls")
        val type = p.optString("type", "all")
        if (type !in setOf("all", "incoming", "outgoing", "missed", "rejected", "blocked")) {
            throw CmdError.badArgs("calls", "type must be all, incoming, outgoing, missed, rejected or blocked")
        }
        val max = p.optInt("max", 20)
        if (max < 1 || max > 500) throw CmdError.badArgs("calls", "max must be between 1 and 500")
        val within = p.optDouble("within", 0.0)
        if (within < 0) throw CmdError.badArgs("calls", "within must be 0 or more")
        val since = if (within > 0) System.currentTimeMillis() - (within * 1000).toLong() else 0L
        val number = clean(p.optString("number")).lowercase()

        val out = JSONArray()
        val cols = arrayOf(CallLog.Calls._ID, CallLog.Calls.NUMBER, CallLog.Calls.CACHED_NAME, CallLog.Calls.TYPE, CallLog.Calls.DATE, CallLog.Calls.DURATION)
        ctx.contentResolver.query(CallLog.Calls.CONTENT_URI, cols, "${CallLog.Calls.DATE} >= $since", null, "${CallLog.Calls.DATE} DESC")?.use { c ->
            while (out.length() < max && c.moveToNext()) {
                val t = typeName(c.getInt(3))
                if (type != "all" && t != type) continue
                val num = c.getString(1).orEmpty()
                val name = c.getString(2).orEmpty()
                if (number.isNotEmpty() && !clean(num).contains(number) && !clean(name).lowercase().contains(number)) continue
                out.put(JSONObject()
                    .put("id", c.getLong(0))
                    .put("number", num)
                    .put("name", name)
                    .put("type", t)
                    .put("time", c.getLong(4))
                    .put("duration", c.getLong(5)))
            }
        }
        return JSONObject().put("value", out)
    }

    private fun clean(s: String) = s.replace(" ", "").replace("-", "")
}
