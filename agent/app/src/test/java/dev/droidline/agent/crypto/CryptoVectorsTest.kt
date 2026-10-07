package dev.droidline.agent.crypto

import dev.droidline.agent.TestFiles
import dev.droidline.agent.link.Envelope
import dev.droidline.agent.link.Handshake
import dev.droidline.agent.link.HsMode
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.security.KeyPair

class CryptoVectorsTest {
    private val v = TestFiles.vectors()
    private val keys = v.getJSONObject("keys")

    private fun pair(name: String): KeyPair {
        val k = keys.getJSONObject(name)
        return KeyPair(P256.publicFromRaw(B64.unurl(k.getString("pub"))), P256.privateFromScalar(Hex.decode(k.getString("d"))))
    }

    private val phoneStatic = pair("phone_static")
    private val serverStatic = pair("server_static")
    private val phoneEph = pair("phone_eph")
    private val serverEph = pair("server_eph")

    private fun cases(): List<JSONObject> {
        val arr = v.getJSONArray("handshakes")
        return (0 until arr.length()).map { arr.getJSONObject(it) }
    }

    private fun token(c: JSONObject): ByteArray? = if (c.isNull("token")) null else B64.unurl(c.getString("token"))

    @Test
    fun rawPublicKeysRoundTrip() {
        for (name in listOf("phone_static", "server_static", "phone_eph", "server_eph")) {
            val raw = B64.unurl(keys.getJSONObject(name).getString("pub"))
            assertArrayEquals(name, raw, P256.rawPublic(P256.publicFromRaw(raw)))
        }
    }

    @Test
    fun ecdhIsSymmetric() {
        assertArrayEquals(P256.ecdh(phoneEph.private, serverEph.public), P256.ecdh(serverEph.private, phoneEph.public))
        assertArrayEquals(P256.ecdh(phoneStatic.private, serverStatic.public), P256.ecdh(serverStatic.private, phoneStatic.public))
    }

    @Test
    fun line1IsBuiltByteForByte() {
        val nonce = ByteArray(16) { it.toByte() }
        for (c in cases()) {
            val mode = HsMode.entries.first { it.wire == c.getString("mode") }
            val line1 = Handshake.line1(
                mode, "a1b2c3d4", P256.rawPublic(phoneEph.public), nonce, P256.rawPublic(phoneStatic.public),
                if (mode == HsMode.PAIR_QR) "t0k3n1d0" else null,
            )
            assertEquals(c.getString("mode"), c.getString("line1"), line1)
        }
    }

    @Test
    fun keyScheduleMatchesVectors() {
        for (c in cases()) {
            val mode = c.getString("mode")
            val line1 = c.getString("line1")
            val line2 = c.getString("line2")
            assertEquals(mode, c.getString("th"), Hex.encode(KeySchedule.transcriptHash(line1, line2)))
            val ikm = KeySchedule.ikm(P256.ecdh(phoneEph.private, serverEph.public), P256.ecdh(phoneStatic.private, serverStatic.public), token(c))
            assertEquals(mode, c.getString("ikm"), Hex.encode(ikm))

            val hello = Handshake.parseLine2(line2)
            assertEquals("k3j9d0a2mq", hello.serverId)
            val k = Handshake.keys(line1, line2, phoneEph, P256.publicFromRaw(hello.eph), phoneStatic, serverStatic.public, token(c))
            assertEquals(mode, c.getString("k_up"), Hex.encode(k.up))
            assertEquals(mode, c.getString("k_dn"), Hex.encode(k.down))
            assertEquals(mode, c.getString("sas"), k.sas)
        }
    }

    @Test
    fun envelopesEncryptAndDecryptExactly() {
        for (c in cases()) {
            val up = EnvelopeCipher(Hex.decode(c.getString("k_up")))
            val down = EnvelopeCipher(Hex.decode(c.getString("k_dn")))

            val u = c.getJSONObject("up_seq0")
            val uEnv = u.getJSONObject("envelope")
            val uSeq = uEnv.getLong("seq")
            assertEquals(0L, uSeq)
            assertEquals(uEnv.getString("blob"), B64.url(up.seal(uSeq, u.getString("plaintext").toByteArray())))
            assertEquals(u.getString("plaintext"), String(up.open(uSeq, B64.unurl(uEnv.getString("blob")))))

            val d = c.getJSONObject("dn_seq1")
            val dEnv = d.getJSONObject("envelope")
            val dSeq = dEnv.getLong("seq")
            assertEquals(1L, dSeq)
            assertEquals(d.getString("plaintext"), String(down.open(dSeq, B64.unurl(dEnv.getString("blob")))))
            assertEquals(dEnv.getString("blob"), B64.url(down.seal(dSeq, d.getString("plaintext").toByteArray())))

            // The envelope line the phone sends for seq 0 parses back to the vector's fields.
            val line = Envelope.lines(uSeq, uEnv.getString("blob")).single()
            val parsed = JSONObject(line)
            assertEquals(uSeq, parsed.getLong("seq"))
            assertEquals(uEnv.getString("blob"), parsed.getString("blob"))
        }
    }

    @Test(expected = javax.crypto.AEADBadTagException::class)
    fun wrongSeqFailsToOpen() {
        val c = cases()[0]
        val up = EnvelopeCipher(Hex.decode(c.getString("k_up")))
        up.open(1, B64.unurl(c.getJSONObject("up_seq0").getJSONObject("envelope").getString("blob")))
    }

    @Test
    fun nonceIsFourZeroBytesThenBigEndianSeq() {
        assertArrayEquals(byteArrayOf(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2), EnvelopeCipher.nonce(0x0102))
        assertArrayEquals(byteArrayOf(0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8), EnvelopeCipher.nonce(0x0102030405060708))
    }

    @Test
    fun relayTicketsMatchVectors() {
        val r = v.getJSONObject("relay")
        val token = r.getString("relay_token").toByteArray()
        val dev = r.getJSONObject("device_ticket")
        val msg = "droidline device|${dev.getString("server")}|${dev.getString("device")}".toByteArray()
        assertEquals(dev.getString("ticket"), B64.url(Hkdf.hmac(token, msg)))
        val enr = r.getJSONObject("enroll_ticket")
        val msg2 = "droidline enroll|${enr.getString("server")}|${enr.getLong("expiry")}".toByteArray()
        assertEquals(enr.getString("ticket"), B64.url(Hkdf.hmac(token, msg2)))
    }

    @Test
    fun hkdfMatchesRfc5869Case1() {
        val ikm = Hex.decode("0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b")
        val salt = Hex.decode("000102030405060708090a0b0c")
        val info = Hex.decode("f0f1f2f3f4f5f6f7f8f9")
        val prk = Hkdf.extract(salt, ikm)
        assertEquals("077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5", Hex.encode(prk))
        assertEquals(
            "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
            Hex.encode(Hkdf.expand(prk, info, 42)),
        )
    }

    @Test
    fun deviceIdIsEightBase32Chars() {
        repeat(50) {
            val id = DeviceIdentity.newDeviceId()
            assertTrue(id, Regex("[0-9a-v]{8}").matches(id))
        }
    }
}
