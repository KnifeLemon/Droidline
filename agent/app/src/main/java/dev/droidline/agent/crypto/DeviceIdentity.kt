package dev.droidline.agent.crypto

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyPair
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The phone's device id and static P-256 key pair, created once per install.
 * AndroidKeyStore cannot do ECDH below API 31, so the EC key is stored encrypted under a keystore AES-GCM key.
 */
class DeviceIdentity private constructor(val deviceId: String, val keyPair: KeyPair) {
    val publicRaw: ByteArray get() = P256.rawPublic(keyPair.public)

    companion object {
        private const val PREFS = "identity"
        private const val WRAP_ALIAS = "droidline_identity_wrap"

        @Volatile private var cached: DeviceIdentity? = null

        fun get(context: Context): DeviceIdentity =
            cached ?: synchronized(this) { cached ?: load(context.applicationContext).also { cached = it } }

        private fun load(context: Context): DeviceIdentity {
            val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            val id = prefs.getString("device_id", null)
            val pub = prefs.getString("static_pub", null)
            val wrapped = prefs.getString("static_priv", null)
            if (id != null && pub != null && wrapped != null) {
                runCatching {
                    val pkcs8 = unwrap(B64.unurl(wrapped))
                    return DeviceIdentity(id, KeyPair(P256.publicFromRaw(B64.unurl(pub)), P256.privateFromPkcs8(pkcs8)))
                }
                // A keystore reset (for example after a restore to a new phone) makes the old key unreadable.
            }
            val pair = P256.generate()
            val newId = id ?: newDeviceId()
            prefs.edit()
                .putString("device_id", newId)
                .putString("static_pub", B64.url(P256.rawPublic(pair.public)))
                .putString("static_priv", B64.url(wrap(pair.private.encoded)))
                .apply()
            return DeviceIdentity(newId, pair)
        }

        fun newDeviceId(random: SecureRandom = SecureRandom()): String {
            val alphabet = "0123456789abcdefghijklmnopqrstuv"
            return buildString { repeat(8) { append(alphabet[random.nextInt(32)]) } }
        }

        private fun wrapKey(): SecretKey {
            val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            (ks.getKey(WRAP_ALIAS, null) as? SecretKey)?.let { return it }
            val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
            gen.init(
                KeyGenParameterSpec.Builder(WRAP_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setKeySize(256)
                    .build()
            )
            return gen.generateKey()
        }

        private fun wrap(plain: ByteArray): ByteArray {
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.ENCRYPT_MODE, wrapKey())
            return c.iv + c.doFinal(plain)
        }

        private fun unwrap(blob: ByteArray): ByteArray {
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.DECRYPT_MODE, wrapKey(), GCMParameterSpec(128, blob.copyOfRange(0, 12)))
            return c.doFinal(blob, 12, blob.size - 12)
        }
    }
}
