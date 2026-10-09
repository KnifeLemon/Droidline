package dev.droidline.agent.a11y

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.app.Notification
import android.content.ComponentName
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Path
import android.graphics.Rect
import android.hardware.display.DisplayManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.DisplayMetrics
import android.view.Display
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import android.view.inputmethod.InputMethodManager
import androidx.annotation.RequiresApi
import dev.droidline.agent.Agent
import kotlinx.coroutines.suspendCancellableCoroutine
import org.json.JSONObject
import java.util.concurrent.ConcurrentHashMap
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

class DroidAccessibilityService : AccessibilityService() {
    private val main = Handler(Looper.getMainLooper())
    private val activityCache = ConcurrentHashMap<String, Boolean>()
    private val lastActivity = ConcurrentHashMap<String, String>()
    private val changedAt = ConcurrentHashMap<String, Long>()
    @Volatile private var windowsChangedAt = 0L
    private var imePackages: Set<String> = emptySet()
    private var lastScreen: Pair<String, String>? = null
    private val screenCheck = Runnable { emitScreenIfChanged() }

    override fun onServiceConnected() {
        instance = this
        imePackages = runCatching {
            getSystemService(InputMethodManager::class.java).inputMethodList.map { it.packageName }.toSet()
        }.getOrDefault(emptySet())
        Agent.refreshReady()
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent) {
        if (Recorder.on) Recorder.onEvent(this, event)
        when (event.eventType) {
            AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED -> {
                noteChange(event)
                onWindowState(event)
            }
            AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, AccessibilityEvent.TYPE_VIEW_SCROLLED -> noteChange(event)
            AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED -> onNotificationState(event)
            AccessibilityEvent.TYPE_WINDOWS_CHANGED -> {
                windowsChangedAt = System.currentTimeMillis()
                scheduleScreenCheck()
            }
            else -> Unit
        }
    }

    private fun noteChange(event: AccessibilityEvent) {
        val pkg = event.packageName?.toString() ?: return
        changedAt[pkg] = System.currentTimeMillis()
    }

    /** When the app [pkg] last drew something new, or windows came or went; wait_idle counts from here. */
    fun lastChangeAt(pkg: String): Long = maxOf(changedAt[pkg] ?: 0L, windowsChangedAt)

    override fun onInterrupt() = Unit

    override fun onUnbind(intent: Intent?): Boolean {
        if (instance === this) instance = null
        Agent.refreshReady()
        return super.onUnbind(intent)
    }

    override fun onDestroy() {
        if (instance === this) instance = null
        main.removeCallbacks(screenCheck)
        super.onDestroy()
    }

    private fun onWindowState(event: AccessibilityEvent) {
        val pkg = event.packageName?.toString() ?: return
        val cls = event.className?.toString() ?: return
        val activity = isActivity(pkg, cls)
        // Skip keyboard windows, but not activities of a keyboard's app (Droidline is one).
        if (pkg in imePackages && !activity) return
        if (activity) lastActivity[pkg] = shortActivity(pkg, cls)
        scheduleScreenCheck()
    }

    private fun isActivity(pkg: String, cls: String): Boolean = activityCache.getOrPut("$pkg/$cls") {
        runCatching { packageManager.getActivityInfo(ComponentName(pkg, cls), 0) }.isSuccess
    }

    private fun scheduleScreenCheck() {
        main.removeCallbacks(screenCheck)
        main.postDelayed(screenCheck, SCREEN_DEBOUNCE_MS)
    }

    private fun emitScreenIfChanged() {
        val now = current()
        if (now.first.isEmpty() || now == lastScreen) return
        lastScreen = now
        Agent.emit(JSONObject().put("event", "screen").put("package", now.first).put("activity", now.second))
    }

    private fun onNotificationState(event: AccessibilityEvent) {
        if (event.parcelableData is Notification) return
        val text = event.text.joinToString(" ") { it.toString() }.trim()
        if (text.isEmpty()) return
        lastToast = text
        lastToastAt = System.currentTimeMillis()
        val pkg = event.packageName?.toString().orEmpty()
        Agent.emit(JSONObject().put("event", "toast").put("package", pkg).put("text", text))
    }

    /** Foreground package from the top application window, and the last activity seen for it. */
    fun current(): Pair<String, String> {
        val app = appWindows().firstNotNullOfOrNull { w -> w.root?.packageName?.toString() }
        if (app != null) return app to (lastActivity[app] ?: "")
        // A system window such as the open shade has no activity; an old one of the same package would mislead.
        return (rootInActiveWindow?.packageName?.toString() ?: "") to ""
    }

    /**
     * current() for command threads: right after an app switch the new window
     * can have no root yet, so wait up to 1.5 s for one instead of answering "".
     */
    fun currentSettled(): Pair<String, String> {
        var now = current()
        val deadline = System.currentTimeMillis() + 1500
        while (now.first.isEmpty() && System.currentTimeMillis() < deadline) {
            Thread.sleep(100)
            now = current()
        }
        return now
    }

    fun screenText(): String {
        val (pkg, act) = currentSettled()
        return if (act.isEmpty()) pkg else "$pkg/$act"
    }

    private fun appWindows(): List<AccessibilityWindowInfo> =
        runCatching { windows }.getOrDefault(emptyList())
            .filter { it.type == AccessibilityWindowInfo.TYPE_APPLICATION }
            .sortedByDescending { it.layer }

