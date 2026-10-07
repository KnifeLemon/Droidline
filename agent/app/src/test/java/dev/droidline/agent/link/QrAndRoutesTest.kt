package dev.droidline.agent.link

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.net.URLEncoder

class QrUriTest {
    private val pub = "BMhFSjSLWcDh0LA8svujwvJ3vC56i9IYPH4b97qbWcV4ttNMssslEorivkznbM0T7VCyTUoIgLPttJwwb5VgLqw"
    private val token = "ZGVmZ2hpamtsbW5vcHFycw"
    private val fp = "a".repeat(64)

    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8")

    @Test
    fun parsesEveryField() {
        val addrs = listOf("tcp://192.168.0.10:8779", "tls://203.0.113.5:8779", "wss://droid.example.com/agent").joinToString(",") { enc(it) }
        val uri = "droidline://pair?v=1&s=k3j9d0a2mq&n=${enc("OFFICE PC")}&k=$pub&t=t0k3n1d0.$token&a=$addrs&f=$fp" +
            "&r=${enc("wss://relay.example.com")}&rt=TICKET&re=1791360000"
        val q = QrUri.parse(uri)
        assertEquals("k3j9d0a2mq", q.serverId)
        assertEquals("OFFICE PC", q.serverName)
        assertEquals(65, q.serverPub.size)
        assertEquals("t0k3n1d0", q.tokenId)
        assertArrayEquals(ByteArray(16) { (100 + it).toByte() }, q.token)
        assertEquals(listOf("tcp://192.168.0.10:8779", "tls://203.0.113.5:8779", "wss://droid.example.com/agent"), q.addresses)
        assertEquals(fp, q.tlsFingerprint)
        assertEquals("wss://relay.example.com", q.relayUrl)
        assertEquals("TICKET", q.relayTicket)
        assertEquals(1791360000L, q.relayExpiry)
    }

    @Test
    fun addressContainingEncodedCommaStaysOneAddress() {
        val odd = "wss://x.example.com/agent?a=1,2"
        val q = QrUri.parse("droidline://pair?v=1&s=s1&k=$pub&t=id.$token&a=${enc(odd)},${enc("tcp://10.0.0.2:8779")}")
        assertEquals(listOf(odd, "tcp://10.0.0.2:8779"), q.addresses)
        assertEquals("s1", q.serverName)
        assertNull(q.relayUrl)
    }

    @Test(expected = IllegalArgumentException::class)
    fun tlsAddressNeedsFingerprint() {
        QrUri.parse("droidline://pair?v=1&s=s1&k=$pub&t=id.$token&a=${enc("tls://1.2.3.4:8779")}")
    }

    @Test(expected = IllegalArgumentException::class)
    fun tokenMustBe16Bytes() {
        QrUri.parse("droidline://pair?v=1&s=s1&k=$pub&t=id.AAAA")
    }

    @Test(expected = IllegalArgumentException::class)
    fun versionIsRequired() {
        QrUri.parse("droidline://pair?s=s1&k=$pub&t=id.$token")
    }

    @Test(expected = IllegalArgumentException::class)
    fun otherSchemesAreRejected() {
        QrUri.parse("https://example.com/pair?v=1")
    }
}

class RoutesTest {
    @Test
    fun orderIsLanDirectTunnelRelay() {
        val routes = Routes.order(
            discovered = HostPort("192.168.0.10", 8779),
            addresses = listOf("wss://t.example.com/agent", "tls://203.0.113.5:8779", "tcp://192.168.0.10:8779", "tcp://10.0.0.5:8779"),
            relayUrl = "wss://relay.example.com/",
            relayBearer = "tkt",
            serverId = "k3j9d0a2mq",
            deviceId = "a1b2c3d4",
        )
        assertEquals(listOf("lan", "lan", "direct", "tunnel", "relay"), routes.map { it.kind })
        assertEquals("tcp://192.168.0.10:8779", routes[0].url)
        assertEquals("tcp://10.0.0.5:8779", routes[1].url)
        assertEquals("wss://relay.example.com/v1/device?server=k3j9d0a2mq&device=a1b2c3d4", routes[4].url)
        assertEquals("tkt", routes[4].bearer)
    }

    @Test
    fun enrollRelayUrlCarriesExpiry() {
        assertEquals(
            "wss://r.example.com/v1/device?server=s&device=d&expiry=1791360000",
            Routes.relayDeviceUrl("wss://r.example.com", "s", "d", 1791360000),
        )
    }

    @Test
    fun relayNeedsATicket() {
        val routes = Routes.order(null, emptyList(), "wss://relay.example.com", null, "s", "d")
        assertEquals(0, routes.size)
    }

    @Test
    fun hostPortHandlesIpv6AndDefaults() {
        assertEquals(HostPort("2001:db8::1", 9000), Routes.hostPort("tcp://[2001:db8::1]:9000"))
        assertEquals(HostPort("2001:db8::1", 8779), Routes.hostPort("tls://[2001:db8::1]"))
        assertEquals(HostPort("pc.local", 8779), Routes.hostPort("tcp://pc.local"))
        assertEquals(HostPort("1.2.3.4", 1234), Routes.hostPort("tcp://1.2.3.4:1234"))
    }

    @Test
    fun discoveryRequestAndReply() {
        val open = org.json.JSONObject(Discovery.request(null))
        assertEquals("discover", open.getString("droidline"))
        assertEquals(1, open.getInt("v"))
        assertEquals(false, open.has("server"))
        assertEquals("k3j9d0a2mq", org.json.JSONObject(Discovery.request("k3j9d0a2mq")).getString("server"))
        val reply = """{"droidline":"here","v":1,"server":"k3j9d0a2mq","name":"OFFICE-PC","port":8779,"pub":"BMhFSjSLWcDh0LA8svujwvJ3vC56i9IYPH4b97qbWcV4ttNMssslEorivkznbM0T7VCyTUoIgLPttJwwb5VgLqw","pairing":true}"""
        val s = Discovery.parseReply(reply, "192.168.0.10")!!
        assertEquals("k3j9d0a2mq", s.serverId)
        assertEquals("OFFICE-PC", s.name)
        assertEquals("192.168.0.10", s.host)
        assertEquals(true, s.pairing)
        assertNull(Discovery.parseReply("""{"droidline":"discover","v":1}""", "x"))
    }
}
