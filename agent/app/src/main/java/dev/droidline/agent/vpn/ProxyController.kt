package dev.droidline.agent.vpn

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import dev.droidline.agent.cmd.CmdError
import dev.droidline.agent.device.Apps
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONArray
import org.json.JSONObject
import java.net.Socket
import java.util.concurrent.TimeUnit
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory

/** proxy and proxy_check. One upstream at a time. */
object ProxyController {
    private const val IP_HOST = "api.ipify.org"

    @Volatile private var upstream: Upstream? = null
    @Volatile private var local: LocalProxy? = null
    @Volatile private var apps: List<String> = emptyList()

    private fun isActive() = Build.VERSION.SDK_INT >= 29 && ProxyVpnService.active != null && local != null

    suspend fun proxy(ctx: Context, p: JSONObject): JSONObject {
        if (Build.VERSION.SDK_INT < 29) {
            throw CmdError("UNSUPPORTED", mapOf("cmd" to "proxy", "min_release" to "10", "release" to Build.VERSION.RELEASE))
        }
        val url = if (p.isNull("url")) null else p.optString("url").trim()
        if (url == null || url.isEmpty() || url.equals("off", ignoreCase = true)) {
            stop(ctx)
            return JSONObject().put("active", false).put("apps", JSONArray())
        }
        if (url.startsWith("@")) throw CmdError.badArgs("proxy", "saved profiles like $url are resolved by the PC server")
        val targets = when (val a = p.opt("app")) {
            is String -> listOf(a)
            is JSONArray -> (0 until a.length()).map { a.getString(it) }
            else -> throw CmdError.badArgs("proxy", "app is required when turning the proxy on")
        }.filter { it.isNotBlank() }.distinct()
        if (targets.isEmpty()) throw CmdError.badArgs("proxy", "app is required when turning the proxy on")
        for (pkg in targets) Apps.requireInstalled(ctx, pkg)
        if (VpnService.prepare(ctx) != null) throw CmdError.noPermission("vpn")
        val up = try {
            Upstream.parse(url)
        } catch (e: IllegalArgumentException) {
            throw CmdError.badArgs("proxy", e.message ?: "bad proxy URL")
        }

        withContext(Dispatchers.IO) {
            try {
                up.connect(IP_HOST, 443).close()
            } catch (e: ProxyFailure) {
                throw CmdError("PROXY_FAILED", mapOf("reason" to e.reason))
            }
        }

        stopLocal()
        val server = withContext(Dispatchers.IO) { LocalProxy(up).start() }
        val req = ProxyVpnService.Request(server.port, targets)
        ProxyVpnService.pending = req
        ctx.startService(Intent(ctx, ProxyVpnService::class.java))
        // The service answers null on success, so map it before the timeout's own null.
        val outcome = withTimeoutOrNull(5000) { req.result.await() ?: "ok" } ?: "timeout"
        when {
            outcome == "permission" -> { server.close(); throw CmdError.noPermission("vpn") }
            outcome.startsWith("app:") -> { server.close(); Apps.requireInstalled(ctx, outcome.removePrefix("app:")) }
            outcome == "timeout" -> { server.close(); stop(ctx); throw CmdError("TIMEOUT", mapOf("cmd" to "proxy", "timeout" to 5)) }
        }
        local = server
        upstream = up
        apps = targets
        return JSONObject().put("active", true).put("apps", JSONArray(targets))
    }

    fun stop(ctx: Context) {
        stopLocal()
        upstream = null
        apps = emptyList()
        if (Build.VERSION.SDK_INT >= 29 && ProxyVpnService.active != null) {
            ctx.startService(Intent(ctx, ProxyVpnService::class.java).setAction(ProxyVpnService.ACTION_STOP))
        }
    }

    private fun stopLocal() {
        local?.close()
        local = null
    }

    fun onRevoked() {
        stopLocal()
        upstream = null
        apps = emptyList()
    }

    suspend fun check(p: JSONObject): JSONObject = withContext(Dispatchers.IO) {
        val app = p.optString("app").ifEmpty { null }
        val up = upstream
        val active = isActive() && up != null && (app == null || app in apps)
        val out = JSONObject().put("active", active).put("upstream", if (active) up!!.display else "")
        try {
            out.put("ip", if (active) ipVia(up!!) else ipDirect()).put("error", "")
        } catch (e: ProxyFailure) {
            out.put("ip", "").put("error", e.reason)
        } catch (e: Exception) {
            out.put("ip", "").put("error", e.message ?: "request failed")
        }
        out
    }

    private fun ipDirect(): String {
        val client = OkHttpClient.Builder().callTimeout(10, TimeUnit.SECONDS).build()
        client.newCall(Request.Builder().url("https://$IP_HOST?format=json").build()).execute().use { r ->
            return JSONObject(r.body?.string().orEmpty()).optString("ip")
        }
    }

    private fun ipVia(up: Upstream): String {
        val raw: Socket = up.connect(IP_HOST, 443)
        raw.use {
            val ssl = (SSLSocketFactory.getDefault() as SSLSocketFactory).createSocket(raw, IP_HOST, 443, true) as SSLSocket
            ssl.soTimeout = 10_000
            ssl.startHandshake()
            if (!HttpsURLConnection.getDefaultHostnameVerifier().verify(IP_HOST, ssl.session)) {
                throw ProxyFailure("protocol", "certificate does not match $IP_HOST")
            }
            // HTTP/1.0 keeps the reply unchunked.
            ssl.outputStream.write("GET /?format=json HTTP/1.0\r\nHost: $IP_HOST\r\nConnection: close\r\n\r\n".toByteArray())
            ssl.outputStream.flush()
            val body = ssl.inputStream.readBytes().toString(Charsets.UTF_8)
            val start = body.indexOf('{')
            val end = body.indexOf('}', start)
            if (start < 0 || end < 0) throw ProxyFailure("protocol", "unexpected reply from $IP_HOST")
            return JSONObject(body.substring(start, end + 1)).optString("ip")
        }
    }
}
