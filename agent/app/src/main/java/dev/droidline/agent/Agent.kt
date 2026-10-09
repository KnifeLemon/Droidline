package dev.droidline.agent

import android.annotation.SuppressLint
import android.content.Context
import android.os.Build
import dev.droidline.agent.cmd.CommandSpec
import dev.droidline.agent.crypto.DeviceIdentity
import dev.droidline.agent.crypto.Hex
import dev.droidline.agent.link.LinkManager
import dev.droidline.agent.link.LinkStatus
import dev.droidline.agent.link.Session
import dev.droidline.agent.service.Readiness
import dev.droidline.agent.service.Ready
import dev.droidline.agent.store.PairingStore
import dev.droidline.agent.store.SettingsStore
import kotlinx.coroutines.flow.MutableStateFlow
import org.json.JSONObject
import java.security.SecureRandom
import java.util.Locale

/** Process-wide state shared by the service, the accessibility service, the keyboard and the UI. */
// Only the application context is kept, which lives as long as the process.
@SuppressLint("StaticFieldLeak")
object Agent {
    lateinit var app: Context
        private set
    lateinit var pairing: PairingStore
        private set
    lateinit var settings: SettingsStore
        private set

    val identity: DeviceIdentity by lazy { DeviceIdentity.get(app) }
    val spec: CommandSpec by lazy { CommandSpec(JSONObject(app.assets.open("commands.json").bufferedReader().use { it.readText() })) }

    /** Random per process start; the server uses it to tell a restart from a reconnect. */
    val boot: String = Hex.encode(ByteArray(3).also { SecureRandom().nextBytes(it) })

    val session = Session()
    val status = MutableStateFlow(LinkStatus())
    val ready = MutableStateFlow(Ready())

    /** Why the last pairing attempt failed, kept apart from [status] so a reconnect to an older pairing does not hide it. */
    val pairFailure = MutableStateFlow<String?>(null)

    @Volatile var link: LinkManager? = null

    val version: String get() = BuildConfig.VERSION_NAME

    fun init(context: Context) {
        if (::app.isInitialized) return
        app = context.applicationContext
        pairing = PairingStore(app)
        settings = SettingsStore(app)
    }

    val isPaired: Boolean get() = ::pairing.isInitialized && pairing.load() != null

    /** Queues an event for the server. Nothing is kept while the phone is not paired. */
    fun emit(event: JSONObject, kind: String? = null) {
        if (!isPaired) return
        session.send(event, kind)
    }

    fun hello(route: String): JSONObject = JSONObject()
        .put("event", "hello")
        .put("device", identity.deviceId)
        .put("boot", boot)
        .put("model", Build.MODEL)
        .put("manufacturer", Build.MANUFACTURER)
        .put("sdk", Build.VERSION.SDK_INT)
        .put("release", Build.VERSION.RELEASE)
        .put("agent", version)
        .put("route", route)
        .put("lang", Locale.getDefault().language)
        .put("ready", snapshotReady().toJson())

    fun onConnected() {
        refreshReady()
    }

    /**
     * Recomputes the ready flags and sends a `state` event when they changed. The accessibility
     * service, the keyboard, the listener and the link all call this from their own threads, so
     * computing and sending happen under one lock: otherwise an older result can be sent last.
     */
    @Synchronized
    fun refreshReady() {
        if (!::app.isInitialized) return
        val now = Readiness.compute(app)
        if (now == ready.value) return
        ready.value = now
        emit(JSONObject().put("event", "state").put("ready", now.toJson()))
    }

    @Synchronized
    private fun snapshotReady(): Ready = Readiness.compute(app).also { ready.value = it }
}
