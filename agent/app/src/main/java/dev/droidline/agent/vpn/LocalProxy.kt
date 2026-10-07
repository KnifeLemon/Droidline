package dev.droidline.agent.vpn

import java.io.Closeable
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.URI
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/**
 * HTTP proxy on 127.0.0.1 that apps inside the per-app VPN are pointed at. CONNECT and plain HTTP requests
 * are forwarded through [upstream]. Its own sockets are outside the VPN, so they reach the upstream directly.
 */
class LocalProxy(private val upstream: Upstream) : Closeable {
    private val server = ServerSocket(0, 64, InetAddress.getByName("127.0.0.1"))
    private val pool: ExecutorService = Executors.newCachedThreadPool()

    @Volatile private var closed = false

    val port: Int get() = server.localPort

    fun start(): LocalProxy {
        Thread({
            while (!closed) {
                val client = runCatching { server.accept() }.getOrNull() ?: continue
                pool.execute { handle(client) }
            }
        }, "droidline-proxy").apply { isDaemon = true }.start()
        return this
    }

    override fun close() {
        closed = true
        runCatching { server.close() }
        pool.shutdownNow()
    }

    private fun handle(client: Socket) {
        var remote: Socket? = null
        try {
            client.soTimeout = 30_000
            val cin = client.getInputStream()
            val cout = client.getOutputStream()
            val head = Upstream.readHead(cin) ?: return
            val parts = head[0].split(' ')
            if (parts.size < 3) return reply(cout, 400, "Bad Request")
            val method = parts[0].uppercase()
            if (method == "CONNECT") {
                val (h, p) = splitHostPort(parts[1], 443)
                remote = upstream.connect(h, p)
                cout.write("HTTP/1.1 200 Connection established\r\n\r\n".toByteArray(Charsets.ISO_8859_1))
                cout.flush()
            } else {
                val uri = URI(parts[1])
                if (uri.scheme?.lowercase() != "http" || uri.host == null) return reply(cout, 400, "Bad Request")
                val p = if (uri.port > 0) uri.port else 80
                val headers = head.drop(1).filterNot {
                    val k = it.substringBefore(':').trim().lowercase()
                    k == "proxy-connection" || k == "proxy-authorization" || k == "connection"
                }
                val sb = StringBuilder()
                if (upstream.scheme == "http") {
                    remote = upstream.openRaw()
                    sb.append("$method ${parts[1]} ${parts[2]}\r\n")
                    upstream.basicAuthHeader()?.let { sb.append("Proxy-Authorization: $it\r\n") }
                } else {
                    remote = upstream.connect(uri.host, p)
                    val path = (uri.rawPath?.ifEmpty { "/" } ?: "/") + (uri.rawQuery?.let { "?$it" } ?: "")
                    sb.append("$method $path ${parts[2]}\r\n")
                }
                headers.forEach { sb.append(it).append("\r\n") }
                // One request per connection keeps requests to different hosts from sharing a tunnel.
                sb.append("Connection: close\r\n\r\n")
                remote.getOutputStream().apply { write(sb.toString().toByteArray(Charsets.ISO_8859_1)); flush() }
            }
            client.soTimeout = 0
            pipe(client, remote)
        } catch (e: ProxyFailure) {
            runCatching { reply(client.getOutputStream(), 502, "Bad Gateway") }
        } catch (_: Exception) {
        } finally {
            runCatching { remote?.close() }
            runCatching { client.close() }
        }
    }

    private fun pipe(a: Socket, b: Socket) {
        val t = Thread { copy(b.getInputStream(), a.getOutputStream()); runCatching { a.shutdownOutput() } }
        t.isDaemon = true
        t.start()
        copy(a.getInputStream(), b.getOutputStream())
        runCatching { b.shutdownOutput() }
        t.join()
    }

    private fun copy(from: InputStream, to: OutputStream) {
        val buf = ByteArray(16 * 1024)
        try {
            while (true) {
                val n = from.read(buf)
                if (n < 0) break
                to.write(buf, 0, n)
                to.flush()
            }
        } catch (_: IOException) {
        }
    }

    private fun reply(out: OutputStream, code: Int, text: String) {
        out.write("HTTP/1.1 $code $text\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".toByteArray(Charsets.ISO_8859_1))
        out.flush()
    }

    companion object {
        fun splitHostPort(target: String, defaultPort: Int): Pair<String, Int> {
            if (target.startsWith("[")) {
                val close = target.indexOf(']')
                val port = target.substring(close + 1).removePrefix(":").toIntOrNull() ?: defaultPort
                return target.substring(1, close) to port
            }
            val colon = target.lastIndexOf(':')
            return if (colon > 0) target.substring(0, colon) to (target.substring(colon + 1).toIntOrNull() ?: defaultPort)
            else target to defaultPort
        }
    }
}
