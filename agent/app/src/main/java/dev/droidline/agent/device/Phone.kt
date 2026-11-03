package dev.droidline.agent.device

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.telecom.TelecomManager
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** Starts and ends phone calls. Needs CALL_PHONE and ANSWER_PHONE_CALLS, which the user grants together. */
object Phone {
    val permissions = arrayOf(Manifest.permission.CALL_PHONE, Manifest.permission.ANSWER_PHONE_CALLS)

    fun granted(ctx: Context) = permissions.all {
        ContextCompat.checkSelfPermission(ctx, it) == PackageManager.PERMISSION_GRANTED
    }

    suspend fun dial(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("phone")
        val number = p.optString("number").trim()
        if (number.isEmpty()) throw CmdError.badArgs("dial", "number must not be empty")
        val intent = Intent(Intent.ACTION_CALL, Uri.fromParts("tel", number, null)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            withContext(Dispatchers.Main) { ctx.startActivity(intent) }
        } catch (e: SecurityException) {
            throw CmdError.noPermission("phone")
        }
        return JSONObject()
    }

    @SuppressLint("MissingPermission")
    fun hangUp(ctx: Context): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("phone")
        val tm = ctx.getSystemService(TelecomManager::class.java)
        // Deprecated for in-call apps, but still the way for any other app to end a call.
        @Suppress("DEPRECATION")
        val ended = tm.endCall()
        return JSONObject().put("value", ended)
    }
}
