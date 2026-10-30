package dev.droidline.agent.device

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.provider.ContactsContract
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import org.json.JSONArray
import org.json.JSONObject

/** Reads saved contacts with their numbers and emails. Needs READ_CONTACTS, which the user grants. */
object Contacts {
    fun granted(ctx: Context) =
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_CONTACTS) == PackageManager.PERMISSION_GRANTED

    fun read(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("contacts")
        val max = p.optInt("max", 50)
        if (max < 1 || max > 500) throw CmdError.badArgs("contacts", "max must be between 1 and 500")
        val query = p.optString("query").lowercase()
        val numberQuery = clean(query)

        val numbers = values(ctx, ContactsContract.CommonDataKinds.Phone.CONTENT_URI, ContactsContract.CommonDataKinds.Phone.NUMBER)
        val emails = values(ctx, ContactsContract.CommonDataKinds.Email.CONTENT_URI, ContactsContract.CommonDataKinds.Email.ADDRESS)
        val out = JSONArray()
        val cols = arrayOf(ContactsContract.Contacts._ID, ContactsContract.Contacts.DISPLAY_NAME_PRIMARY)
        val sort = "${ContactsContract.Contacts.DISPLAY_NAME_PRIMARY} COLLATE LOCALIZED ASC"
        ctx.contentResolver.query(ContactsContract.Contacts.CONTENT_URI, cols, null, null, sort)?.use { c ->
            while (out.length() < max && c.moveToNext()) {
                val id = c.getLong(0)
                val name = c.getString(1).orEmpty()
                val nums = numbers[id].orEmpty()
                val mails = emails[id].orEmpty()
                if (query.isNotEmpty() &&
                    !name.lowercase().contains(query) &&
                    mails.none { it.lowercase().contains(query) } &&
                    (numberQuery.isEmpty() || nums.none { clean(it).contains(numberQuery) })
                ) continue
                out.put(JSONObject().put("id", id).put("name", name).put("numbers", JSONArray(nums)).put("emails", JSONArray(mails)))
            }
        }
        return JSONObject().put("value", out)
    }

    /** Distinct values of one data kind, keyed by contact. */
    private fun values(ctx: Context, uri: android.net.Uri, column: String): Map<Long, List<String>> {
        val map = HashMap<Long, LinkedHashSet<String>>()
        ctx.contentResolver.query(uri, arrayOf(ContactsContract.Data.CONTACT_ID, column), null, null, null)?.use { c ->
            while (c.moveToNext()) {
                val v = c.getString(1)?.trim().orEmpty()
                if (v.isNotEmpty()) map.getOrPut(c.getLong(0)) { LinkedHashSet() } += v
            }
        }
        return map.mapValues { it.value.toList() }
    }

    private fun clean(s: String) = s.replace(" ", "").replace("-", "")
}
