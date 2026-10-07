package dev.droidline.agent.link

import dev.droidline.agent.crypto.B64
import dev.droidline.agent.crypto.EnvelopeCipher
import dev.droidline.agent.crypto.SessionKeys
import java.io.Closeable
import java.io.IOException
import java.security.GeneralSecurityException

/** One connection after the handshake: every line is an envelope (PROTOCOL 4.5). */
class SecureLink(
    private val transport: LineTransport,
    val keys: SessionKeys,
    val route: String,
    val serverId: String,
    private val pacer: LinePacer? = if (route == Route.RELAY) LinePacer() else null,
) : Closeable {
    /** WebSocket close code from the peer, when the transport has one. */
    val closeCode: Int? get() = transport.closeCode

    private val up = EnvelopeCipher(keys.up)
    private val down = EnvelopeCipher(keys.down)
    private val assembler = Envelope.Assembler()
    private var sendSeq = 0L

    @Volatile var closed = false
        private set

    @Volatile var lastReceived = System.currentTimeMillis()
        private set

    /** Encrypts and writes one inner line; the seq lock keeps parts of different envelopes from interleaving. */
    @Synchronized
    fun send(plain: String) {
        if (closed) throw IOException("link closed")
        val seq = sendSeq++
        val blob = B64.url(up.seal(seq, plain.toByteArray(Charsets.UTF_8)))
        for (line in Envelope.lines(seq, blob)) {
            pacer?.acquire()
            transport.writeLine(line)
        }
    }

    /** Next decrypted inner line, or null at end of stream. A bad seq or tag throws. */
    fun receive(): String? {
        while (true) {
            val line = transport.readLine() ?: return null
            lastReceived = System.currentTimeMillis()
            val seq = assembler.expectedSeq
            val blob = assembler.feed(line) ?: continue
            val plain = try {
                down.open(seq, B64.unurl(blob))
            } catch (e: GeneralSecurityException) {
                throw ProtocolException("envelope $seq failed to open")
            } catch (e: IllegalArgumentException) {
                throw ProtocolException("envelope $seq is not base64url")
            }
            return String(plain, Charsets.UTF_8)
        }
    }

    override fun close() {
        closed = true
        transport.close()
    }
}
