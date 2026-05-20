package net.aethernet.bridge.velocity

import com.google.inject.Inject
import com.velocitypowered.api.event.Subscribe
import com.velocitypowered.api.event.connection.DisconnectEvent
import com.velocitypowered.api.event.player.PlayerChooseInitialServerEvent
import com.velocitypowered.api.event.player.ServerConnectedEvent
import com.velocitypowered.api.event.proxy.ProxyInitializeEvent
import com.velocitypowered.api.plugin.Plugin
import com.velocitypowered.api.plugin.annotation.DataDirectory
import com.velocitypowered.api.proxy.ProxyServer
import com.velocitypowered.api.proxy.server.RegisteredServer
import com.velocitypowered.api.proxy.server.ServerInfo
import net.aethernet.bridge.common.RedisStateStore
import org.slf4j.Logger
import java.net.InetSocketAddress
import java.nio.file.Path

/**
 * Module 4 — dynamic Velocity routing.
 *
 * Subscribes to the daemon's VelocityService gRPC stream. Every backend
 * lifecycle event (READY / GONE / PLAYERS) is translated into a Velocity
 * server registration mutation. Live, no config reloads, no disconnects.
 *
 * In addition we sit on:
 *   - DisconnectEvent : free the player's Redis online flag.
 *   - PlayerChooseInitialServerEvent : pick the least-loaded server in the
 *     "lobby" group as a fallback target.
 */
@Plugin(
    id = "aethernet-bridge",
    name = "AetherNet Bridge (Velocity)",
    version = "0.1.0",
    authors = ["AetherNet"]
)
class AetherVelocityPlugin @Inject constructor(
    private val proxy: ProxyServer,
    private val logger: Logger,
    @DataDirectory private val dataDir: Path
) {

    private lateinit var control: DaemonControlClient
    private lateinit var redis: RedisStateStore

    @Subscribe
    fun onInit(event: ProxyInitializeEvent) {
        val cfg = AetherConfig.loadOrCreate(dataDir.resolve("config.toml"))
        redis = RedisStateStore(cfg.redisHost, cfg.redisPort, cfg.redisPassword)
        control = DaemonControlClient(cfg.daemonAddress, cfg.velocityId, logger)
        control.start { event ->
            when (event) {
                is BackendEvent.Ready    -> registerBackend(event)
                is BackendEvent.Gone     -> deregisterBackend(event)
                is BackendEvent.Players  -> Unit // tablist updates flow via signs
                is BackendEvent.GroupRefresh -> refreshGroup(event)
            }
        }
        logger.info("AetherNet bridge online — proxying live ingress from ${cfg.daemonAddress}")
    }

    private fun registerBackend(ev: BackendEvent.Ready) {
        val info = ServerInfo(ev.serverId, InetSocketAddress(ev.host, ev.port))
        // Replace any prior registration atomically.
        proxy.getServer(ev.serverId).ifPresent { proxy.unregisterServer(it.serverInfo) }
        proxy.registerServer(info)
        logger.info("backend READY: id=${ev.serverId} ${ev.host}:${ev.port} group=${ev.groupId}")
    }

    private fun deregisterBackend(ev: BackendEvent.Gone) {
        proxy.getServer(ev.serverId).ifPresent {
            proxy.unregisterServer(it.serverInfo)
            logger.info("backend GONE: id=${ev.serverId} reason=${ev.reason}")
        }
    }

    private fun refreshGroup(ev: BackendEvent.GroupRefresh) {
        // Currently a no-op; group membership is implicit in the per-server
        // registration. The hook exists so future routing strategies
        // (round-robin within a group, fewest-players, etc.) can be added.
    }

    @Subscribe
    fun onChooseInitial(event: PlayerChooseInitialServerEvent) {
        val lobby = leastLoaded("lobby")
        if (lobby != null) event.setInitialServer(lobby)
    }

    @Subscribe
    fun onConnected(event: ServerConnectedEvent) {
        redis.setRoute(event.player.uniqueId, event.server.serverInfo.name)
    }

    @Subscribe
    fun onDisconnect(event: DisconnectEvent) {
        // The Paper plugin already flips online flags off; we additionally
        // drop the route hint so reconnects don't try to pin to the old node.
        redis.setRoute(event.player.uniqueId, "")
    }

    private fun leastLoaded(groupId: String): RegisteredServer? {
        return proxy.allServers
            .filter { it.serverInfo.name.startsWith("$groupId-") || tagOf(it) == groupId }
            .minByOrNull { it.playersConnected.size }
    }

    private fun tagOf(s: RegisteredServer): String? {
        // We could read a Redis hash per backend to know its group id; for
        // the skeleton we encode group_id in the server name prefix.
        val n = s.serverInfo.name
        val dash = n.indexOf('-')
        return if (dash > 0) n.substring(0, dash) else null
    }
}
