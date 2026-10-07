package dev.droidline.agent.link

import java.net.URLEncoder

/** One way to reach the server. [kind] is reported as `route` in hello. */
data class Route(val kind: String, val url: String, val bearer: String? = null) {
    companion object {
        const val LAN = "lan"
        const val DIRECT = "direct"
        const val TUNNEL = "tunnel"
        const val RELAY = "relay"
    }
}

data class HostPort(val host: String, val port: Int)

object Routes {
    /**
     * Routes in the order lan, direct, tunnel, relay. [discovered] is where a discovery reply came from;
     * tcp:// addresses from the QR or a config event are tried as lan right after it.
     */
    fun order(
        discovered: HostPort?,
        addresses: List<String>,
        relayUrl: String?,
        relayBearer: String?,
        serverId: String,
        deviceId: String,
        relayExpiry: Long? = null,
    ): List<Route> {
        val out = ArrayList<Route>()
        discovered?.let { out += Route(Route.LAN, "tcp://${hostText(it.host)}:${it.port}") }
        val list = addresses.map { it.trim() }
        list.filter { it.startsWith("tcp://", true) }.forEach { out += Route(Route.LAN, it) }
        list.filter { it.startsWith("tls://", true) }.forEach { out += Route(Route.DIRECT, it) }
        list.filter { it.startsWith("wss://", true) }.forEach { out += Route(Route.TUNNEL, it) }
        if (!relayUrl.isNullOrBlank() && !relayBearer.isNullOrBlank()) {
            out += Route(Route.RELAY, relayDeviceUrl(relayUrl, serverId, deviceId, relayExpiry), relayBearer)
        }
        return out.distinctBy { it.kind + " " + it.url.lowercase() }
    }

    fun relayDeviceUrl(base: String, serverId: String, deviceId: String, expiry: Long?): String {
        val root = base.trimEnd('/')
        val q = "server=${enc(serverId)}&device=${enc(deviceId)}" + (expiry?.let { "&expiry=$it" } ?: "")
        return "$root/v1/device?$q"
    }

    /** Parses tcp:// and tls:// addresses, including bracketed IPv6 hosts. */
    fun hostPort(address: String, defaultPort: Int = 8779): HostPort {
        val rest = address.substringAfter("://").substringBefore('/')
        if (rest.startsWith("[")) {
            val close = rest.indexOf(']')
            require(close > 0) { "bad IPv6 address" }
            val port = rest.substring(close + 1).removePrefix(":").toIntOrNull() ?: defaultPort
            return HostPort(rest.substring(1, close), port)
        }
        val colon = rest.lastIndexOf(':')
        return if (colon > 0) HostPort(rest.substring(0, colon), rest.substring(colon + 1).toInt())
        else HostPort(rest, defaultPort)
    }

    private fun hostText(host: String) = if (host.contains(':')) "[$host]" else host

    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8")
}
