package dev.droidline.agent.link

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import dev.droidline.agent.Agent
import dev.droidline.agent.crypto.P256
import dev.droidline.agent.store.PairingRecord
import dev.droidline.agent.store.toStringList
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.io.IOException
import java.security.SecureRandom

sealed class LinkTarget {
    class Paired(val record: PairingRecord) : LinkTarget()
    class PairQr(val qr: QrPairing) : LinkTarget()
    class PairCode(val server: DiscoveredServer) : LinkTarget()
}

enum class Phase { IDLE, CONNECTING, CONNECTED, WAITING_CODE, OFFLINE, REJECTED, PAIR_FAILED }

data class LinkStatus(
    val phase: Phase = Phase.IDLE,
    val route: String? = null,
    val serverName: String? = null,
    val sas: String? = null,
    val detail: String? = null,
    val pairing: Boolean = false,
)

/** Keeps one link to the server alive: route selection, handshake, keepalive, reconnect (PROTOCOL 4). */
class LinkManager(
    private val context: Context,
    private val scope: CoroutineScope,
    private val onCommand: (JSONObject) -> Unit,
) {
    private val kick = Channel<Unit>(Channel.CONFLATED)
    private val random = SecureRandom()
    private var loopJob: Job? = null
    private var probeJob: Job? = null
    private var netCallback: ConnectivityManager.NetworkCallback? = null

    @Volatile private var current: SecureLink? = null
    @Volatile private var pairingTarget: LinkTarget? = null

    fun start() {
        if (loopJob != null) return
        loopJob = scope.launch(Dispatchers.IO) { loop() }
        registerNetworkCallback()
    }

    fun stop() {
        loopJob?.cancel()
        loopJob = null
        probeJob?.cancel()
        current?.close()
        netCallback?.let { runCatching { context.getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it) } }
        netCallback = null
    }

    /** Drops the current link and reconnects without waiting for the backoff. */
    fun reconnectNow() {
        current?.close()
        kick.trySend(Unit)
    }

    fun startPairing(target: LinkTarget) {
        pairingTarget = target
        Agent.pairFailure.value = null
        Agent.status.value = LinkStatus(Phase.CONNECTING, pairing = true)
        reconnectNow()
    }

    fun cancelPairing() {
        if (pairingTarget == null) return
        pairingTarget = null
        Agent.status.value = LinkStatus()
        reconnectNow()
    }

    private suspend fun loop() {
        var failures = 0
        while (scope.isActive) {
            val target = pairingTarget ?: Agent.pairing.load()?.let { LinkTarget.Paired(it) }
            if (target == null) {
                Agent.status.value = LinkStatus()
                kick.receive()
                continue
            }
            val result = runCatching { connectOnce(target) }.getOrElse { Outcome.Failed(it.message) }
            val waitMs = when (result) {
                is Outcome.SessionEnded -> { failures = 0; BACKOFF[0] }
                is Outcome.Failed -> {
                    val w = BACKOFF[minOf(failures, BACKOFF.lastIndex)]
                    failures++
                    if (pairingTarget == null) {
                        Agent.status.value = Agent.status.value.copy(phase = Phase.OFFLINE, detail = result.reason, sas = null)
                    }
                    w
                }
                is Outcome.Rejected -> {
                    failures++
                    Agent.status.value = LinkStatus(Phase.REJECTED, detail = result.status)
                    REJECTED_RETRY_MS
                }
                is Outcome.PairingFailed -> {
                    pairingTarget = null
                    Agent.pairFailure.value = result.reason
                    Agent.status.value = LinkStatus(Phase.PAIR_FAILED, detail = result.reason)
                    0L
                }
            }
            if (waitMs > 0) withTimeoutOrNull(waitMs) { kick.receive() }
        }
    }

    private sealed class Outcome {
        data object SessionEnded : Outcome()
        class Failed(val reason: String?) : Outcome()
        class Rejected(val status: String) : Outcome()
        class PairingFailed(val reason: String) : Outcome()
    }

    private suspend fun connectOnce(target: LinkTarget): Outcome {
        val pairing = target !is LinkTarget.Paired
        val prev = Agent.status.value
        Agent.status.value = LinkStatus(Phase.CONNECTING, serverName = prev.serverName, pairing = pairing)
        val deviceId = Agent.identity.deviceId
        val routes: List<Route>
        val serverId: String
        val serverPub: ByteArray
        when (target) {
            is LinkTarget.Paired -> {
                val r = target.record
                serverId = r.serverId
                serverPub = r.serverPub
                val found = Discovery.run(context, r.serverId).firstOrNull()
                routes = Routes.order(found?.let { HostPort(it.host, it.port) }, r.addresses, r.relayUrl, r.relayTicket, r.serverId, deviceId)
            }
            is LinkTarget.PairQr -> {
                val q = target.qr
                serverId = q.serverId
                serverPub = q.serverPub
                val found = Discovery.run(context, q.serverId).firstOrNull()
                routes = Routes.order(found?.let { HostPort(it.host, it.port) }, q.addresses, q.relayUrl, q.relayTicket, q.serverId, deviceId, q.relayExpiry)
            }
            is LinkTarget.PairCode -> {
                val s = target.server
                serverId = s.serverId
                serverPub = s.pub
                routes = listOf(Route(Route.LAN, "tcp://${if (s.host.contains(':')) "[${s.host}]" else s.host}:${s.port}"))
            }
        }
        if (routes.isEmpty()) return if (pairing) Outcome.PairingFailed("unreachable") else Outcome.Failed("no route")

        var lastError: String? = null
        for (route in routes) {
            val link = try {
                handshake(route, target, serverId, serverPub)
            } catch (e: HandshakeRejected) {
                when (e.status) {
                    "busy" -> { lastError = "busy"; continue }
                    else -> return if (pairing) Outcome.PairingFailed(e.status) else Outcome.Rejected(e.status)
                }
            } catch (e: Exception) {
                lastError = "${route.kind}: ${e.message ?: e.javaClass.simpleName}"
                continue
            }
            return runSession(link, target)
        }
        return if (pairing) Outcome.PairingFailed(if (lastError == "busy") "busy" else "unreachable") else Outcome.Failed(lastError)
    }

    private suspend fun handshake(route: Route, target: LinkTarget, serverId: String, serverPub: ByteArray): SecureLink {
        val transport = runInterruptible(Dispatchers.IO) { openTransport(route) }
        try {
            val watchdog = scope.launch { delay(HANDSHAKE_TIMEOUT_MS); transport.close() }
            try {
                return runInterruptible(Dispatchers.IO) { doHandshake(transport, route, target, serverId, serverPub) }
            } finally {
                watchdog.cancel()
            }
        } catch (e: Exception) {
            transport.close()
            throw e
        }
    }

    private fun openTransport(route: Route): LineTransport = when {
        route.url.startsWith("tcp://", true) -> {
            val hp = Routes.hostPort(route.url)
            SocketTransport.tcp(hp.host, hp.port, if (route.kind == Route.LAN) 4000 else 10000)
        }
        route.url.startsWith("tls://", true) -> {
            val hp = Routes.hostPort(route.url)
            val fp = tlsFingerprint() ?: throw IOException("no TLS fingerprint")
            SocketTransport.tls(hp.host, hp.port, fp, 10000)
        }
        else -> WsTransport.connect(route.url, route.bearer, 15000)
    }

    private fun tlsFingerprint(): String? = when (val t = pairingTarget) {
        is LinkTarget.PairQr -> t.qr.tlsFingerprint
        else -> Agent.pairing.load()?.tlsFingerprint
    }

    private fun doHandshake(t: LineTransport, route: Route, target: LinkTarget, serverId: String, serverPub: ByteArray): SecureLink {
        val identity = Agent.identity
        val mode = when (target) {
            is LinkTarget.Paired -> HsMode.AUTH
            is LinkTarget.PairQr -> HsMode.PAIR_QR
            is LinkTarget.PairCode -> HsMode.PAIR_CODE
        }
        val eph = P256.generate()
        val nonce = ByteArray(16).also(random::nextBytes)
        val tokenId = (target as? LinkTarget.PairQr)?.qr?.tokenId
        val token = (target as? LinkTarget.PairQr)?.qr?.token
        val line1 = Handshake.line1(mode, identity.deviceId, P256.rawPublic(eph.public), nonce, identity.publicRaw, tokenId)
        t.writeLine(line1)
        val line2 = t.readLine() ?: throw IOException("closed during handshake")
        val hello = Handshake.parseLine2(line2)
        if (hello.serverId != serverId) throw ProtocolException("answered by server ${hello.serverId}")
        val keys = Handshake.keys(line1, line2, eph, P256.publicFromRaw(hello.eph), identity.keyPair, P256.publicFromRaw(serverPub), token)
        return SecureLink(t, keys, route.kind, serverId)
    }

    private suspend fun runSession(link: SecureLink, target: LinkTarget): Outcome = coroutineScope {
        current = link
        val session = Agent.session
        val pairing = target !is LinkTarget.Paired
        var intervalMs = DEFAULT_PING_MS
        var welcomed = false
        var pump: Job? = null
        val startedAt = System.currentTimeMillis()
        val serverName = when (target) {
            is LinkTarget.Paired -> target.record.serverName
            is LinkTarget.PairQr -> target.qr.serverName
            is LinkTarget.PairCode -> target.server.name
        }
        Agent.status.value = when (target) {
            is LinkTarget.PairCode -> LinkStatus(Phase.WAITING_CODE, link.route, serverName, sas = link.keys.sas, pairing = true)
            else -> LinkStatus(Phase.CONNECTING, link.route, serverName, pairing = pairing)
        }

        val pinger = launch(Dispatchers.IO) {
            while (isActive && !link.closed) {
                delay(intervalMs)
                val silent = System.currentTimeMillis() - link.lastReceived
                // A pending code pairing may sit quietly until the user types the code on the PC.
                val waitingForCode = target is LinkTarget.PairCode && !welcomed
                if (waitingForCode && System.currentTimeMillis() - startedAt > PAIR_PENDING_MS) {
                    link.close()
                    break
                }
                if (!waitingForCode && silent > 3 * intervalMs) {
                    link.close()
                    break
                }
                runCatching { link.send(JSONObject().put("event", "ping").put("t", System.currentTimeMillis()).toString()) }
                    .onFailure { link.close() }
            }
        }
        val prober = if (link.route != Route.LAN) launch(Dispatchers.IO) { probeLan(link) } else null

        var pairFailure: String? = null
        try {
            runInterruptible(Dispatchers.IO) { link.send(Agent.hello(link.route).toString()) }
            while (true) {
                val line = runInterruptible(Dispatchers.IO) { link.receive() } ?: break
                val msg = runCatching { JSONObject(line) }.getOrNull() ?: continue
                if (msg.has("cmd")) {
                    onCommand(msg)
                    continue
                }
                when (msg.optString("event")) {
                    "paired" -> onPaired(target, msg)
                    "renamed" -> msg.optString("name").ifEmpty { null }?.let { n -> Agent.pairing.update { it.copy(deviceName = n) } }
                    "welcome" -> {
                        welcomed = true
                        val ack = msg.optLong("ack", 0)
                        msg.optLong("ping", 0).takeIf { it > 0 }?.let { intervalMs = it * 1000 }
                        if (pairingTarget != null) pairFailure = "no paired event before welcome"
                        if (pairFailure != null) break
                        session.onWelcome(ack)
                        val name = msg.optString("name").ifEmpty { serverName }
                        val deviceName = msg.optString("device_name").ifEmpty { null }
                        Agent.pairing.update { it.copy(serverName = name, deviceName = deviceName ?: it.deviceName) }
                        Agent.status.value = LinkStatus(Phase.CONNECTED, link.route, name)
                        pump?.cancel()
                        // A failed write ends this link; the reader then sees the close and the session ends normally.
                        pump = launch(Dispatchers.IO) { runCatching { session.pump(link, ack) }.onFailure { link.close() } }
                        Agent.onConnected()
                    }
                    "ack" -> session.ack(msg.optLong("n", 0))
                    "pong" -> if (msg.has("ack")) session.ack(msg.optLong("ack", 0))
                    "config" -> onConfig(msg)
                }
            }
        } catch (e: Exception) {
            if (!welcomed && pairing) pairFailure = e.message ?: e.javaClass.simpleName
        } finally {
            pinger.cancel()
            prober?.cancel()
            pump?.cancel()
            link.close()
            if (current === link) current = null
        }
        when {
            pairFailure != null -> Outcome.PairingFailed(pairFailure!!)
            pairing && !welcomed && pairingTarget != null ->
                Outcome.PairingFailed(if (target is LinkTarget.PairCode) "code_expired" else "closed")
            // Replaced by a newer connection (4000) or rate limited (4008): back off instead of reconnecting at once.
            link.closeCode == CLOSE_REPLACED || link.closeCode == CLOSE_RATE_LIMITED ->
                Outcome.Failed("relay closed the link (${link.closeCode})")
            welcomed -> Outcome.SessionEnded
            else -> Outcome.Failed("closed before welcome")
        }
    }

    private fun onPaired(target: LinkTarget, msg: JSONObject) {
        val deviceName = msg.optString("name").ifEmpty { null }
        val record = when (target) {
            is LinkTarget.PairQr -> target.qr.let {
                PairingRecord(it.serverId, it.serverName, it.serverPub, it.addresses, it.tlsFingerprint, it.relayUrl, null, deviceName)
            }
            is LinkTarget.PairCode -> target.server.let {
                val host = if (it.host.contains(':')) "[${it.host}]" else it.host
                PairingRecord(it.serverId, it.name, it.pub, listOf("tcp://$host:${it.port}"), null, null, null, deviceName)
            }
            is LinkTarget.Paired -> {
                Agent.pairing.update { it.copy(deviceName = deviceName ?: it.deviceName) }
                return
            }
        }
        val previous = Agent.pairing.load()
        if (previous?.serverId != record.serverId) Agent.session.resetForNewServer()
        Agent.pairing.save(record)
        pairingTarget = null
    }

    private fun onConfig(msg: JSONObject) {
        Agent.pairing.update { r ->
            val relay = msg.optJSONObject("relay")
            r.copy(
                addresses = if (msg.has("addresses")) msg.optJSONArray("addresses").toStringList() else r.addresses,
                tlsFingerprint = if (msg.has("tls_fp")) msg.optString("tls_fp").ifEmpty { null } else r.tlsFingerprint,
                relayUrl = if (msg.has("relay")) relay?.optString("url")?.ifEmpty { null } else r.relayUrl,
                relayTicket = if (msg.has("relay")) relay?.optString("ticket")?.ifEmpty { null } else r.relayTicket,
            )
        }
    }

    /** Off LAN, look for the PC every 60 s and switch to a LAN link when it answers; the session carries over. */
    private suspend fun probeLan(link: SecureLink) {
        val serverId = link.serverId
        while (!link.closed) {
            delay(LAN_PROBE_MS)
            if (Discovery.run(context, serverId).isNotEmpty()) {
                reconnectNow()
                return
            }
        }
    }

    private fun registerNetworkCallback() {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return
        var last: Network? = null
        var first = true
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                val changed = network != last
                last = network
                if (first) {
                    first = false
                    return
                }
                if (changed) reconnectNow()
            }

            override fun onLost(network: Network) {
                if (network == last) kick.trySend(Unit)
            }
        }
        runCatching { cm.registerDefaultNetworkCallback(cb) }.onSuccess { netCallback = cb }
    }

    companion object {
        private val BACKOFF = longArrayOf(1000, 2000, 4000, 8000, 16000, 30000)
        private const val DEFAULT_PING_MS = 25_000L
        private const val HANDSHAKE_TIMEOUT_MS = 15_000L
        private const val PAIR_PENDING_MS = 5 * 60_000L
        private const val LAN_PROBE_MS = 60_000L
        private const val REJECTED_RETRY_MS = 5 * 60_000L
        const val CLOSE_REPLACED = 4000
        const val CLOSE_RATE_LIMITED = 4008
    }
}
