package app.ferry

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.util.Collections

/** Finding a paired computer again, with the network stood in for. */
class ReconnectTest {
    private val token = "phone-token-0123456789"
    private val secure = FerryComputer(
        id = "studio-pc", name = "Studio PC", host = "192.168.1.20", port = 7070, tlsPort = 7071,
        ca = "Q0E=", token = token,
    )

    /**
     * A network where each address in [answers] answers after its time (opening, or not); any other refuses
     * at once. Bonjour gives [bonjour] after [bonjourMs], or nothing; null [bonjourMs] waits for ever.
     * Everything asked of it is written down, and what was cancelled.
     */
    private class FakeNetwork(
        val answers: Map<String, Pair<Long, Boolean>> = emptyMap(),
        val bonjour: String? = null,
        val bonjourMs: Long? = 0,
        val own: List<String> = emptyList(),
        val broken: Set<String> = emptySet(),
    ) : Probes {
        val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())
        val cancelled: MutableList<String> = Collections.synchronizedList(mutableListOf())

        override suspend fun opens(host: String, port: Int, timeoutMs: Long): Boolean {
            asked += "open $host $port"
            if (host in broken) throw IOException("broken")
            val (ms, opens) = answers[host] ?: return false
            try {
                delay(ms)
            } catch (e: CancellationException) {
                cancelled += host
                throw e
            }
            return opens
        }

        override suspend fun bonjour(id: String, timeoutMs: Long): String? {
            asked += "bonjour $id"
            try {
                delay(bonjourMs ?: Long.MAX_VALUE)
            } catch (e: CancellationException) {
                cancelled += "bonjour"
                throw e
            }
            return bonjour
        }

