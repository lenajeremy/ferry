package app.ferry

import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.InetAddress
import java.net.ServerSocket
import kotlin.concurrent.thread

class NearbyTest {
    // MARK: Which gateway is the computer

    @Test fun windowsHotspot() = assertEquals(listOf("192.168.137.1"), Nearby.gateways(listOf("192.168.137.23")))

    @Test fun linuxHotspot() = assertEquals(listOf("10.42.0.1"), Nearby.gateways(listOf("10.42.0.57")))

    @Test fun anOrdinaryNetworkHasNone() = assertEquals(emptyList<String>(), Nearby.gateways(listOf("192.168.1.5", "10.0.2.15")))

    @Test fun eachGatewayOnce() =
        assertEquals(listOf("192.168.137.1"), Nearby.gateways(listOf("192.168.1.5", "192.168.137.80", "192.168.137.81")))

    @Test fun bothKinds() =
        assertEquals(listOf("192.168.137.1", "10.42.0.1"), Nearby.gateways(listOf("192.168.137.80", "10.42.0.9")))

    @Test fun notThisPhoneItself() = assertEquals(emptyList<String>(), Nearby.gateways(listOf("192.168.137.1")))

    @Test fun lookAlikesAreNot() =
        assertEquals(emptyList<String>(), Nearby.gateways(listOf("192.168.13.7", "10.42.1.5", "110.42.0.5", "192.168.1370.2")))

    // MARK: Opening a connection, and nothing more

    @Test fun opensAndSendsNothing() {
        ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { server ->
            var received = -2
            val reader = thread {
                server.accept().use { s ->
                    s.soTimeout = 3000
                    received = s.getInputStream().read() // -1: closed without a byte sent
                }
            }
            val opened = runBlocking { Nearby.opens("127.0.0.1", server.localPort, 2500) }
            reader.join(4000)
            assertTrue(opened)
            assertEquals(-1, received)
        }
    }

    @Test fun aClosedPortDoesntOpen() {
        val port = ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { it.localPort }
        assertFalse(runBlocking { Nearby.opens("127.0.0.1", port, 2500) })
    }

    @Test fun cancellingStopsAtOnce() {
        val start = System.nanoTime()
        // An address nothing answers at: the connection would wait its whole ten seconds.
        val result = runBlocking { withTimeoutOrNull(200) { Nearby.opens("10.255.255.1", 9, 10_000) } }
        val ms = (System.nanoTime() - start) / 1_000_000
        assertTrue("took $ms ms", ms < 1500)
        assertTrue(result == null || result == false)
    }
}
