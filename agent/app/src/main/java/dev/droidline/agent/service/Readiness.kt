package dev.droidline.agent.service

import android.content.Context
import android.net.VpnService
import android.os.Build
import dev.droidline.agent.a11y.DroidAccessibilityService
import dev.droidline.agent.device.Projection
import dev.droidline.agent.ime.DroidKeyboard
import dev.droidline.agent.notif.DroidNotificationListener
import org.json.JSONObject

data class Ready(
    val a11y: Boolean = false,
    val ime: Boolean = false,
    val vpn: Boolean = false,
    val notif: Boolean = false,
    val capture: Boolean = false,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("a11y", a11y)
        .put("ime", ime)
        .put("vpn", vpn)
        .put("notif", notif)
        .put("capture", capture)
}

object Readiness {
    fun compute(ctx: Context) = Ready(
        a11y = DroidAccessibilityService.instance != null,
        ime = DroidKeyboard.isEnabled(ctx) && DroidKeyboard.isSelected(ctx),
        vpn = runCatching { VpnService.prepare(ctx) == null }.getOrDefault(false),
        notif = DroidNotificationListener.isEnabled(ctx),
        capture = Build.VERSION.SDK_INT >= 30 || Projection.granted,
    )
}
