package dev.droidline.agent.link

import android.annotation.SuppressLint
import dev.droidline.agent.crypto.Hex
import dev.droidline.agent.crypto.sha256
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.ByteArrayOutputStream
import java.io.Closeable
import java.io.IOException
import java.io.InputStream
import java.net.InetSocketAddress
import java.net.Socket
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.X509TrustManager

/** A bidirectional stream of NDJSON lines. Blocking; call from an IO thread. */
interface LineTransport : Closeable {
    /** Next line without the newline, or null at end of stream. */
    fun readLine(): String?

    fun writeLine(line: String)

    /** Close code sent by the peer, for transports that have one (WebSocket). */
    val closeCode: Int? get() = null
}

/** Lines are at most 1 MiB (PROTOCOL 1); a little headroom covers the envelope fields. */
const val MAX_LINE = 1024 * 1024 + 4096

class LineReader(stream: InputStream, private val max: Int = MAX_LINE) {
    private val input = BufferedInputStream(stream, 64 * 1024)
    private val buf = ByteArrayOutputStream(8192)

    fun readLine(): String? {
        buf.reset()
        while (true) {
            val b = input.read()
            if (b < 0) return if (buf.size() == 0) null else throw IOException("stream ended mid-line")
            if (b == '\n'.code) break
            if (buf.size() >= max) throw IOException("line too long")
            buf.write(b)
        }
        var s = buf.toString("UTF-8")
        if (s.endsWith('\r')) s = s.dropLast(1)
        return s
    }
}

open class SocketTransport(private val socket: Socket) : LineTransport {
    private val reader = LineReader(socket.getInputStream())
    private val out = BufferedOutputStream(socket.getOutputStream(), 64 * 1024)

    override fun readLine(): String? = reader.readLine()

    @Synchronized
    override fun writeLine(line: String) {
        out.write(line.toByteArray(Charsets.UTF_8))
        out.write('\n'.code)
        out.flush()
    }

    override fun close() {
        runCatching { socket.close() }
    }

    companion object {
        fun tcp(host: String, port: Int, timeoutMs: Int): SocketTransport {
            val s = Socket()
            try {
                s.tcpNoDelay = true
                s.keepAlive = true
                s.connect(InetSocketAddress(host, port), timeoutMs)
            } catch (e: Exception) {
                runCatching { s.close() }
                throw e
            }
            return SocketTransport(s)
        }

        /** TLS to a self-signed server certificate, accepted only when its SHA-256 matches [fingerprintHex]. */
        fun tls(host: String, port: Int, fingerprintHex: String, timeoutMs: Int): SocketTransport {
            val ctx = SSLContext.getInstance("TLS")
            ctx.init(null, arrayOf(PinnedCertTrust(fingerprintHex)), null)
            val raw = Socket()
            try {
                raw.tcpNoDelay = true
                raw.connect(InetSocketAddress(host, port), timeoutMs)
                val ssl = ctx.socketFactory.createSocket(raw, host, port, true) as SSLSocket
                ssl.soTimeout = timeoutMs
                ssl.startHandshake()
                ssl.soTimeout = 0
                return SocketTransport(ssl)
            } catch (e: Exception) {
                runCatching { raw.close() }
                throw e
            }
        }
    }
}

/** The pin replaces chain and hostname checks: the server certificate is self-signed and often addressed by IP. */
@SuppressLint("CustomX509TrustManager")
class PinnedCertTrust(fingerprintHex: String) : X509TrustManager {
    private val expected = Hex.decode(fingerprintHex.lowercase().replace(":", ""))

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        val leaf = chain?.firstOrNull() ?: throw CertificateException("no certificate")
        if (!MessageDigest.isEqual(sha256(leaf.encoded), expected)) throw CertificateException("certificate fingerprint mismatch")
    }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) =
        throw CertificateException("client certificates are not used")

    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
}

/** WebSocket for tunnel and relay routes: each text frame is one line without the newline. */
class WsTransport private constructor() : LineTransport {
    private val inbox = LinkedBlockingQueue<Any>()
    private var ws: WebSocket? = null

    @Volatile override var closeCode: Int? = null
        private set

    private object Closed

    override fun readLine(): String? {
        val item = inbox.take()
        if (item === Closed) {
            inbox.put(Closed)
            return null
        }
        if (item is Throwable) {
            inbox.put(Closed)
            throw IOException(item.message, item)
        }
        return item as String
    }

    override fun writeLine(line: String) {
        val socket = ws ?: throw IOException("not connected")
        if (!socket.send(line)) throw IOException("websocket closed or send queue full")
    }

    override fun close() {
        ws?.close(1000, null)
        ws?.cancel()
        inbox.put(Closed)
    }

    companion object {
        private val baseClient by lazy {
            OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(0, TimeUnit.MILLISECONDS)
                .build()
        }

        fun connect(url: String, bearer: String?, timeoutMs: Long): WsTransport {
            val transport = WsTransport()
            val opened = LinkedBlockingQueue<Any>(1)
            val req = Request.Builder().url(url).apply { bearer?.let { header("Authorization", "Bearer $it") } }.build()
            val socket = baseClient.newWebSocket(req, object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    opened.offer(true)
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    if (text.length > MAX_LINE) {
                        transport.inbox.put(IOException("line too long"))
                        webSocket.cancel()
                    } else {
                        transport.inbox.put(text)
                    }
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    transport.closeCode = code
                    webSocket.close(1000, null)
                    transport.inbox.put(Closed)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    if (transport.closeCode == null) transport.closeCode = code
                    transport.inbox.put(Closed)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    // A relay answers 409 when an enroll ticket would replace a phone already on a device ticket.
                    val err = if (response?.code == 409) IOException("relay busy (409)") else t
                    opened.offer(err)
                    transport.inbox.put(err)
                }
            })
            transport.ws = socket
            when (val r = opened.poll(timeoutMs, TimeUnit.MILLISECONDS)) {
                true -> return transport
                is Throwable -> throw IOException("websocket failed: ${r.message}", r)
                else -> {
                    socket.cancel()
                    throw IOException("websocket open timed out")
                }
            }
        }
    }
}
