package app.ferry

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiNetworkSpecifier
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.asExecutor
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.NetworkInterface
import java.net.Socket
import kotlin.coroutines.resume

// Finding a paired computer again, and joining a computer's own Wi-Fi.
//
// On a network in common, the computer is at its saved address, or Bonjour (_ferry._tcp) says where
// it went. With no router in common, a Windows or Linux computer can make a Wi-Fi network of its own
// (Windows' mobile hotspot, or NetworkManager's on Linux), and is that network's gateway. Looking only
// ever opens a TCP connection and closes it: nothing is sent until the computer's own pinned client
// has checked it's that computer.

/** The ways of looking for a computer, apart so tests can stand in for the network. */
interface Probes {
    /** Whether a TCP connection to [host]:[port] opens within [timeoutMs]. Nothing is sent over it. */
    suspend fun opens(host: String, port: Int, timeoutMs: Long): Boolean

    /** The address Bonjour gives for the computer with this [id], or null after [timeoutMs]. */
    suspend fun bonjour(id: String, timeoutMs: Long): String?

    /** This phone's own IPv4 addresses. */
    fun ownAddresses(): List<String>

    companion object {
        /** The real network, over a computer's own Wi-Fi when this phone joined it for that computer. */
        fun of(context: Context, c: FerryComputer): Probes = object : Probes {
            private val network = DirectWifi.network(c.id)
            override suspend fun opens(host: String, port: Int, timeoutMs: Long) = Nearby.opens(host, port, timeoutMs, network)
            override suspend fun bonjour(id: String, timeoutMs: Long) = Discovery(context).find(id, timeoutMs)?.host
            override fun ownAddresses() = Nearby.ownAddresses()
        }
    }
}

/** Where a paired computer is now. */
object Reconnect {
    const val OPEN_MS = 2500L
    const val BONJOUR_MS = 4000L
    /** Tailscale can take a few seconds to wake its connection. */
    const val TAILNET_MS = 6000L
    /** Tailscale is tried this much later than the rest, so at home the faster network in common wins. */
    const val TAILNET_AFTER_MS = 1500L

    /**
     * The computer as it can be reached now: at its saved address, where Bonjour heard it (a computer
     * that got a new address), as the gateway of its own Wi-Fi this phone is on, or at its Tailscale address
     * (away from home; the address saved for it stays its home one). Null if it's nowhere. All of them are
     * tried at once; the first that answers wins and the others stop.
     *
     * Only a computer paired securely is looked for: everything then goes to it over TLS pinned to its
     * own authority, so wherever a lookup points, this phone's token only reaches that computer. Any
     * other is given back as it is.
     *
     * Bonjour's own answer is the address it resolved, not the name in the TXT record: the computer's
     * certificate covers its addresses, but a Windows or Linux computer announces a name of Ferry's own
     * that the certificate doesn't.
     */
    suspend fun find(c: FerryComputer, probes: Probes): FerryComputer? {
        if (!c.secure) return c
        val port = c.tlsPort
        val tries = mutableListOf<suspend () -> FerryComputer?>()
        tries += { if (probes.opens(c.host, port, OPEN_MS)) c else null }
        for (gateway in Nearby.gateways(probes.ownAddresses())) {
            if (gateway != c.host) tries += { if (probes.opens(gateway, port, OPEN_MS)) c.copy(host = gateway) else null }
        }
        tries += {
            val host = probes.bonjour(c.id, BONJOUR_MS)
            if (host != null && host != c.host && probes.opens(host, port, OPEN_MS)) c.copy(host = host) else null
        }
        c.tailnet?.takeIf { it != c.host }?.let { tailnet ->
            tries += {
                delay(TAILNET_AFTER_MS)
                if (probes.opens(tailnet, port, TAILNET_MS)) c.copy(host = tailnet) else null
            }
        }
        return first(tries)
    }

    /** What to say when a computer is nowhere to be found. */
    fun nowhere(c: FerryComputer) = "Can't find ${c.name}. Is Ferry running there, and is this phone on the same Wi-Fi?" +
        if (c.tailnet != null) " Away from home? Turn on Tailscale on this phone, and check ${c.name} is awake." else ""

    /** Runs [tries] at once and gives the first answer that isn't null, cancelling the rest as soon as there is one. */
    suspend fun <T : Any> first(tries: List<suspend () -> T?>): T? = coroutineScope {
        val answers = Channel<T?>(tries.size)
        val jobs = tries.map { t ->
            launch {
                val answer = try {
                    t()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    null
                }
                answers.send(answer)
            }
        }
        var found: T? = null
        for (i in tries.indices) {
            found = answers.receive()
            if (found != null) break
        }
        jobs.forEach { it.cancel() }
        found
    }
}

