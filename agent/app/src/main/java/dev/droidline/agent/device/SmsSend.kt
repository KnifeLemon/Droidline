package dev.droidline.agent.device

import android.Manifest
import android.app.Activity
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.os.Build
import android.telephony.SmsManager
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.util.UUID

/** Sends a text message from the default SIM. Needs SEND_SMS, which the user grants. */
object SmsSend {
    private const val WAIT_MS = 60_000L

    fun granted(ctx: Context) =
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.SEND_SMS) == PackageManager.PERMISSION_GRANTED

    private fun reason(code: Int) = when (code) {
        SmsManager.RESULT_ERROR_GENERIC_FAILURE -> "generic_failure"
        SmsManager.RESULT_ERROR_RADIO_OFF -> "radio_off"
        SmsManager.RESULT_ERROR_NULL_PDU -> "null_pdu"
        SmsManager.RESULT_ERROR_NO_SERVICE -> "no_service"
        else -> "error_$code"
    }

    suspend fun send(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("sms_send")
        val number = p.optString("number").trim()
        val text = p.optString("text")
        if (number.isEmpty()) throw CmdError.badArgs("sms_send", "number must not be empty")
        if (text.isEmpty()) throw CmdError.badArgs("sms_send", "text must not be empty")

        val sm = if (Build.VERSION.SDK_INT >= 31) ctx.getSystemService(SmsManager::class.java) else @Suppress("DEPRECATION") SmsManager.getDefault()
        val parts = sm.divideMessage(text)
        val action = "dev.droidline.agent.SMS_SENT." + UUID.randomUUID()
        val results = Channel<Int>(Channel.UNLIMITED)
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(c: Context, i: Intent) {
                results.trySend(resultCode)
            }
        }
        ContextCompat.registerReceiver(ctx, receiver, IntentFilter(action), ContextCompat.RECEIVER_NOT_EXPORTED)
        try {
            val sent = ArrayList(parts.indices.map {
                PendingIntent.getBroadcast(ctx, it, Intent(action).setPackage(ctx.packageName), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_ONE_SHOT)
            })
            sm.sendMultipartTextMessage(number, null, parts, sent, null)
            withTimeoutOrNull(WAIT_MS) {
                repeat(parts.size) {
                    val code = results.receive()
                    if (code != Activity.RESULT_OK) throw CmdError("SEND_FAILED", mapOf("reason" to reason(code)))
                }
            } ?: throw CmdError("SEND_FAILED", mapOf("reason" to "no answer from the network within ${WAIT_MS / 1000} s"))
        } finally {
            ctx.unregisterReceiver(receiver)
        }
        return JSONObject().put("parts", parts.size)
    }
}
