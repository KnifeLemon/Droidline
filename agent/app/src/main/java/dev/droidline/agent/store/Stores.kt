package dev.droidline.agent.store

import android.content.Context
import dev.droidline.agent.crypto.B64
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import org.json.JSONObject

/** The one server this phone is paired with. Re-pairing replaces it. */
data class PairingRecord(
    val serverId: String,
    val serverName: String,
    val serverPub: ByteArray,
    val addresses: List<String>,
    val tlsFingerprint: String?,
    val relayUrl: String?,
    val relayTicket: String?,
    val deviceName: String?,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("server", serverId)
        .put("name", serverName)
        .put("pub", B64.url(serverPub))
        .put("addresses", JSONArray(addresses))
        .put("tls_fp", tlsFingerprint ?: JSONObject.NULL)
        .put("relay_url", relayUrl ?: JSONObject.NULL)
        .put("relay_ticket", relayTicket ?: JSONObject.NULL)
        .put("device_name", deviceName ?: JSONObject.NULL)

    companion object {
        fun fromJson(o: JSONObject) = PairingRecord(
            serverId = o.getString("server"),
            serverName = o.optString("name", o.getString("server")),
            serverPub = B64.unurl(o.getString("pub")),
            addresses = o.optJSONArray("addresses").toStringList(),
            tlsFingerprint = o.optNullableString("tls_fp"),
            relayUrl = o.optNullableString("relay_url"),
            relayTicket = o.optNullableString("relay_ticket"),
            deviceName = o.optNullableString("device_name"),
        )
    }
}

fun JSONArray?.toStringList(): List<String> =
    if (this == null) emptyList() else (0 until length()).mapNotNull { optString(it, null) }

fun JSONObject.optNullableString(name: String): String? =
    if (isNull(name)) null else optString(name).takeIf { it.isNotEmpty() }

class PairingStore(context: Context) {
    private val prefs = context.getSharedPreferences("pairing", Context.MODE_PRIVATE)
    private var loaded = false
    private var cached: PairingRecord? = null
    private val _revision = MutableStateFlow(0)

    /** Counts saves, so screens can re-read the record when the server renames this phone. */
    val revision: StateFlow<Int> = _revision

    @Synchronized
    fun load(): PairingRecord? {
        if (!loaded) {
            cached = prefs.getString("record", null)?.let { runCatching { PairingRecord.fromJson(JSONObject(it)) }.getOrNull() }
            loaded = true
        }
        return cached
    }

    @Synchronized
    fun save(record: PairingRecord) {
        prefs.edit().putString("record", record.toJson().toString()).apply()
        cached = record
        loaded = true
        _revision.value++
    }

    @Synchronized
    fun update(change: (PairingRecord) -> PairingRecord) {
        load()?.let { save(change(it)) }
    }

    @Synchronized
    fun clear() {
        prefs.edit().remove("record").apply()
        cached = null
        loaded = true
        _revision.value++
    }
}

class SettingsStore(context: Context) {
    private val prefs = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

    /** Packages whose notifications are forwarded to the PC. Empty by default. */
    var notifyAllowlist: Set<String>
        get() = prefs.getStringSet("notify_allowlist", emptySet())!!.toSet()
        set(value) = prefs.edit().putStringSet("notify_allowlist", value.toSet()).apply()

    var lastUpdateCheck: Long
        get() = prefs.getLong("last_update_check", 0)
        set(value) = prefs.edit().putLong("last_update_check", value).apply()

    var latestRelease: String?
        get() = prefs.getString("latest_release", null)
        set(value) = prefs.edit().putString("latest_release", value).apply()

    var latestReleaseUrl: String?
        get() = prefs.getString("latest_release_url", null)
        set(value) = prefs.edit().putString("latest_release_url", value).apply()

    var notifiedRelease: String?
        get() = prefs.getString("notified_release", null)
        set(value) = prefs.edit().putString("notified_release", value).apply()
}
