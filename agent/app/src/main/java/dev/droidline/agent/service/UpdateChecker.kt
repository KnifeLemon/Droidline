package dev.droidline.agent.service

import android.Manifest
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dev.droidline.agent.Agent
import dev.droidline.agent.R
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/** Once a day, looks for a newer GitHub release and posts a notification that links to it. Never installs anything. */
object UpdateChecker {
    private const val URL = "https://api.github.com/repos/KnifeLemon/Droidline/releases/latest"
    private const val DAY_MS = 24 * 60 * 60 * 1000L
    private const val NOTIF_ID = 2

    fun maybeCheck(ctx: Context) {
        val s = Agent.settings
        if (System.currentTimeMillis() - s.lastUpdateCheck < DAY_MS) return
        runCatching { check(ctx) }
        s.lastUpdateCheck = System.currentTimeMillis()
    }

    private fun check(ctx: Context) {
        val client = OkHttpClient.Builder().callTimeout(20, TimeUnit.SECONDS).build()
        val req = Request.Builder()
            .url(URL)
            .header("Accept", "application/vnd.github+json")
            .header("User-Agent", "droidline-agent/${Agent.version}")
            .build()
        val json = client.newCall(req).execute().use { r ->
            if (!r.isSuccessful) return
            JSONObject(r.body?.string().orEmpty())
        }
        val tag = json.optString("tag_name")
        val page = json.optString("html_url").ifEmpty { "https://github.com/KnifeLemon/Droidline/releases" }
        val s = Agent.settings
        if (!isNewer(tag, Agent.version)) {
            s.latestRelease = null
            s.latestReleaseUrl = null
            return
        }
        s.latestRelease = tag
        s.latestReleaseUrl = page
        if (s.notifiedRelease == tag) return
        s.notifiedRelease = tag
        notify(ctx, tag, page)
    }

    private fun notify(ctx: Context, tag: String, page: String) {
        val open = PendingIntent.getActivity(
            ctx, 1, Intent(Intent.ACTION_VIEW, Uri.parse(page)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val n = NotificationCompat.Builder(ctx, AgentService.CHANNEL_UPDATES)
            .setSmallIcon(R.drawable.ic_stat_droidline)
            .setContentTitle(ctx.getString(R.string.update_title, tag))
            .setContentText(ctx.getString(R.string.update_text))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) return
        NotificationManagerCompat.from(ctx).notify(NOTIF_ID, n)
    }

    /** Compares vX.Y.Z tags numerically; anything unparsable is not newer. */
    fun isNewer(tag: String, current: String): Boolean {
        val a = parse(tag) ?: return false
        val b = parse(current) ?: return false
        for (i in 0 until 3) if (a[i] != b[i]) return a[i] > b[i]
        return false
    }

    private fun parse(v: String): IntArray? {
        val m = Regex("^v?(\\d+)\\.(\\d+)\\.(\\d+)").find(v.trim()) ?: return null
        return IntArray(3) { m.groupValues[it + 1].toInt() }
    }
}