object Nearby {
    /**
     * When this phone is on a computer's own Wi-Fi (Windows' 192.168.137.x, or 10.42.0.x from
     * NetworkManager on Linux), the computer is that network's gateway.
     */
    fun gateways(own: List<String>): List<String> =
        own.filter { it.startsWith("192.168.137.") || it.startsWith("10.42.0.") }
            .map { it.substringBeforeLast('.') + ".1" }
            .filter { it !in own }
            .distinct()

    /** This phone's own IPv4 addresses. */
    fun ownAddresses(): List<String> = runCatching {
        NetworkInterface.getNetworkInterfaces().toList().filter { it.isUp && !it.isLoopback }
            .flatMap { it.inetAddresses.toList() }.filterIsInstance<Inet4Address>().mapNotNull { it.hostAddress }
    }.getOrDefault(emptyList())

    /**
     * Whether a TCP connection to [host]:[port] opens within [timeoutMs], over [network] when there is
     * one. It says nothing about who answers, and nothing is sent: the connection is closed as soon as
     * it opens. Cancelling closes it at once, even mid-connect.
     */
    suspend fun opens(host: String, port: Int, timeoutMs: Long, network: Network? = null): Boolean {
        val socket = network?.socketFactory?.createSocket() ?: Socket()
        try {
            return withTimeoutOrNull(timeoutMs) {
                suspendCancellableCoroutine { done ->
                    done.invokeOnCancellation { runCatching { socket.close() } }
                    Dispatchers.IO.asExecutor().execute {
                        val ok = try {
                            val address = network?.getByName(host) ?: InetAddress.getByName(host)
                            socket.connect(InetSocketAddress(address, port), timeoutMs.toInt())
                            true
                        } catch (e: Exception) {
                            false
                        }
                        done.resume(ok)
                    }
                }
            } ?: false
        } finally {
            runCatching { socket.close() }
        }
    }
}

/**
 * A computer's own Wi-Fi, joined by its name and password (from its QR code). Android asks first
 * ("Connect?"), and keeps this phone on it for as long as Ferry holds the request: while that computer
 * is the one in use. That Wi-Fi has no internet, so only Ferry's requests to that computer go over it.
 */
object DirectWifi {
    /** The key a Wi-Fi joined while pairing is held under, until the computer's id is known. */
    const val PAIRING = "pairing"

    private class Held(val callback: ConnectivityManager.NetworkCallback) {
        @Volatile var network: Network? = null
    }

    private val held = mutableMapOf<String, Held>()

    /** The network a computer's own Wi-Fi gave, while this phone is on it for that computer. */
    fun network(id: String): Network? = synchronized(held) { held[id]?.network }

    /**
     * Asks Android to join [ssid] for the computer [id], and waits for the answer: the network once it's
     * joined, or null if the person said no or it can't be found.
     */
    suspend fun join(context: Context, id: String, ssid: String, password: String?): Network? {
        release(context, id)
        val specifier = runCatching {
            WifiNetworkSpecifier.Builder().setSsid(ssid).apply { if (!password.isNullOrEmpty()) setWpa2Passphrase(password) }.build()
        }.getOrNull() ?: return null
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .setNetworkSpecifier(specifier)
            .build()
        val answer = CompletableDeferred<Network?>()
        lateinit var entry: Held
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                entry.network = network
                answer.complete(network)
            }

            override fun onLost(network: Network) {
                if (entry.network == network) entry.network = null
            }

            override fun onUnavailable() {
                answer.complete(null)
            }
        }
        entry = Held(callback)
        synchronized(held) { held[id] = entry }
        try {
            context.getSystemService(ConnectivityManager::class.java).requestNetwork(request, callback)
        } catch (e: RuntimeException) {
            synchronized(held) { held.remove(id) }
            return null
        }
        val network = try {
            answer.await()
        } catch (e: CancellationException) {
            release(context, id)
            throw e
        }
        if (network == null) release(context, id)
        return network
    }

    /** Hands a Wi-Fi joined while pairing ([old]) over to the computer's id ([new]). */
    fun rename(context: Context, old: String, new: String) {
        val replaced = synchronized(held) {
            val entry = held.remove(old) ?: return
            held.put(new, entry)
        }
        replaced?.let { unregister(context, it) }
    }

    /** Lets go of a computer's Wi-Fi: Android goes back to the network it was on. */
    fun release(context: Context, id: String) {
        synchronized(held) { held.remove(id) }?.let { unregister(context, it) }
    }

    /** Lets go of every computer's Wi-Fi but [keep]'s. */
    fun releaseAllBut(context: Context, keep: String?) {
        val others = synchronized(held) { held.keys.filter { it != keep } }
        others.forEach { release(context, it) }
    }

    private fun unregister(context: Context, entry: Held) {
        runCatching { context.getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(entry.callback) }
    }
}
