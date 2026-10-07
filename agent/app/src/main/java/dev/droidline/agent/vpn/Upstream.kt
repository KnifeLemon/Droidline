package dev.droidline.agent.vpn

import dev.droidline.agent.crypto.B64
import java.io.BufferedInputStream
import java.io.IOException
import java.io.InputStream
import java.net.ConnectException
import java.net.InetSocketAddress
import java.net.NoRouteToHostException
import java.net.Socket
import java.net.SocketTimeoutException
import java.net.URI
import java.net.URLDecoder
import java.net.UnknownHostException

/** Why an upstream proxy failed, as reported in PROXY_FAILED: auth, timeout, refused or protocol. */
class ProxyFailure(val reason: String, message: String) : IOException(message)

/** One upstream proxy: http:// with CONNECT and optional Basic auth, or socks5:// with optional RFC 1929 auth. */
class Upstream(val scheme: String, val host: String, val port: Int, val user: String?, val pass: String?) {
    /** The upstream without credentials, safe to report. */
    val display: String get() = "$scheme://${if (host.contains(':')) "[$host]" else host}:$port"

    /** A socket already tunnelled to [targetHost]:[targetPort]. The target name is resolved by the upstream. */
    fun connect(targetHost: String, targetPort: Int, timeoutMs: Int = 10_000): Socket {
        val s = openRaw(timeoutMs)
        try {
            s.soTimeout = timeoutMs
            if (scheme == "socks5") socks5(s, targetHost, targetPort) else httpConnect(s, targetHost, targetPort)
            s.soTimeout = 0
            return s
        } catch (e: SocketTimeoutException) {
            s.close()
            throw ProxyFailure("timeout", "upstream did not answer in time")
        } catch (e: ProxyFailure) {
            s.close()
            throw e
        } catch (e: IOException) {
            s.close()
            throw ProxyFailure("protocol", e.message ?: "upstream closed the connection")
        }
    }

    /** A plain connection to the upstream itself, used to forward absolute-form HTTP requests to an http upstream. */
    fun openRaw(timeoutMs: Int = 10_000): Socket {
        val s = Socket()
        try {
            s.tcpNoDelay = true
            s.connect(InetSocketAddress(host, port), timeoutMs)
            return s
        } catch (e: SocketTimeoutException) {
            s.close()
            throw ProxyFailure("timeout", "could not reach $display")
        } catch (e: ConnectException) {
            s.close()
            throw ProxyFailure("refused", "$display refused the connection")
        } catch (e: UnknownHostException) {
            s.close()
            throw ProxyFailure("refused", "unknown host $host")
        } catch (e: NoRouteToHostException) {
            s.close()
            throw ProxyFailure("refused", "no route to $host")
        }
    }

    fun basicAuthHeader(): String? =
        if (user == null) null else "Basic " + B64.std("$user:${pass.orEmpty()}".toByteArray(Charsets.UTF_8))

    private fun httpConnect(s: Socket, h: String, p: Int) {
        val target = "${if (h.contains(':')) "[$h]" else h}:$p"
        val req = StringBuilder("CONNECT $target HTTP/1.1\r\nHost: $target\r\n")
        basicAuthHeader()?.let { req.append("Proxy-Authorization: $it\r\n") }
        req.append("\r\n")
        s.getOutputStream().apply { write(req.toString().toByteArray(Charsets.ISO_8859_1)); flush() }
        val head = readHead(s.getInputStream()) ?: throw ProxyFailure("protocol", "upstream closed during CONNECT")
        val status = head.firstOrNull()?.split(' ')?.getOrNull(1)?.toIntOrNull()
            ?: throw ProxyFailure("protocol", "not an HTTP proxy")
        when (status) {
            in 200..299 -> Unit
            407 -> throw ProxyFailure("auth", "upstream rejected the credentials")
            403, 502, 503, 504 -> throw ProxyFailure("refused", "upstream answered $status")
            else -> throw ProxyFailure("protocol", "upstream answered $status")
        }
    }

