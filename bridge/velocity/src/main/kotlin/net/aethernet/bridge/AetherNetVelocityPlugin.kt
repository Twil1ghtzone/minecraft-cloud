package net.aethernet.bridge

import com.velocitypowered.api.event.Subscribe
import com.velocitypowered.api.event.proxy.ProxyInitializeEvent
import com.velocitypowered.api.event.proxy.ProxyShutdownEvent
import com.velocitypowered.api.plugin.Plugin
import com.velocitypowered.api.plugin.annotation.DataDirectory
import com.velocitypowered.api.proxy.ProxyServer
import com.velocitypowered.api.proxy.server.ServerInfo
import kotlinx.coroutines.*
import java.net.InetSocketAddress
import java.nio.file.Path
import java.util.logging.Logger
import javax.inject.Inject

@Plugin(
    id = "aethernet-bridge",
    name = "AetherNet Bridge",
    version = "0.1.0",
    description = "AetherNet Velocity proxy bridge — hot server routing via gRPC",
    authors = ["AetherNet"]
)
class AetherNetVelocityPlugin @Inject constructor(
    private val proxy: ProxyServer,
    private val logger: Logger,
    @DataDirectory private val dataDir: Path,
) {
    private lateinit var grpcClient: GrpcStreamClient
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    @Subscribe
    fun onInit(event: ProxyInitializeEvent) {
        val config = loadConfig(dataDir)
        logger.info("[AetherNet] Connecting to daemon at ${config.daemonHost}:${config.daemonPort}")

        grpcClient = GrpcStreamClient(
            daemonHost   = config.daemonHost,
            daemonPort   = config.daemonPort,
            proxyId      = "velocity-" + proxy.boundAddress.port,
            proxyType    = "velocity",
            groupFilter  = config.groupFilter,
        )
        grpcClient.start()

        scope.launch {
            grpcClient.eventFlow().collect { event ->
                handleServerEvent(event)
            }
        }
        logger.info("[AetherNet] Bridge initialized successfully.")
    }

    @Subscribe
    fun onShutdown(event: ProxyShutdownEvent) {
        scope.cancel()
        grpcClient.shutdown()
        logger.info("[AetherNet] Bridge shut down.")
    }

    /**
     * Handles a ServerEvent from the AetherNet cluster.
     * READY  -> register/update server in Velocity
     * STOPPED/CRASHED -> unregister server from Velocity
     */
    private fun handleServerEvent(event: ServerEvent) {
        when (event.eventType) {
            ServerEvent.EventType.READY -> {
                val addr = InetSocketAddress(event.hostIp, event.hostPort)
                val info = ServerInfo(event.serverName, addr)
                proxy.registerServer(info)
                logger.info("[AetherNet] Registered server: ${event.serverName} at ${event.hostIp}:${event.hostPort}")
            }
            ServerEvent.EventType.STOPPED, ServerEvent.EventType.CRASHED -> {
                proxy.getServer(event.serverName).ifPresent {
                    proxy.unregisterServer(it.serverInfo)
                    logger.info("[AetherNet] Unregistered server: ${event.serverName} (${event.eventType})")
                }
            }
            else -> { /* STARTING, STOPPING — no routing change needed */ }
        }
    }

    private fun loadConfig(dataDir: Path): BridgeConfig {
        // Load from aethernet-bridge.conf in dataDir, or use defaults
        val confFile = dataDir.resolve("aethernet-bridge.conf").toFile()
        if (!confFile.exists()) {
            confFile.parentFile.mkdirs()
            confFile.writeText("""
                # AetherNet Bridge Configuration
                daemon_host=127.0.0.1
                daemon_port=7001
                group_filter=
            """.trimIndent())
        }
        val props = java.util.Properties()
        confFile.inputStream().use { props.load(it) }
        return BridgeConfig(
            daemonHost  = props.getProperty("daemon_host", "127.0.0.1"),
            daemonPort  = props.getProperty("daemon_port", "7001").toInt(),
            groupFilter = props.getProperty("group_filter", "").split(",").filter { it.isNotBlank() },
        )
    }

    data class BridgeConfig(
        val daemonHost: String,
        val daemonPort: Int,
        val groupFilter: List<String>,
    )
}
