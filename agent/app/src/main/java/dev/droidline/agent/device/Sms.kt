package dev.droidline.agent.device

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.Telephony
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import org.json.JSONArray
import org.json.JSONObject

/** Reads stored SMS and the text parts of MMS through the Telephony provider. Needs READ_SMS, which the user grants. */
object Sms {
    private const val INBOX = 1
    private const val SENT = 2
    // PduHeaders.FROM and PduHeaders.TO, which are not public API.
    private const val ADDR_FROM = 137
    private const val ADDR_TO = 151

    fun granted(ctx: Context) =
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_SMS) == PackageManager.PERMISSION_GRANTED

    private class Filter(val box: String, address: String, contains: String, val since: Long, val max: Int) {
        val address = clean(address)
        val contains = contains.lowercase()

        fun keep(address: String, text: String) =
            (this.address.isEmpty() || clean(address).contains(this.address)) &&
                (contains.isEmpty() || text.lowercase().contains(contains))
    }

    fun read(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("sms")
        val box = p.optString("box", "inbox")
        if (box !in setOf("inbox", "sent", "all")) throw CmdError.badArgs("sms", "box must be inbox, sent or all")
        val max = p.optInt("max", 20)
        if (max < 1 || max > 500) throw CmdError.badArgs("sms", "max must be between 1 and 500")
        val within = p.optDouble("within", 0.0)
        if (within < 0) throw CmdError.badArgs("sms", "within must be 0 or more")
        val since = if (within > 0) System.currentTimeMillis() - (within * 1000).toLong() else 0L
        val f = Filter(box, p.optString("address"), p.optString("contains"), since, max)

        val all = (sms(ctx, f) + mms(ctx, f)).sortedByDescending { it.getLong("time") }.take(max)
        return JSONObject().put("value", JSONArray(all))
    }

    private fun boxSelection(f: Filter, column: String) = when (f.box) {
        "inbox" -> "$column = $INBOX"
        "sent" -> "$column = $SENT"
        else -> "$column IN ($INBOX, $SENT)"
    }

    private fun sms(ctx: Context, f: Filter): List<JSONObject> {
        val out = ArrayList<JSONObject>()
        val cols = arrayOf(Telephony.Sms._ID, Telephony.Sms.ADDRESS, Telephony.Sms.BODY, Telephony.Sms.DATE, Telephony.Sms.TYPE, Telephony.Sms.READ)
        val where = boxSelection(f, Telephony.Sms.TYPE) + " AND ${Telephony.Sms.DATE} >= ${f.since}"
        ctx.contentResolver.query(Telephony.Sms.CONTENT_URI, cols, where, null, "${Telephony.Sms.DATE} DESC")?.use { c ->
            while (out.size < f.max && c.moveToNext()) {
                val address = c.getString(1).orEmpty()
                val text = c.getString(2).orEmpty()
                if (!f.keep(address, text)) continue
                out += message(c.getLong(0), address, text, c.getLong(3), c.getInt(4), c.getInt(5) == 1, mms = false)
            }
        }
        return out
    }

    private fun mms(ctx: Context, f: Filter): List<JSONObject> {
        val out = ArrayList<JSONObject>()
        val cols = arrayOf(Telephony.Mms._ID, Telephony.Mms.DATE, Telephony.Mms.MESSAGE_BOX, Telephony.Mms.READ)
        // MMS dates are in seconds.
        val where = boxSelection(f, Telephony.Mms.MESSAGE_BOX) + " AND ${Telephony.Mms.DATE} >= ${f.since / 1000}"
        ctx.contentResolver.query(Telephony.Mms.CONTENT_URI, cols, where, null, "${Telephony.Mms.DATE} DESC")?.use { c ->
            while (out.size < f.max && c.moveToNext()) {
                val id = c.getLong(0)
                val boxValue = c.getInt(2)
                val text = mmsText(ctx, id)
                val address = mmsAddress(ctx, id, if (boxValue == INBOX) ADDR_FROM else ADDR_TO)
                if (!f.keep(address, text)) continue
                out += message(id, address, text, c.getLong(1) * 1000, boxValue, c.getInt(3) == 1, mms = true)
            }
        }
        return out
    }

    private fun mmsText(ctx: Context, id: Long): String {
        val parts = ArrayList<String>()
        ctx.contentResolver.query(Uri.parse("content://mms/part"), arrayOf("ct", "text"), "mid = ?", arrayOf(id.toString()), null)?.use { c ->
            while (c.moveToNext()) {
                if (c.getString(0) == "text/plain") c.getString(1)?.let { parts += it }
            }
        }
        return parts.joinToString("\n")
    }

    private fun mmsAddress(ctx: Context, id: Long, type: Int): String {
        val found = ArrayList<String>()
        ctx.contentResolver.query(Uri.parse("content://mms/$id/addr"), arrayOf("address"), "type = $type", null, null)?.use { c ->
            while (c.moveToNext()) {
                c.getString(0)?.takeIf { it.isNotBlank() && it != "insert-address-token" }?.let { found += it }
            }
        }
        return found.joinToString(", ")
    }

    private fun message(id: Long, address: String, text: String, time: Long, box: Int, read: Boolean, mms: Boolean) = JSONObject()
        .put("id", id)
        .put("address", address)
        .put("text", text)
        .put("time", time)
        .put("box", if (box == SENT) "sent" else "inbox")
        .put("read", read)
        .put("mms", mms)

    private fun clean(s: String) = s.replace(" ", "").replace("-", "")
}
