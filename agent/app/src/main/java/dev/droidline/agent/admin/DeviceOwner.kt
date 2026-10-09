package dev.droidline.agent.admin

import android.app.admin.DeviceAdminReceiver
import android.app.admin.DevicePolicyManager
import android.content.ComponentName
import android.content.Context
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeoutOrNull

/** Target of `dpm set-device-owner`. It asks for no policies; owning the device is what matters. */
class DroidAdminReceiver : DeviceAdminReceiver()

/** Optional device owner mode. clear_data works only while it is set. */
object DeviceOwner {
    fun admin(ctx: Context) = ComponentName(ctx, DroidAdminReceiver::class.java)

    private fun dpm(ctx: Context) = ctx.getSystemService(DevicePolicyManager::class.java)

    fun isOwner(ctx: Context): Boolean = runCatching { dpm(ctx).isDeviceOwnerApp(ctx.packageName) }.getOrDefault(false)

    /** true once the app's data is gone; false when Android refused. */
    suspend fun clearData(ctx: Context, pkg: String): Boolean {
        val done = CompletableDeferred<Boolean>()
        val started = runCatching {
            dpm(ctx).clearApplicationUserData(admin(ctx), pkg, ctx.mainExecutor) { _, ok -> done.complete(ok) }
        }.isSuccess
        if (!started) return false
        return withTimeoutOrNull(CLEAR_WAIT_MS) { done.await() } ?: false
    }

    /** Gives up device owner mode, so the app can be uninstalled normally again. */
    @Suppress("DEPRECATION")
    fun release(ctx: Context) {
        runCatching { dpm(ctx).clearDeviceOwnerApp(ctx.packageName) }
    }

    private const val CLEAR_WAIT_MS = 15_000L
}
