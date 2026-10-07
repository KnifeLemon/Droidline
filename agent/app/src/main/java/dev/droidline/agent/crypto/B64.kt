package dev.droidline.agent.crypto

import java.util.Base64

object B64 {
    fun url(bytes: ByteArray): String = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)

    // The URL decoder accepts input with or without padding.
    fun unurl(text: String): ByteArray = Base64.getUrlDecoder().decode(text)

    fun std(bytes: ByteArray): String = Base64.getEncoder().encodeToString(bytes)
}

object Hex {
    fun encode(bytes: ByteArray): String = bytes.joinToString("") { "%02x".format(it) }

    fun decode(text: String): ByteArray {
        require(text.length % 2 == 0) { "odd hex length" }
        return ByteArray(text.length / 2) { text.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
    }
}
