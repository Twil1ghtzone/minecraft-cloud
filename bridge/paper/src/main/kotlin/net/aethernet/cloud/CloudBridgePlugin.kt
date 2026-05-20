package net.aethernet.cloud

import org.bukkit.plugin.java.JavaPlugin

/**
 * AetherNet CloudBridge Plugin for Paper/Spigot/Purpur.
 *
 * Provides:
 *  1. Anti-exploit player profile sync (Module 6)
 *  2. Sign & Tablist API (Module 7)
 *  3. Graceful server shutdown hook (Module 8)
 */
class CloudBridgePlugin : JavaPlugin() {

    lateinit var redisClient: RedisClient
        private set
    private lateinit var playerQuitHandler: PlayerQuitHandler
    private lateinit var playerPreLoginHandler: PlayerPreLoginHandler

    override fun onEnable() {
        saveDefaultConfig()
        val cfg = config

        val redisAddrs = cfg.getStringList("redis.addresses")
            .ifEmpty { listOf("127.0.0.1:6379") }
        val daemonHost = cfg.getString("daemon.host", "127.0.0.1")!!
        val daemonPort = cfg.getInt("daemon.port", 7001)

        logger.info("[AetherNet] Connecting to Redis cluster: $redisAddrs")
        redisClient = RedisClient(redisAddrs)
        redisClient.connect()

        playerQuitHandler = PlayerQuitHandler(this)
        playerPreLoginHandler = PlayerPreLoginHandler(this)

        server.pluginManager.registerEvents(playerQuitHandler, this)
        server.pluginManager.registerEvents(playerPreLoginHandler, this)

        // Soft-depend: McMMO hook
        if (server.pluginManager.getPlugin("mcMMO") != null) {
            logger.info("[AetherNet] mcMMO detected — enabling stat sync.")
        }

        logger.info("[AetherNet] CloudBridge enabled. Redis: ${redisAddrs.first()}")
    }

    override fun onDisable() {
        redisClient.disconnect()
        logger.info("[AetherNet] CloudBridge disabled.")
    }
}
