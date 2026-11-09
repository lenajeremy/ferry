package app.ferry

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Reading the QR code on a computer's page, with and without the computer's own Wi-Fi in it. */
class PairingParseTest {
    private val fp = "gZ_x2YJh2S2DD_40s0j8a8"

    private fun read(text: String) = Pairing.parse(text) ?: throw AssertionError("Not read as Ferry's code: $text")

    @Test fun aCodeWithoutWifi() {
        val link = read("http://192.168.1.20:7070/?k=k7m2q9xa&f=$fp&s=7071")
        assertEquals("192.168.1.20", link.host)
        assertEquals(7070, link.port)
        assertEquals("k7m2q9xa", link.code)
        assertEquals(fp, link.fingerprint)
        assertEquals(7071, link.tlsPort)
        assertNull(link.ssid)
        assertNull(link.password)
    }

    @Test fun aCodeWithTheComputersOwnWifi() {
        val link = read("http://192.168.137.1:7070/?k=k7m2q9xa&f=$fp&s=7071&w=Ferry-Studio-PC&p=abcdefgh2345")
        assertEquals("192.168.137.1", link.host)
        assertEquals(7071, link.tlsPort)
        assertEquals("Ferry-Studio-PC", link.ssid)
        assertEquals("abcdefgh2345", link.password)
    }

    // As Go's url.QueryEscape writes them: a space is +, a + is %2B.
    @Test fun plusIsASpaceAndEscapesAreUndone() {
        val link = read("http://10.42.0.1:7070/?k=k7m2q9xa&f=$fp&s=7071&w=Alex%27s+PC+%26+Co&p=a+b%2Bc%26d%3De%25%2F%3F%23")
        assertEquals("Alex's PC & Co", link.ssid)
        assertEquals("a b+c&d=e%/?#", link.password)
    }

    @Test fun lettersBeyondAscii() {
        val link = read("http://192.168.137.1:7070/?k=k7m2q9xa&f=$fp&s=7071&w=Caf%C3%A9+Wi-Fi&p=abcdefgh2345")
        assertEquals("Café Wi-Fi", link.ssid)
    }

    @Test fun aWifiWithoutAPassword() {
        val link = read("http://192.168.137.1:7070/?k=k7m2q9xa&f=$fp&s=7071&w=Ferry-PC")
        assertEquals("Ferry-PC", link.ssid)
        assertNull(link.password)
    }

    @Test fun aPasswordWithoutAWifiIsIgnored() {
        val link = read("http://192.168.1.20:7070/?k=k7m2q9xa&f=$fp&s=7071&p=abcdefgh2345")
        assertNull(link.ssid)
        assertNull(link.password)
    }

    @Test fun anEmptyWifiNameIsNone() {
        val link = read("http://192.168.1.20:7070/?k=k7m2q9xa&f=$fp&s=7071&w=&p=")
        assertNull(link.ssid)
        assertNull(link.password)
    }

    @Test fun notFerrysCode() {
        assertNull(Pairing.parse("WIFI:T:WPA;S:Ferry-PC;P:abcdefgh2345;;"))
        assertNull(Pairing.parse("http://192.168.1.20:7070/?w=Ferry-PC&p=abcdefgh2345"))
    }
}