        override fun ownAddresses() = own
    }

    private fun timed(block: suspend () -> FerryComputer?): Pair<FerryComputer?, Long> {
        val start = System.nanoTime()
        val found = runBlocking { block() }
        return found to (System.nanoTime() - start) / 1_000_000
    }

    @Test fun atItsSavedAddress() {
        val net = FakeNetwork(answers = mapOf("192.168.1.20" to (10L to true)), bonjourMs = null)
        val (found, ms) = timed { Reconnect.find(secure, net) }
        assertSame(secure, found)
        assertTrue("took $ms ms", ms < 1500)
        assertEquals(listOf("bonjour"), net.cancelled)
    }

    @Test fun theFirstToAnswerWins() {
        val net = FakeNetwork(
            answers = mapOf("192.168.1.20" to (600L to true), "192.168.137.1" to (20L to true)),
            bonjourMs = null, own = listOf("192.168.137.42"),
        )
        val (found, ms) = timed { Reconnect.find(secure, net) }
        assertEquals("192.168.137.1", found?.host)
        assertTrue("took $ms ms", ms < 500)
        assertEquals(setOf("192.168.1.20", "bonjour"), net.cancelled.toSet())
    }

    @Test fun aComputerWithANewAddress() {
        val net = FakeNetwork(answers = mapOf("192.168.1.77" to (10L to true)), bonjour = "192.168.1.77", bonjourMs = 30)
        val (found, _) = timed { Reconnect.find(secure, net) }
        assertEquals(secure.copy(host = "192.168.1.77"), found)
    }

    @Test fun onTheComputersOwnWifi() {
        val net = FakeNetwork(answers = mapOf("10.42.0.1" to (10L to true)), bonjourMs = null, own = listOf("10.42.0.83"))
        val (found, _) = timed { Reconnect.find(secure, net) }
        assertEquals(secure.copy(host = "10.42.0.1"), found)
    }

    @Test fun bonjourAtTheSameAddressIsntAMove() {
        val net = FakeNetwork(bonjour = "192.168.1.20", bonjourMs = 10)
        val (found, _) = timed { Reconnect.find(secure, net) }
        assertNull(found)
        assertEquals(1, net.asked.count { it.startsWith("open 192.168.1.20") })
    }

    @Test fun nowhere() {
        val net = FakeNetwork(answers = mapOf("192.168.1.20" to (30L to false)), own = listOf("192.168.137.9"))
        val (found, _) = timed { Reconnect.find(secure, net) }
        assertNull(found)
    }

    @Test fun aBrokenLookupDoesntStopTheOthers() {
        val net = FakeNetwork(answers = mapOf("192.168.1.77" to (10L to true)), bonjour = "192.168.1.77", bonjourMs = 20, broken = setOf("192.168.1.20"))
        val (found, _) = timed { Reconnect.find(secure, net) }
        assertEquals("192.168.1.77", found?.host)
    }

    @Test fun plainPairingsAreLeftAlone() {
        val noSecurePort = secure.copy(tlsPort = 0)
        val noAuthority = secure.copy(ca = null)
        for (c in listOf(noSecurePort, noAuthority)) {
            val net = FakeNetwork(answers = mapOf("192.168.137.1" to (0L to true)), bonjour = "192.168.1.77", own = listOf("192.168.137.42"))
            val (found, _) = timed { Reconnect.find(c, net) }
            assertSame(c, found)
            assertEquals(emptyList<String>(), net.asked)
        }
    }

    @Test fun lookingSendsNoToken() {
        val nets = listOf(
            FakeNetwork(answers = mapOf("192.168.1.20" to (10L to true)), bonjourMs = null),
            FakeNetwork(bonjour = "192.168.1.77", bonjourMs = 10, answers = mapOf("192.168.1.77" to (10L to true))),
            FakeNetwork(own = listOf("192.168.137.42"), answers = mapOf("192.168.137.1" to (10L to true))),
            FakeNetwork(own = listOf("10.42.0.5")),
        )
        for (net in nets) {
            runBlocking { Reconnect.find(secure, net) }
            assertTrue(net.asked.isNotEmpty())
            for (line in net.asked) {
                assertFalse(line, line.contains(token))
                // Only the secure port is looked at, never the plain page.
                if (line.startsWith("open ")) assertTrue(line, line.endsWith(" 7071"))
            }
        }
    }

    private val tailnet = "100.101.102.103"
    private val onTailscale = secure.copy(tailnet = tailnet)

    @Test fun atHomeTheNetworkBeatsTailscale() {
        val net = FakeNetwork(answers = mapOf("192.168.1.20" to (50L to true), tailnet to (0L to true)), bonjourMs = null)
        val (found, _) = timed { Reconnect.find(onTailscale, net) }
        assertSame(onTailscale, found)
        assertFalse(net.asked.toString(), net.asked.any { tailnet in it })
    }

    @Test fun awayItsFoundAtItsTailscaleAddressAndKeepsItsHomeOne() {
        val net = FakeNetwork(answers = mapOf(tailnet to (10L to true)), bonjourMs = null)
        val (found, ms) = timed { Reconnect.find(onTailscale, net) }
        assertEquals(tailnet, found?.host)
        assertEquals("192.168.1.20", onTailscale.host)
        assertTrue("took $ms ms", ms in Reconnect.TAILNET_AFTER_MS..(Reconnect.TAILNET_AFTER_MS + 1000))
    }

    @Test fun withoutTailscaleNothingIsTriedThereOrSaid() {
        val net = FakeNetwork(bonjourMs = 0)
        assertNull(runBlocking { Reconnect.find(secure, net) })
        assertFalse(net.asked.toString(), net.asked.any { tailnet in it })
        assertFalse(Reconnect.nowhere(secure).contains("Tailscale"))
        assertTrue(Reconnect.nowhere(onTailscale).contains("Tailscale"))
    }

    @Test fun onlyTheTailscaleAddressIsCheckedByAnotherName() {
        assertEquals("localhost", Net.certificateName(tailnet, tailnet))
        assertEquals("192.168.1.20", Net.certificateName("192.168.1.20", tailnet))
        assertEquals(tailnet, Net.certificateName(tailnet, null))
    }

    @Test fun firstGivesTheFirstAnswerAndCancelsTheRest() {
        val cancelled = Collections.synchronizedList(mutableListOf<String>())
        fun slow(name: String, ms: Long, answer: String?): suspend () -> String? = {
            try {
                delay(ms)
            } catch (e: CancellationException) {
                cancelled += name
                throw e
            }
            answer
        }
        val answer = runBlocking { Reconnect.first(listOf(slow("a", 5_000, "a"), slow("b", 10, null), slow("c", 40, "c"), slow("d", 5_000, "d"))) }
        assertEquals("c", answer)
        assertEquals(setOf("a", "d"), cancelled.toSet())
        assertNull(runBlocking { Reconnect.first(emptyList<suspend () -> String?>()) })
        assertNull(runBlocking { Reconnect.first(listOf(slow("e", 5, null), slow("f", 10, null))) })
    }
}
