package dev.droidline.agent.link

import dev.droidline.agent.crypto.B64
import dev.droidline.agent.crypto.KeySchedule
import dev.droidline.agent.crypto.P256
import dev.droidline.agent.crypto.SessionKeys
import org.json.JSONObject
import java.security.KeyPair
import java.security.PublicKey

enum class HsMode(val wire: String) { AUTH("auth"), PAIR_QR("pair_qr"), PAIR_CODE("pair_code") }

class HandshakeRejected(val status: String) : Exception("server answered $status")

class ServerHello(val serverId: String, val eph: ByteArray, val nonce: ByteArray)

object Handshake {
    /** Builds line 1 with a fixed field order; the transcript hash covers these exact bytes. */
    fun line1(
        mode: HsMode,
        deviceId: String,
        ephPub: ByteArray,
        nonce: ByteArray,
        staticPub: ByteArray?,
        tokenId: String?,
    ): String = buildString {
        append("{\"hs\":1,\"proto\":1,\"mode\":").append(JSONObject.quote(mode.wire))
        append(",\"device\":").append(JSONObject.quote(deviceId))
        append(",\"eph\":").append(JSONObject.quote(B64.url(ephPub)))
        append(",\"nonce\":").append(JSONObject.quote(B64.url(nonce)))
        if (mode != HsMode.AUTH) append(",\"pub\":").append(JSONObject.quote(B64.url(requireNotNull(staticPub))))
        if (mode == HsMode.PAIR_QR) append(",\"tid\":").append(JSONObject.quote(requireNotNull(tokenId)))
        append('}')
    }

    fun parseLine2(line2: String): ServerHello {
        val o = JSONObject(line2)
        if (o.optInt("hs") != 1) throw ProtocolException("not a handshake reply")
        val status = o.optString("status")
        if (status != "ok") throw HandshakeRejected(status.ifEmpty { "unknown" })
        if (o.optInt("proto") != 1) throw HandshakeRejected("bad_proto")
        return ServerHello(o.getString("server"), B64.unurl(o.getString("eph")), B64.unurl(o.getString("nonce")))
    }

    fun keys(
        line1: String,
        line2: String,
        eph: KeyPair,
        serverEph: PublicKey,
        static: KeyPair,
        serverStatic: PublicKey,
        token: ByteArray?,
    ): SessionKeys {
        val ikm = KeySchedule.ikm(P256.ecdh(eph.private, serverEph), P256.ecdh(static.private, serverStatic), token)
        return KeySchedule.derive(line1, line2, ikm)
    }
}
