package dev.droidline.agent.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import dev.droidline.agent.Agent
import dev.droidline.agent.R
import dev.droidline.agent.cmd.CommandRunner
import dev.droidline.agent.device.Projection
import dev.droidline.agent.link.LinkManager
import dev.droidline.agent.link.LinkStatus
import dev.droidline.agent.link.Phase
import dev.droidline.agent.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** Foreground service that holds the link to the PC and runs commands. */
class AgentService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var runner: CommandRunner
    private lateinit var link: LinkManager
    @Volatile private var projectionActive = false

    override fun onCreate() {
        super.onCreate()
        Agent.init(this)
        createChannels(this)
        goForeground(Agent.status.value)
        runner = CommandRunner(applicationContext, scope).also { it.start() }
        link = LinkManager(applicationContext, scope) { runner.submit(it) }
        Agent.link = link
        link.start()
        instance = this
        scope.launch {
            Agent.status.collect { goForeground(it) }
        }
        scope.launch {
            while (isActive) {
                Agent.refreshReady()
                delay(5000)
            }
        }
        scope.launch(Dispatchers.IO) {
            while (isActive) {
                UpdateChecker.maybeCheck(applicationContext)
                delay(6 * 60 * 60 * 1000L)
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        if (instance === this) instance = null
        Agent.link = null
        link.stop()
        runner.stop()
        if (Build.VERSION.SDK_INT < 30) Projection.release()
        scope.cancel()
        super.onDestroy()
    }

    /** Android 10 requires the mediaProjection type while a projection is held; 9 has no types. */
    fun setProjectionActive(active: Boolean) {
        projectionActive = active
        goForeground(Agent.status.value)
    }

    private fun goForeground(status: LinkStatus) {
        val type = when {
            Build.VERSION.SDK_INT >= 34 -> ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
            Build.VERSION.SDK_INT >= 29 && projectionActive -> ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION
            else -> 0
        }
        ServiceCompat.startForeground(this, NOTIF_ID, notification(status), type)
    }

    private fun notification(status: LinkStatus): Notification {
        val text = when (status.phase) {
            Phase.CONNECTED -> getString(R.string.notif_connected, status.serverName ?: "", status.route ?: "")
            Phase.CONNECTING -> getString(R.string.notif_connecting)
            Phase.WAITING_CODE -> getString(R.string.notif_waiting_code, status.sas ?: "")
            Phase.REJECTED -> getString(R.string.notif_rejected)
            Phase.IDLE, Phase.PAIR_FAILED -> getString(R.string.notif_not_paired)
            Phase.OFFLINE -> getString(R.string.notif_offline)
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, CHANNEL_LINK)
            .setSmallIcon(R.drawable.ic_stat_droidline)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(text)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(open)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .build()
    }

    companion object {
        const val CHANNEL_LINK = "link"
        const val CHANNEL_UPDATES = "updates"
        private const val NOTIF_ID = 1

        @Volatile var instance: AgentService? = null
            private set

        fun createChannels(ctx: Context) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_LINK, ctx.getString(R.string.channel_link), NotificationManager.IMPORTANCE_LOW)
            )
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_UPDATES, ctx.getString(R.string.channel_updates), NotificationManager.IMPORTANCE_DEFAULT)
            )
        }

        /** Starts the service; a background start can be refused on Android 12+, which is ignored here. */
        fun start(ctx: Context) {
            runCatching { ContextCompat.startForegroundService(ctx, Intent(ctx, AgentService::class.java)) }
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, AgentService::class.java))
        }
    }
}

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED && intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        Agent.init(context)
        if (Agent.isPaired) AgentService.start(context)
    }
}