    private fun socks5(s: Socket, h: String, p: Int) {
        val out = s.getOutputStream()
        val inp = s.getInputStream()
        val methods = if (user != null) byteArrayOf(0x00, 0x02) else byteArrayOf(0x00)
        out.write(byteArrayOf(0x05, methods.size.toByte()) + methods)
        out.flush()
        val choice = readN(inp, 2)
        if (choice[0] != 0x05.toByte()) throw ProxyFailure("protocol", "not a SOCKS5 proxy")
        when (choice[1].toInt() and 0xff) {
            0x00 -> Unit
            0x02 -> {
                val u = user.orEmpty().toByteArray(Charsets.UTF_8)
                val pw = pass.orEmpty().toByteArray(Charsets.UTF_8)
                if (u.size > 255 || pw.size > 255) throw ProxyFailure("auth", "user or password longer than 255 bytes")
                out.write(byteArrayOf(0x01, u.size.toByte()) + u + byteArrayOf(pw.size.toByte()) + pw)
                out.flush()
                val r = readN(inp, 2)
                if (r[1] != 0x00.toByte()) throw ProxyFailure("auth", "upstream rejected the credentials")
            }
            0xff -> throw ProxyFailure("auth", "upstream accepts none of the offered auth methods")
            else -> throw ProxyFailure("protocol", "unexpected SOCKS5 method")
        }
        val name = h.toByteArray(Charsets.UTF_8)
        if (name.size > 255) throw ProxyFailure("protocol", "host name too long")
        out.write(byteArrayOf(0x05, 0x01, 0x00, 0x03, name.size.toByte()) + name + byteArrayOf((p shr 8).toByte(), p.toByte()))
        out.flush()
        val head = readN(inp, 4)
        val rep = head[1].toInt() and 0xff
        if (rep != 0) throw ProxyFailure(if (rep in 2..5) "refused" else "protocol", "SOCKS5 reply $rep")
        val skip = when (head[3].toInt()) {
            0x01 -> 4
            0x04 -> 16
            0x03 -> readN(inp, 1)[0].toInt() and 0xff
            else -> throw ProxyFailure("protocol", "bad SOCKS5 address type")
        }
        readN(inp, skip + 2)
    }

    companion object {
        fun parse(url: String): Upstream {
            val u = runCatching { URI(url) }.getOrNull() ?: throw IllegalArgumentException("not a proxy URL")
            val scheme = u.scheme?.lowercase()
            if (scheme != "http" && scheme != "socks5") throw IllegalArgumentException("proxy scheme must be socks5 or http")
            val host = u.host?.removePrefix("[")?.removeSuffix("]") ?: throw IllegalArgumentException("proxy URL has no host")
            if (u.port <= 0) throw IllegalArgumentException("proxy URL needs a port")
            val info = u.rawUserInfo
            val user = info?.substringBefore(':')?.let { URLDecoder.decode(it, "UTF-8") }
            val pass = info?.takeIf { it.contains(':') }?.substringAfter(':')?.let { URLDecoder.decode(it, "UTF-8") }
            return Upstream(scheme, host, u.port, user, pass)
        }

        fun readN(inp: InputStream, n: Int): ByteArray {
            val b = ByteArray(n)
            var off = 0
            while (off < n) {
                val r = inp.read(b, off, n - off)
                if (r < 0) throw IOException("connection closed")
                off += r
            }
            return b
        }

        /** Reads an HTTP head up to the blank line, byte by byte so no body bytes are consumed. */
        fun readHead(inp: InputStream, max: Int = 64 * 1024): List<String>? {
            val sb = StringBuilder()
            while (sb.length < max) {
                val c = inp.read()
                if (c < 0) return if (sb.isEmpty()) null else throw IOException("connection closed in headers")
                sb.append(c.toChar())
                if (sb.endsWith("\r\n\r\n") || sb.endsWith("\n\n")) {
                    return sb.toString().split("\r\n", "\n").filter { it.isNotEmpty() }
                }
            }
            throw IOException("headers too large")
        }

        fun buffered(inp: InputStream) = if (inp is BufferedInputStream) inp else BufferedInputStream(inp)
    }
}
