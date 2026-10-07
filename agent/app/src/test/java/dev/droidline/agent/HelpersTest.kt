package dev.droidline.agent

import dev.droidline.agent.device.Apps
import dev.droidline.agent.device.DeviceCommands
import dev.droidline.agent.service.UpdateChecker
import dev.droidline.agent.vpn.LocalProxy
import dev.droidline.agent.vpn.Upstream
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HelpersTest {
    @Test
    fun releaseTagsCompareNumerically() {
        assertTrue(UpdateChecker.isNewer("v0.2.0", "0.1.0"))
        assertTrue(UpdateChecker.isNewer("v0.1.10", "0.1.9"))
        assertFalse(UpdateChecker.isNewer("v0.1.0", "0.1.0"))
        assertFalse(UpdateChecker.isNewer("v0.0.9", "0.1.0"))
        assertFalse(UpdateChecker.isNewer("nightly", "0.1.0"))
    }

    @Test
    fun similarPackagesAreSuggested() {
        val installed = listOf("com.kakao.talk", "com.kakao.taxi", "com.android.chrome", "com.google.android.youtube")
        assertEquals("com.kakao.talk", Apps.similar("com.kakao.tlak", installed).first())
        assertEquals(listOf("com.android.chrome"), Apps.similar("com.chrome", installed).take(1))
        assertTrue(Apps.similar("org.nothing.like.it", installed).isEmpty())
        assertTrue(Apps.similar("com.kakao.talk", installed, limit = 3).size <= 3)
    }

    @Test
    fun chromeUrlGetsHttpsWhenNoScheme() {
        assertEquals("https://naver.com", DeviceCommands.chromeUrl("naver.com"))
        assertEquals("https://localhost:3000/x", DeviceCommands.chromeUrl("localhost:3000/x"))
        assertEquals("http://example.com", DeviceCommands.chromeUrl("http://example.com"))
        assertEquals("about:blank", DeviceCommands.chromeUrl("about:blank"))
    }

    @Test
    fun upstreamUrlsParseWithAndWithoutCredentials() {
        val s = Upstream.parse("socks5://user:p%40ss@1.2.3.4:1080")
        assertEquals("socks5", s.scheme)
        assertEquals("1.2.3.4", s.host)
        assertEquals(1080, s.port)
        assertEquals("user", s.user)
        assertEquals("p@ss", s.pass)
        assertEquals("socks5://1.2.3.4:1080", s.display)
        val h = Upstream.parse("http://proxy.example.com:3128")
        assertNull(h.user)
        assertNull(h.basicAuthHeader())
        assertEquals("Basic dXNlcjpwQHNz", s.basicAuthHeader())
    }

    @Test(expected = IllegalArgumentException::class)
    fun upstreamNeedsAPort() {
        Upstream.parse("http://proxy.example.com")
    }

    @Test(expected = IllegalArgumentException::class)
    fun upstreamSchemeMustBeHttpOrSocks5() {
        Upstream.parse("https://proxy.example.com:443")
    }

    @Test
    fun connectTargetsSplit() {
        assertEquals("example.com" to 443, LocalProxy.splitHostPort("example.com:443", 80))
        assertEquals("2001:db8::1" to 8443, LocalProxy.splitHostPort("[2001:db8::1]:8443", 80))
        assertEquals("example.com" to 80, LocalProxy.splitHostPort("example.com", 80))
    }
}
