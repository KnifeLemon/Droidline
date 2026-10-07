package dev.droidline.agent.ui

import android.Manifest
import android.annotation.SuppressLint
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import android.view.inputmethod.InputMethodManager
import androidx.core.content.ContextCompat
import dev.droidline.agent.a11y.DroidAccessibilityService
import dev.droidline.agent.notif.DroidNotificationListener

/** Intents for the exact settings screen behind each onboarding row, with fallbacks. */
object SystemScreens {
    private fun Context.tryStart(vararg intents: Intent) {
        for (i in intents) {
            i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (runCatching { startActivity(i) }.isSuccess) return
        }
    }

    fun accessibility(ctx: Context) {
        val cn = ComponentName(ctx, DroidAccessibilityService::class.java).flattenToString()
        ctx.tryStart(
            Intent("android.settings.ACCESSIBILITY_DETAILS_SETTINGS").putExtra(Intent.EXTRA_COMPONENT_NAME, cn),
            Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS),
        )
    }

    fun appInfo(ctx: Context) {
        ctx.tryStart(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", ctx.packageName, null)))
    }

    fun keyboardSettings(ctx: Context) {
        ctx.tryStart(Intent(Settings.ACTION_INPUT_METHOD_SETTINGS))
    }

    fun keyboardPicker(ctx: Context) {
        ctx.getSystemService(InputMethodManager::class.java).showInputMethodPicker()
    }

    @SuppressLint("BatteryLife")
    fun batteryOptimization(ctx: Context) {
        ctx.tryStart(
            Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.fromParts("package", ctx.packageName, null)),
            Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS),
        )
    }

    fun notificationAccess(ctx: Context) {
        val detail = if (Build.VERSION.SDK_INT >= 30) {
            Intent(Settings.ACTION_NOTIFICATION_LISTENER_DETAIL_SETTINGS)
                .putExtra(Settings.EXTRA_NOTIFICATION_LISTENER_COMPONENT_NAME, DroidNotificationListener.component(ctx).flattenToString())
        } else {
            null
        }
        ctx.tryStart(*listOfNotNull(detail, Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS)).toTypedArray())
    }

    fun ignoringBatteryOptimizations(ctx: Context): Boolean =
        ctx.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(ctx.packageName)

    fun canPostNotifications(ctx: Context): Boolean =
        Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
}
