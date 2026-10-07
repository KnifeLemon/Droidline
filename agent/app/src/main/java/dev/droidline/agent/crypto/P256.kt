package dev.droidline.agent.crypto

import java.math.BigInteger
import java.security.KeyFactory
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.PrivateKey
import java.security.PublicKey
import java.security.interfaces.ECPrivateKey
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECPrivateKeySpec
import java.security.spec.PKCS8EncodedKeySpec
import java.security.spec.X509EncodedKeySpec
import javax.crypto.KeyAgreement

object P256 {
    // SubjectPublicKeyInfo header for id-ecPublicKey on prime256v1, followed by the 65-byte point.
    private val SPKI_PREFIX = Hex.decode("3059301306072a8648ce3d020106082a8648ce3d030107034200")

    fun generate(): KeyPair =
        KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()

    fun rawPublic(key: PublicKey): ByteArray {
        val point = (key as ECPublicKey).w
        return byteArrayOf(4) + fixed32(point.affineX) + fixed32(point.affineY)
    }

    fun publicFromRaw(raw: ByteArray): PublicKey {
        require(raw.size == 65 && raw[0] == 4.toByte()) { "not an uncompressed P-256 point" }
        return KeyFactory.getInstance("EC").generatePublic(X509EncodedKeySpec(SPKI_PREFIX + raw))
    }

    fun privateFromScalar(d: ByteArray): PrivateKey {
        val params = (generate().private as ECPrivateKey).params
        return KeyFactory.getInstance("EC").generatePrivate(ECPrivateKeySpec(BigInteger(1, d), params))
    }

    fun privateFromPkcs8(encoded: ByteArray): PrivateKey =
        KeyFactory.getInstance("EC").generatePrivate(PKCS8EncodedKeySpec(encoded))

    fun ecdh(own: PrivateKey, peer: PublicKey): ByteArray =
        KeyAgreement.getInstance("ECDH").run {
            init(own)
            doPhase(peer, true)
            generateSecret()
        }

    private fun fixed32(n: BigInteger): ByteArray {
        val b = n.toByteArray()
        return when {
            b.size == 32 -> b
            b.size > 32 -> b.copyOfRange(b.size - 32, b.size)
            else -> ByteArray(32 - b.size) + b
        }
    }
}
