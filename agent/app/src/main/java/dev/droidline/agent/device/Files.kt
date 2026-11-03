package dev.droidline.agent.device

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.os.Environment
import android.util.Base64
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** Lists and reads files in shared storage. Needs All files access on Android 11+, or READ_EXTERNAL_STORAGE before. */
object Files {
    private const val MAX_PULL = 32L * 1024 * 1024

    fun granted(ctx: Context) =
        if (Build.VERSION.SDK_INT >= 30) Environment.isExternalStorageManager()
        else ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED

    private fun root(): File = Environment.getExternalStorageDirectory().canonicalFile

    /** Resolves a path given relative to shared storage, or absolute inside it, and refuses anything outside. */
    private fun resolve(cmd: String, path: String): File {
        val root = root()
        val f = (if (path.startsWith("/")) File(path) else File(root, path)).canonicalFile
        if (f != root && !f.path.startsWith(root.path + File.separator)) {
            throw CmdError.badArgs(cmd, "$path is outside shared storage")
        }
        return f
    }

    private fun relative(f: File) = f.path.removePrefix(root().path).trimStart('/')

    fun list(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("files")
        val path = p.optString("path")
        val sort = p.optString("sort", "name")
        if (sort !in setOf("name", "time")) throw CmdError.badArgs("files", "sort must be name or time")
        val max = p.optInt("max", 200)
        if (max < 1 || max > 1000) throw CmdError.badArgs("files", "max must be between 1 and 1000")
        val dir = resolve("files", path)
        if (!dir.isDirectory) throw CmdError.badArgs("files", "no folder $path")
        val entries = dir.listFiles().orEmpty().toList().let { all ->
            if (sort == "time") all.sortedByDescending { it.lastModified() } else all.sortedBy { it.name.lowercase() }
        }
        val out = JSONArray()
        for (f in entries.take(max)) {
            out.put(JSONObject()
                .put("name", f.name)
                .put("path", relative(f))
                .put("size", if (f.isFile) f.length() else 0)
                .put("time", f.lastModified())
                .put("dir", f.isDirectory))
        }
        return JSONObject().put("value", out)
    }

    fun pull(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("files")
        val path = p.optString("file")
        val f = resolve("pull", path)
        if (!f.isFile) throw CmdError.badArgs("pull", "no file $path")
        if (f.length() > MAX_PULL) throw CmdError.badArgs("pull", "$path is larger than 32 MB")
        val data = Base64.encodeToString(f.readBytes(), Base64.NO_WRAP)
        return JSONObject().put("name", f.name).put("size", f.length()).put("data", data)
    }
}
