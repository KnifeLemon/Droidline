package dev.droidline.agent.vpn

import android.content.Intent
import android.content.pm.PackageManager
import android.net.ProxyInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.CompletableDeferred
import java.io.FileInputStream

/**
 * Per-app VPN whose only job is to hand the target apps an HTTP proxy. Every packet that reaches the TUN
 * is dropped, so an app that ignores the proxy setting loses network access instead of leaking.
 */
class ProxyVpnService : VpnService() {
    private var tun: ParcelFileDescriptor? = null
    private var drain: Thread? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            teardown()
            stopSelf()
            return START_NOT_STICKY
        }
        val req = pending ?: return START_NOT_STICKY
        pending = null
        req.result.complete(establish(req))
        return START_NOT_STICKY
    }

    private fun establish(req: Request): String? {
        teardown()
        // setHttpProxy needs Android 10; ProxyController refuses earlier versions before getting here.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return "permission"
        val b = Builder()
            .setSession("Droidline proxy")
            .addAddress("10.215.0.2", 32)
            .addAddress("fd15:d1d1::2", 128)
            .addRoute("0.0.0.0", 0)
            .addRoute("::", 0)
            .addDnsServer("10.215.0.1")
            .setHttpProxy(ProxyInfo.buildDirectProxy("127.0.0.1", req.port))
            .setBlocking(true)
        for (app in req.apps) {
            try {
                b.addAllowedApplication(app)
            } catch (e: PackageManager.NameNotFoundException) {
                return "app:$app"
            }
        }
        val fd = runCatching { b.establish() }.getOrNull() ?: return "permission"
        tun = fd
        drain = Thread({
            val buf = ByteArray(32 * 1024)
            runCatching { FileInputStream(fd.fileDescriptor).use { while (it.read(buf) >= 0) Unit } }
        }, "droidline-tun").apply { isDaemon = true; start() }
        active = this
        return null
    }

    private fun teardown() {
        runCatching { tun?.close() }
        tun = null
        drain = null
        if (active === this) active = null
    }

    override fun onRevoke() {
        teardown()
        ProxyController.onRevoked()
        super.onRevoke()
    }

    override fun onDestroy() {
        teardown()
        super.onDestroy()
    }

    /** [result] completes with null on success, "permission", or "app:<package>". */
    class Request(val port: Int, val apps: List<String>, val result: CompletableDeferred<String?> = CompletableDeferred())

    companion object {
        const val ACTION_STOP = "dev.droidline.agent.vpn.STOP"

        @Volatile var pending: Request? = null
        @Volatile var active: ProxyVpnService? = null
            private set
    }
}
