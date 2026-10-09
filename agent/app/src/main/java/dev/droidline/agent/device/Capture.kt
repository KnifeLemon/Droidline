package dev.droidline.agent.device

import android.accessibilityservice.AccessibilityService
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.ImageReader
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import androidx.annotation.RequiresApi
import dev.droidline.agent.Agent
import dev.droidline.agent.a11y.DroidAccessibilityService
import dev.droidline.agent.a11y.UiCommands
import dev.droidline.agent.cmd.CmdError
import dev.droidline.agent.crypto.B64
import dev.droidline.agent.service.AgentService
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.io.ByteArrayOutputStream

/** screenshot and color: the accessibility screenshot on Android 11+, MediaProjection on 9 and 10. */
object Capture {
    @Volatile private var lastShotAt = 0L

    suspend fun bitmap(ctx: Context): Bitmap {
        val svc = UiCommands.svc()
        if (Build.VERSION.SDK_INT >= 30) return a11yShot(svc)
        if (!Projection.ensure(ctx)) throw CmdError.noPermission("capture")
        return Projection.grab(ctx) ?: throw CmdError("TIMEOUT", mapOf("cmd" to "screenshot", "timeout" to 2))
    }

    /** takeScreenshot is rate limited by the system; wait out the interval and retry. */
    @RequiresApi(Build.VERSION_CODES.R)
    private suspend fun a11yShot(svc: DroidAccessibilityService): Bitmap {
        var attempt = 0
        while (true) {
            val gap = System.currentTimeMillis() - lastShotAt
            if (gap < MIN_GAP_MS) delay(MIN_GAP_MS - gap)
            lastShotAt = System.currentTimeMillis()
            try {
                return svc.screenshot()
            } catch (e: DroidAccessibilityService.ScreenshotFailed) {
                when {
                    e.code == AccessibilityService.ERROR_TAKE_SCREENSHOT_INTERVAL_TIME_SHORT && attempt < 5 -> {
                        attempt++
                        delay(400)
                    }
                    e.code == AccessibilityService.ERROR_TAKE_SCREENSHOT_SECURE_WINDOW ->
                        throw CmdError.internal("the screen shows a secure window that cannot be captured")
                    else -> throw CmdError.internal("screenshot failed with code ${e.code}")
                }
            }
        }
    }

    suspend fun screenshot(ctx: Context, p: JSONObject): JSONObject {
        val format = p.optString("format", "jpeg").lowercase()
        if (format != "jpeg" && format != "png") throw CmdError.badArgs("screenshot", "format must be png or jpeg")
        val quality = p.optInt("quality", 80).coerceIn(1, 100)
        val scale = p.optDouble("scale", 1.0).coerceIn(0.1, 1.0)
        val full = bitmap(ctx)
        return withContext(Dispatchers.Default) {
            val bmp = if (scale < 1.0) {
                Bitmap.createScaledBitmap(full, (full.width * scale).toInt().coerceAtLeast(1), (full.height * scale).toInt().coerceAtLeast(1), true)
            } else {
                full
            }
            val out = ByteArrayOutputStream()
            bmp.compress(if (format == "png") Bitmap.CompressFormat.PNG else Bitmap.CompressFormat.JPEG, quality, out)
            JSONObject().put("format", format).put("width", bmp.width).put("height", bmp.height).put("data", B64.std(out.toByteArray()))
        }
    }

    suspend fun color(ctx: Context, p: JSONObject): JSONObject {
        val x = (p.opt("x") as? Number)?.toInt() ?: throw CmdError.badArgs("color", "x must be an integer")
        val y = (p.opt("y") as? Number)?.toInt() ?: throw CmdError.badArgs("color", "y must be an integer")
        val bmp = bitmap(ctx)
        if (x !in 0 until bmp.width || y !in 0 until bmp.height) throw CmdError.badArgs("color", "($x, $y) is outside the ${bmp.width}x${bmp.height} screen")
        return JSONObject().put("value", "#%06X".format(bmp.getPixel(x, y) and 0xFFFFFF))
    }

    private const val MIN_GAP_MS = 350L
}

/** MediaProjection for Android 9 and 10. The user grants it once; it is kept while the service runs. */
object Projection {
    @Volatile private var projection: MediaProjection? = null
    private var reader: ImageReader? = null
    private var display: VirtualDisplay? = null
    private var size = 0 to 0
    @Volatile private var pending: CompletableDeferred<Boolean>? = null
    @Volatile private var askedAt = 0L

