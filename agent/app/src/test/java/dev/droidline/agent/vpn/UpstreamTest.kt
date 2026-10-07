package dev.droidline.agent.vpn

import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test
import java.io.DataInputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import kotlin.concurrent.thread

/** Drives Upstream against minimal fake proxies on localhost. */
class UpstreamTest {
    private fun server(handler: (Socket) -> Unit): ServerSocket {
        val ss = ServerSocket(0, 1, InetAddress.getLoopbackAddress())
        thread(isDaemon = true) {
            runCatching { ss.accept().use(handler) }
            runCatching { ss.close() }
        }
        return ss
    }

    /** SOCKS5 server that wants user/pass auth, then echoes one line after CONNECT. */
    private fun socks5(user: String, pass: String, captured: MutableList<String>) = server { s ->
        val inp = DataInputStream(s.getInputStream())
        val out = s.getOutputStream()
        inp.readByte()
        val n = inp.readUnsignedByte()
        ByteArray(n).also { inp.readFully(it) }
        out.write(byteArrayOf(5, 2))
        inp.readByte()
        val u = ByteArray(inp.readUnsignedByte()).also { inp.readFully(it) }
        val p = ByteArray(inp.readUnsignedByte()).also { inp.readFully(it) }
        val ok = String(u) == user && String(p) == pass
        out.write(byteArrayOf(1, if (ok) 0 else 1))
        if (!ok) return@server
        val head = ByteArray(4).also { inp.readFully(it) }
        val host = ByteArray(inp.readUnsignedByte()).also { inp.readFully(it) }
        val port = inp.readUnsignedShort()
        captured += "${head[1]} ${head[3]} ${String(host)}:$port"
        out.write(byteArrayOf(5, 0, 0, 1, 0, 0, 0, 0, 0, 0))
        val b = ByteArray(5).also { inp.readFully(it) }
        out.write(b)
        out.flush()
    }

    @Test
    fun socks5WithAuthTunnelsAndResolvesRemotely() {
        val captured = mutableListOf<String>()
        val ss = socks5("me", "pw", captured)
        val up = Upstream("socks5", "127.0.0.1", ss.localPort, "me", "pw")
        up.connect("api.ipify.org", 443, 3000).use { s ->
            s.getOutputStream().write("hello".toByteArray())
            val back = ByteArray(5).also { DataInputStream(s.getInputStream()).readFully(it) }
            assertEquals("hello", String(back))
        }
        assertEquals(listOf("1 3 api.ipify.org:443"), captured)
    }

    @Test
    fun socks5WrongPasswordIsAuth() {
        val ss = socks5("me", "pw", mutableListOf())
        expectReason("auth") { Upstream("socks5", "127.0.0.1", ss.localPort, "me", "nope").connect("x.example", 443, 3000) }
    }

    @Test
    fun httpConnectSendsBasicAuth() {
        val seen = mutableListOf<String>()
        val ss = server { s ->
            val head = Upstream.readHead(s.getInputStream())!!
            seen += head
            s.getOutputStream().write("HTTP/1.1 200 Connection established\r\n\r\n".toByteArray())
        }
        Upstream("http", "127.0.0.1", ss.localPort, "user", "p@ss").connect("example.com", 443, 3000).close()
        assertEquals("CONNECT example.com:443 HTTP/1.1", seen[0])
        assertEquals(true, seen.contains("Proxy-Authorization: Basic dXNlcjpwQHNz"))
    }

    @Test
    fun http407IsAuth() {
        val ss = server { s ->
            Upstream.readHead(s.getInputStream())
            s.getOutputStream().write("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n".toByteArray())
        }
        expectReason("auth") { Upstream("http", "127.0.0.1", ss.localPort, null, null).connect("example.com", 443, 3000) }
    }

    @Test
    fun garbageIsProtocol() {
        val ss = server { s ->
            Upstream.readHead(s.getInputStream())
            s.getOutputStream().write("SSH-2.0-OpenSSH\r\n\r\n".toByteArray())
        }
        expectReason("protocol") { Upstream("http", "127.0.0.1", ss.localPort, null, null).connect("example.com", 443, 3000) }
    }

    @Test
    fun closedPortIsRefused() {
        val port = ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { it.localPort }
        expectReason("refused") { Upstream("socks5", "127.0.0.1", port, null, null).connect("example.com", 443, 3000) }
    }

    @Test
    fun silentUpstreamIsTimeout() {
        val ss = server { s -> Thread.sleep(1500); s.close() }
        expectReason("timeout") { Upstream("http", "127.0.0.1", ss.localPort, null, null).connect("example.com", 443, 300) }
    }

    private fun expectReason(reason: String, block: () -> Unit) {
        try {
            block()
            fail("expected PROXY_FAILED $reason")
        } catch (e: ProxyFailure) {
            assertEquals(reason, e.reason)
        }
    }
}
