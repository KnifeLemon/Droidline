package dev.droidline.agent.link

import android.content.Context
import android.net.wifi.WifiManager
import dev.droidline.agent.crypto.B64
import org.json.JSONObject
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.NetworkInterface
import java.net.SocketTimeoutException

data class DiscoveredServer(
    val serverId: String,
    val name: String,
    val host: String,
    val port: Int,
    val pub: ByteArray,
    val pairing: Boolean,
)

/** UDP broadcast discovery (PROTOCOL 4.1). The reply is unauthenticated; the handshake checks it. */
object Discovery {
    const val PORT = 8778

    fun request(serverId: String?): String {
        val o = JSONObject().put("droidline", "discover").put("v", 1)
        if (serverId != null) o.put("server", serverId)
        return o.toString()
    }

    fun parseReply(text: String, from: String): DiscoveredServer? = runCatching {
        val o = JSONObject(text)
        if (o.optString("droidline") != "here" || o.optInt("v") != 1) return null
        val pub = B64.unurl(o.getString("pub"))
        if (pub.size != 65) return null
        DiscoveredServer(
            serverId = o.getString("server"),
            name = o.optString("name", o.getString("server")),
            host = from,
            port = o.optInt("port", 8779),
            pub = pub,
            pairing = o.optBoolean("pairing", false),
        )
    }.getOrNull()

    /**
     * Broadcasts to 255.255.255.255 and each interface's directed broadcast, then collects replies for [listenMs].
     * With [serverId] set, returns as soon as that server answers.
     */
    fun run(context: Context, serverId: String?, listenMs: Long = 1500, rounds: Int = 2): List<DiscoveredServer> {
        val wifi = context.applicationContext.getSystemService(WifiManager::class.java)
        val lock = wifi?.createMulticastLock("droidline-discovery")?.apply { setReferenceCounted(false) }
        val found = LinkedHashMap<String, DiscoveredServer>()
        try {
            lock?.acquire()
            DatagramSocket().use { socket ->
                socket.broadcast = true
                val payload = request(serverId).toByteArray(Charsets.UTF_8)
                val targets = broadcastTargets()
                val buf = ByteArray(4096)
                repeat(rounds) {
                    for (t in targets) runCatching { socket.send(DatagramPacket(payload, payload.size, t, PORT)) }
                    val until = System.currentTimeMillis() + listenMs / rounds
                    while (true) {
                        val left = until - System.currentTimeMillis()
                        if (left <= 0) break
                        socket.soTimeout = left.toInt().coerceAtLeast(1)
                        val p = DatagramPacket(buf, buf.size)
                        try {
                            socket.receive(p)
                        } catch (_: SocketTimeoutException) {
                            break
                        }
                        val host = p.address.hostAddress ?: continue
                        val reply = parseReply(String(p.data, 0, p.length, Charsets.UTF_8), host) ?: continue
                        if (serverId != null && reply.serverId != serverId) continue
                        found[reply.serverId] = reply
                        if (serverId != null) return found.values.toList()
                    }
                }
            }
        } catch (_: Exception) {
            // No network or no permission to broadcast: report what was found so far.
        } finally {
            runCatching { lock?.release() }
        }
        return found.values.toList()
    }

    private fun broadcastTargets(): List<InetAddress> {
        val out = LinkedHashSet<InetAddress>()
        out += InetAddress.getByName("255.255.255.255")
        runCatching {
            for (nif in NetworkInterface.getNetworkInterfaces()) {
                if (!nif.isUp || nif.isLoopback) continue
                for (ia in nif.interfaceAddresses) {
                    if (ia.address is Inet4Address) ia.broadcast?.let { out += it }
                }
            }
        }
        return out.toList()
    }
}
