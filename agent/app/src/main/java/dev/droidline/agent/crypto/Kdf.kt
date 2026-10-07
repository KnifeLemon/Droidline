package dev.droidline.agent.crypto

import java.io.ByteArrayOutputStream
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

object Hkdf {
    fun extract(salt: ByteArray, ikm: ByteArray): ByteArray = hmac(salt, ikm)

    fun expand(prk: ByteArray, info: ByteArray, length: Int): ByteArray {
        val out = ByteArrayOutputStream()
        var t = ByteArray(0)
        var i = 1
        while (out.size() < length) {
            t = hmac(prk, t + info + byteArrayOf(i.toByte()))
            out.write(t)
            i++
        }
        return out.toByteArray().copyOf(length)
    }

    fun hmac(key: ByteArray, data: ByteArray): ByteArray =
        Mac.getInstance("HmacSHA256").run {
            init(SecretKeySpec(key, "HmacSHA256"))
            doFinal(data)
        }
}

fun sha256(data: ByteArray): ByteArray = MessageDigest.getInstance("SHA-256").digest(data)

class SessionKeys(val th: ByteArray, val up: ByteArray, val down: ByteArray, val sas: String)

object KeySchedule {
    fun transcriptHash(line1: String, line2: String): ByteArray =
        sha256(line1.toByteArray(Charsets.UTF_8) + byteArrayOf('\n'.code.toByte()) + line2.toByteArray(Charsets.UTF_8))

    fun derive(line1: String, line2: String, ikm: ByteArray): SessionKeys {
        val th = transcriptHash(line1, line2)
        val prk = Hkdf.extract(th, ikm)
        val up = Hkdf.expand(prk, "droidline v1 up".toByteArray(), 32)
        val down = Hkdf.expand(prk, "droidline v1 down".toByteArray(), 32)
        val sasBytes = Hkdf.expand(prk, "droidline v1 sas".toByteArray(), 4)
        val sasInt = ((sasBytes[0].toLong() and 0xff) shl 24) or ((sasBytes[1].toLong() and 0xff) shl 16) or
            ((sasBytes[2].toLong() and 0xff) shl 8) or (sasBytes[3].toLong() and 0xff)
        return SessionKeys(th, up, down, "%06d".format(sasInt % 1_000_000))
    }

    fun ikm(ecdhEphemeral: ByteArray, ecdhStatic: ByteArray, token: ByteArray?): ByteArray =
        ecdhEphemeral + ecdhStatic + (token ?: ByteArray(0))
}

class EnvelopeCipher(private val key: ByteArray) {
    fun seal(seq: Long, plaintext: ByteArray): ByteArray = cipher(Cipher.ENCRYPT_MODE, seq).doFinal(plaintext)

    fun open(seq: Long, ciphertext: ByteArray): ByteArray = cipher(Cipher.DECRYPT_MODE, seq).doFinal(ciphertext)

    private fun cipher(mode: Int, seq: Long): Cipher =
        Cipher.getInstance("AES/GCM/NoPadding").apply {
            init(mode, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce(seq)))
            updateAAD(AAD)
        }

    companion object {
        private val AAD = "droidline v1".toByteArray(Charsets.US_ASCII)

        fun nonce(seq: Long): ByteArray {
            val n = ByteArray(12)
            for (i in 0 until 8) n[11 - i] = (seq ushr (8 * i)).toByte()
            return n
        }
    }
}