    val granted: Boolean get() = projection != null

    /**
     * Asks for screen capture once. A remembered consent comes back within a second; when Android
     * shows its prompt instead, answer NO_PERMISSION soon, because the phone runs one command at a
     * time and a long wait would block the very command that could press Start. The prompt stays
     * up, and the next screenshot after someone accepts it works.
     */
    suspend fun ensure(ctx: Context): Boolean {
        if (granted) return true
        // Reuse a prompt that is still up; ask again if the last one was left unanswered for a minute.
        val fresh = System.currentTimeMillis() - askedAt < PROMPT_REUSE_MS
        val wait = pending?.takeIf { it.isActive && fresh } ?: CompletableDeferred<Boolean>().also {
            pending = it
            askedAt = System.currentTimeMillis()
            withContext(Dispatchers.Main) {
                ctx.startActivity(Intent(ctx, CaptureConsentActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
        }
        return withTimeoutOrNull(CONSENT_WAIT_MS) { wait.await() } ?: false
    }

    private const val CONSENT_WAIT_MS = 5_000L
    private const val PROMPT_REUSE_MS = 60_000L

    fun onResult(ctx: Context, resultCode: Int, data: Intent?) {
        if (resultCode != Activity.RESULT_OK || data == null) {
            pending?.complete(false)
            return
        }
        // Android 10 only hands out a projection to a foreground service of type mediaProjection.
        AgentService.instance?.setProjectionActive(true)
        val mpm = ctx.getSystemService(MediaProjectionManager::class.java)
        val mp = runCatching { mpm.getMediaProjection(resultCode, data) }.getOrNull()
        if (mp == null) {
            AgentService.instance?.setProjectionActive(false)
            pending?.complete(false)
            return
        }
        mp.registerCallback(object : MediaProjection.Callback() {
            override fun onStop() = release()
        }, Handler(Looper.getMainLooper()))
        projection = mp
        Agent.refreshReady()
        pending?.complete(true)
    }

    @Synchronized
    fun release() {
        display?.release()
        reader?.close()
        display = null
        reader = null
        last = null
        projection?.stop()
        projection = null
        AgentService.instance?.setProjectionActive(false)
        Agent.refreshReady()
    }

    @Synchronized
    private fun prepare(ctx: Context) {
        val mp = projection ?: return
        val (w, h) = DeviceCommands.screenSize(ctx)
        if (display != null && size == (w to h)) return
        last = null
        display?.release()
        reader?.close()
        val r = ImageReader.newInstance(w, h, PixelFormat.RGBA_8888, 2)
        display = mp.createVirtualDisplay(
            "droidline", w, h, ctx.resources.displayMetrics.densityDpi,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, r.surface, null, null,
        )
        reader = r
        size = w to h
    }

    /**
     * The virtual display only delivers a frame when the screen changes, so a still screen
     * gets the last frame again after a short wait instead of no frame at all.
     */
    suspend fun grab(ctx: Context): Bitmap? {
        prepare(ctx)
        val start = System.currentTimeMillis()
        while (System.currentTimeMillis() - start < 2000) {
            val img = synchronized(this) { reader?.acquireLatestImage() }
            if (img != null) {
                img.use {
                    val plane = it.planes[0]
                    val padding = plane.rowStride - plane.pixelStride * it.width
                    val raw = Bitmap.createBitmap(it.width + padding / plane.pixelStride, it.height, Bitmap.Config.ARGB_8888)
                    raw.copyPixelsFromBuffer(plane.buffer)
                    val frame = if (padding == 0) raw else Bitmap.createBitmap(raw, 0, 0, it.width, it.height)
                    last = frame
                    return frame
                }
            }
            val previous = last
            if (previous != null && System.currentTimeMillis() - start >= STILL_MS) return previous
            delay(50)
        }
        return null
    }

    @Volatile private var last: Bitmap? = null
    private const val STILL_MS = 300L
}

/** Asks once for screen capture permission on Android 9 and 10. */
class CaptureConsentActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) {
            @Suppress("DEPRECATION")
            startActivityForResult(getSystemService(MediaProjectionManager::class.java).createScreenCaptureIntent(), 1)
        }
    }

    @Deprecated("Activity result API is not available on the framework Activity")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        Projection.onResult(applicationContext, resultCode, data)
        finish()
    }
}
