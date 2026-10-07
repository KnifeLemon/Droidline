package dev.droidline.agent.device

import android.content.Context
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import dev.droidline.agent.cmd.CmdError
import org.json.JSONArray
import org.json.JSONObject

object Apps {
    fun versionName(ctx: Context, pkg: String): String? = try {
        ctx.packageManager.getPackageInfo(pkg, 0).versionName ?: ""
    } catch (e: PackageManager.NameNotFoundException) {
        null
    }

    fun installedPackages(ctx: Context): List<String> =
        ctx.packageManager.getInstalledApplications(0).map { it.packageName }

    /** Throws APP_NOT_FOUND with up to three similar package names. */
    fun requireInstalled(ctx: Context, pkg: String) {
        if (versionName(ctx, pkg) != null) return
        val similar = similar(pkg, installedPackages(ctx))
        throw CmdError("APP_NOT_FOUND", mapOf("package" to pkg, "similar" to JSONArray(similar)))
    }

    fun similar(pkg: String, candidates: List<String>, limit: Int = 3): List<String> {
        val q = pkg.lowercase()
        val lastPart = q.substringAfterLast('.')
        return candidates.asSequence()
            .map { it to score(q, lastPart, it.lowercase()) }
            .filter { it.second < Int.MAX_VALUE }
            .sortedBy { it.second }
            .take(limit)
            .map { it.first }
            .toList()
    }

    private fun score(q: String, lastPart: String, c: String): Int {
        val d = levenshtein(q, c)
        return when {
            lastPart.length >= 3 && c.contains(lastPart) -> d / 2
            d <= maxOf(3, q.length / 3) -> d
            else -> Int.MAX_VALUE
        }
    }

    fun levenshtein(a: String, b: String): Int {
        var prev = IntArray(b.length + 1) { it }
        var cur = IntArray(b.length + 1)
        for (i in 1..a.length) {
            cur[0] = i
            for (j in 1..b.length) {
                val cost = if (a[i - 1] == b[j - 1]) 0 else 1
                cur[j] = minOf(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost)
            }
            val t = prev
            prev = cur
            cur = t
        }
        return prev[b.length]
    }

    fun list(ctx: Context, includeSystem: Boolean): JSONArray {
        val pm = ctx.packageManager
        val out = JSONArray()
        for (info in pm.getInstalledPackages(0).sortedBy { it.packageName }) {
            val app = info.applicationInfo ?: continue
            val system = app.flags and ApplicationInfo.FLAG_SYSTEM != 0
            if (system && !includeSystem) continue
            out.put(
                JSONObject()
                    .put("package", info.packageName)
                    .put("label", runCatching { pm.getApplicationLabel(app).toString() }.getOrDefault(info.packageName))
                    .put("version", info.versionName ?: "")
                    .put("system", system)
            )
        }
        return out
    }
}
