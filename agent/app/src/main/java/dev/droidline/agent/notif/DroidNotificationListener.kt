package dev.droidline.agent.notif

import android.app.ActivityOptions
import android.app.Notification
import android.app.PendingIntent
import android.app.RemoteInput
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import androidx.core.app.NotificationManagerCompat
import dev.droidline.agent.Agent
import dev.droidline.agent.cmd.CmdError
import dev.droidline.agent.link.Session
import org.json.JSONArray
import org.json.JSONObject

class DroidNotificationListener : NotificationListenerService() {
    /** Keys of notifications now showing, so a post with a known key can be reported as an update. */
    private val showing = java.util.concurrent.ConcurrentHashMap.newKeySet<String>()

    override fun onListenerConnected() {
        instance = this
        showing.clear()
        runCatching { activeNotifications?.forEach { showing.add(it.key) } }
        Agent.refreshReady()
    }

    override fun onListenerDisconnected() {
        if (instance === this) instance = null
        Agent.refreshReady()
    }

    override fun onNotificationPosted(sbn: StatusBarNotification) {
        val update = !showing.add(sbn.key)
        if (sbn.packageName !in Agent.settings.notifyAllowlist) return
        if (sbn.notification.flags and Notification.FLAG_GROUP_SUMMARY != 0) return
        val event = toJson(sbn).put("event", "notification").put("update", update)
        Agent.emit(event, Session.KIND_NOTIFICATION)
    }

    override fun onNotificationRemoved(sbn: StatusBarNotification) {
        showing.remove(sbn.key)
    }

    companion object {
        @Volatile var instance: DroidNotificationListener? = null
            private set

        fun isEnabled(context: Context): Boolean =
            NotificationManagerCompat.getEnabledListenerPackages(context).contains(context.packageName)

        fun component(context: Context) = ComponentName(context, DroidNotificationListener::class.java)

        private fun svc(): DroidNotificationListener = instance ?: throw CmdError.noPermission("notif")

        fun toJson(sbn: StatusBarNotification): JSONObject {
            val ex = sbn.notification.extras
            val text = ex.getCharSequence(Notification.EXTRA_BIG_TEXT)?.takeIf { it.isNotBlank() } ?: ex.getCharSequence(Notification.EXTRA_TEXT)
            return JSONObject()
                .put("key", sbn.key)
                .put("package", sbn.packageName)
                .put("title", ex.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty())
                .put("text", text?.toString().orEmpty())
                .put("lines", JSONArray(ex.getCharSequenceArray(Notification.EXTRA_TEXT_LINES).orEmpty().map { it.toString() }))
                .put("time", sbn.postTime)
                .put("actions", JSONArray(actionNames(sbn.notification)))
        }

        /** Semantic names where the app declares them, else the button title. */
        private fun actionNames(n: Notification): List<String> = n.actions.orEmpty().map { a ->
            when {
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_REPLY || !a.remoteInputs.isNullOrEmpty() -> "reply"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_MARK_AS_READ -> "mark_read"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_MARK_AS_UNREAD -> "mark_unread"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_DELETE -> "delete"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_ARCHIVE -> "archive"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_MUTE -> "mute"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_UNMUTE -> "unmute"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_THUMBS_UP -> "thumbs_up"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_THUMBS_DOWN -> "thumbs_down"
                a.semanticAction == Notification.Action.SEMANTIC_ACTION_CALL -> "call"
                else -> a.title?.toString().orEmpty()
            }
        }

        private fun active(): List<StatusBarNotification> =
            runCatching { svc().activeNotifications?.toList() }.getOrNull().orEmpty()

        fun list(pkg: String?): JSONArray =
            JSONArray(active().filter { pkg == null || it.packageName == pkg }.map { toJson(it) })

        fun has(by: String, value: String): Boolean = active().any { sbn ->
            val ex = sbn.notification.extras
            val title = ex.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty()
            val text = (ex.getCharSequence(Notification.EXTRA_BIG_TEXT)?.takeIf { it.isNotBlank() } ?: ex.getCharSequence(Notification.EXTRA_TEXT))?.toString().orEmpty()
            when (by) {
                "text" -> title == value || text == value
                "textContains" -> title.contains(value) || text.contains(value)
                "title" -> title == value
                "package" -> sbn.packageName == value
                else -> throw CmdError.badArgs("has_notification", "by must be text, textContains, title or package")
            }
        }

        private fun byKey(cmd: String, key: String): StatusBarNotification =
            active().firstOrNull { it.key == key }
                ?: throw CmdError("NOT_FOUND", mapOf("target" to "key '$key'", "timeout" to 0, "screen" to "", "cmd" to cmd))

        fun reply(key: String, text: String) {
            val sbn = byKey("notification_reply", key)
            val action = sbn.notification.actions.orEmpty().firstOrNull { a ->
                a.remoteInputs.orEmpty().any { it.allowFreeFormInput }
            } ?: throw CmdError.badArgs("notification_reply", "this notification has no reply action")
            val intent = Intent()
            val results = Bundle()
            for (ri in action.remoteInputs) results.putCharSequence(ri.resultKey, text)
            RemoteInput.addResultsToIntent(action.remoteInputs, intent, results)
            send(action.actionIntent, intent)
        }

        fun click(key: String) {
            val sbn = byKey("notification_click", key)
            val pi = sbn.notification.contentIntent
                ?: throw CmdError.badArgs("notification_click", "this notification does not open anything")
            send(pi, null)
        }

        fun dismiss(key: String) {
            byKey("notification_dismiss", key)
            svc().cancelNotification(key)
        }

        /** Android 14+ only lets a PendingIntent start an activity from the background when the sender opts in. */
        private fun send(pi: PendingIntent, fill: Intent?) {
            try {
                val options = if (Build.VERSION.SDK_INT >= 34) {
                    val mode = if (Build.VERSION.SDK_INT >= 36) ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOW_ALWAYS
                    else @Suppress("DEPRECATION") ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED
                    ActivityOptions.makeBasic().setPendingIntentBackgroundActivityStartMode(mode).toBundle()
                } else {
                    null
                }
                pi.send(svc(), 0, fill, null, null, null, options)
            } catch (e: PendingIntent.CanceledException) {
                throw CmdError.badArgs("notification", "the app cancelled this action")
            }
        }
    }
}