    /** Roots that selectors search: application windows top first, plus an active system window such as the shade. */
    fun selectorRoots(): List<UiNode> {
        val all = runCatching { windows }.getOrDefault(emptyList()).sortedByDescending { it.layer }
        val picked = all.filter {
            it.type == AccessibilityWindowInfo.TYPE_APPLICATION ||
                (it.type == AccessibilityWindowInfo.TYPE_SYSTEM && it.isActive)
        }
        val roots = picked.mapNotNull { it.root }.ifEmpty { listOfNotNull(rootInActiveWindow) }
        val budget = intArrayOf(MAX_NODES)
        return roots.map { build(it, 0, budget) }
    }

    fun allWindowRoots(): List<UiNode> {
        val budget = intArrayOf(MAX_NODES)
        val roots = runCatching { windows }.getOrDefault(emptyList()).sortedByDescending { it.layer }.mapNotNull { it.root }
        return roots.ifEmpty { listOfNotNull(rootInActiveWindow) }.map { build(it, 0, budget) }
    }

    fun isKeyboardPackage(pkg: String): Boolean = pkg in imePackages

    /** The tree under one platform node, for the recorder. */
    fun snapshotOf(info: AccessibilityNodeInfo): UiNode = build(info, 0, intArrayOf(MAX_NODES))

    fun keyboardShown(): Boolean =
        runCatching { windows }.getOrDefault(emptyList()).any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }

    private fun build(info: AccessibilityNodeInfo, depth: Int, budget: IntArray): UiNode {
        budget[0]--
        val r = Rect().also { info.getBoundsInScreen(it) }
        val kids = ArrayList<UiNode>()
        if (depth < MAX_DEPTH) {
            for (i in 0 until info.childCount) {
                if (budget[0] <= 0) break
                val c = info.getChild(i) ?: continue
                kids += build(c, depth + 1, budget)
            }
        }
        return UiNode(
            text = info.text?.toString().orEmpty(),
            id = info.viewIdResourceName.orEmpty(),
            desc = info.contentDescription?.toString().orEmpty(),
            cls = info.className?.toString().orEmpty(),
            pkg = info.packageName?.toString().orEmpty(),
            bounds = Bounds(r.left, r.top, r.right, r.bottom),
            clickable = info.isClickable,
            longClickable = info.isLongClickable,
            checkable = info.isCheckable,
            checked = checkedOf(info),
            enabled = info.isEnabled,
            focused = info.isFocused,
            selected = info.isSelected,
            scrollable = info.isScrollable,
            editable = info.isEditable,
            password = info.isPassword,
            visible = info.isVisibleToUser,
            children = kids,
            handle = info,
        )
    }

    private fun checkedOf(info: AccessibilityNodeInfo): Boolean =
        if (Build.VERSION.SDK_INT >= 36) info.checked == AccessibilityNodeInfo.CHECKED_STATE_TRUE
        else @Suppress("DEPRECATION") info.isChecked

    fun screenSize(): Pair<Int, Int> {
        val dm = DisplayMetrics()
        @Suppress("DEPRECATION")
        getSystemService(DisplayManager::class.java).getDisplay(Display.DEFAULT_DISPLAY).getRealMetrics(dm)
        return dm.widthPixels to dm.heightPixels
    }

    suspend fun gesture(path: Path, durationMs: Long): Boolean = suspendCancellableCoroutine { cont ->
        val d = durationMs.coerceIn(1, GestureDescription.getMaxGestureDuration())
        val g = GestureDescription.Builder().addStroke(GestureDescription.StrokeDescription(path, 0, d)).build()
        val accepted = dispatchGesture(g, object : GestureResultCallback() {
            override fun onCompleted(gestureDescription: GestureDescription?) {
                if (cont.isActive) cont.resume(true)
            }

            override fun onCancelled(gestureDescription: GestureDescription?) {
                if (cont.isActive) cont.resume(false)
            }
        }, null)
        if (!accepted && cont.isActive) cont.resume(false)
    }

    suspend fun tapAt(x: Int, y: Int, durationMs: Long = 50): Boolean =
        gesture(Path().apply { moveTo(x.toFloat(), y.toFloat()) }, durationMs)

    suspend fun swipe(x1: Int, y1: Int, x2: Int, y2: Int, durationMs: Long): Boolean =
        gesture(Path().apply { moveTo(x1.toFloat(), y1.toFloat()); lineTo(x2.toFloat(), y2.toFloat()) }, durationMs)

    class ScreenshotFailed(val code: Int) : Exception("screenshot failed with code $code")

    @RequiresApi(Build.VERSION_CODES.R)
    suspend fun screenshot(): Bitmap = suspendCancellableCoroutine { cont ->
        takeScreenshot(Display.DEFAULT_DISPLAY, mainExecutor, object : TakeScreenshotCallback {
            override fun onSuccess(result: ScreenshotResult) {
                val hb = result.hardwareBuffer
                val bmp = runCatching {
                    Bitmap.wrapHardwareBuffer(hb, result.colorSpace)!!.copy(Bitmap.Config.ARGB_8888, false)
                }
                hb.close()
                bmp.onSuccess { if (cont.isActive) cont.resume(it) }
                    .onFailure { if (cont.isActive) cont.resumeWithException(it) }
            }

            override fun onFailure(errorCode: Int) {
                if (cont.isActive) cont.resumeWithException(ScreenshotFailed(errorCode))
            }
        })
    }

    companion object {
        @Volatile var instance: DroidAccessibilityService? = null
            private set

        @Volatile var lastToast: String = ""
            private set

        @Volatile var lastToastAt: Long = 0
            private set

        private const val SCREEN_DEBOUNCE_MS = 300L
        private const val MAX_NODES = 5000
        private const val MAX_DEPTH = 80

        fun shortActivity(pkg: String, cls: String): String =
            if (cls.startsWith("$pkg.")) cls.substring(pkg.length) else cls
    }
}
