package dev.droidline.agent.link

import dev.droidline.agent.crypto.B64
import java.net.URLDecoder

/** Parsed `droidline://pair?...` URI from PROTOCOL section 5. */
class QrPairing(
    val serverId: String,
    val serverName: String,
    val serverPub: ByteArray,
    val tokenId: String,
    val token: ByteArray,
    val addresses: List<String>,
    val tlsFingerprint: String?,
    val relayUrl: String?,
    val relayTicket: String?,
    val relayExpiry: Long?,
)

object QrUri {
    private const val PREFIX = "droidline://pair?"
    private val HEX64 = Regex("[0-9a-f]{64}")

    fun parse(uri: String): QrPairing {
        val text = uri.trim()
        if (!text.startsWith(PREFIX, ignoreCase = true)) throw IllegalArgumentException("not a Droidline pairing code")
        val raw = HashMap<String, String>()
        for (pair in text.substring(PREFIX.length).split('&')) {
            if (pair.isEmpty()) continue
            val eq = pair.indexOf('=')
            if (eq < 0) raw[decode(pair)] = "" else raw[decode(pair.substring(0, eq))] = pair.substring(eq + 1)
        }
        fun param(name: String): String? = raw[name]?.let(::decode)?.takeIf { it.isNotEmpty() }

        if (param("v") != "1") throw IllegalArgumentException("unsupported pairing version")
        val server = param("s") ?: throw IllegalArgumentException("missing server id")
        val pub = B64.unurl(param("k") ?: throw IllegalArgumentException("missing server key"))
        if (pub.size != 65 || pub[0] != 4.toByte()) throw IllegalArgumentException("bad server key")

        val t = param("t") ?: throw IllegalArgumentException("missing token")
        val dot = t.lastIndexOf('.')
        if (dot <= 0 || dot == t.length - 1) throw IllegalArgumentException("bad token")
        val token = B64.unurl(t.substring(dot + 1))
        if (token.size != 16) throw IllegalArgumentException("bad token length")

        // Split before decoding: each address is URL-encoded on its own.
        val addresses = raw["a"].orEmpty().split(',').map { decode(it).trim() }.filter { it.isNotEmpty() }
        val fp = param("f")?.lowercase()
        if (fp != null && !HEX64.matches(fp)) throw IllegalArgumentException("bad TLS fingerprint")
        if (fp == null && addresses.any { it.startsWith("tls://", ignoreCase = true) }) {
            throw IllegalArgumentException("tls address without fingerprint")
        }
        return QrPairing(
            serverId = server,
            serverName = param("n") ?: server,
            serverPub = pub,
            tokenId = t.substring(0, dot),
            token = token,
            addresses = addresses,
            tlsFingerprint = fp,
            relayUrl = param("r"),
            relayTicket = param("rt"),
            relayExpiry = param("re")?.toLongOrNull(),
        )
    }

    private fun decode(s: String): String = URLDecoder.decode(s, "UTF-8")
}
