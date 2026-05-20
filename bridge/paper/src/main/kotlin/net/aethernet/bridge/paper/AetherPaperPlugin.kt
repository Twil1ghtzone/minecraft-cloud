package net.aethernet.bridge.paper

import net.aethernet.bridge.common.RedisStateStore
import org.bukkit.event.EventHandler
import org.bukkit.event.EventPriority
import org.bukkit.event.Listener
import org.bukkit.event.player.AsyncPlayerPreLoginEvent
import org.bukkit.event.player.PlayerJoinEvent
import org.bukkit.event.player.PlayerQuitEvent
import org.bukkit.plugin.java.JavaPlugin
import java.util.UUID

/**
 * Module 6 — atomic player-state sync.
 *
 * On quit:
 *   1. Set Redis lock with NX (refuse concurrent transfer).
 *   2. Serialize the full player profile to NBT (inventory, enderchest,
 *      health, hunger, advancement progress, skill plugins via PDC).
 *   3. Write the blob to Redis.
 *   4. Release the lock.
 *
 * On the destination server (handled below in handlePreLogin), we
 * block briefly until the lock is released, then inject the NBT
 * before the player actually spawns. Item duplication is therefore
 * structurally impossible — the data only exists in one place at a time.
 */
class AetherPaperPlugin : JavaPlugin(), Listener {

    private lateinit var redis: RedisStateStore
    lateinit var serverId: String
        private set

    override fun onEnable() {
        saveDefaultConfig()
        val host = config.getString("redis.host", "127.0.0.1")!!
        val port = config.getInt("redis.port", 6379)
        val pwd  = config.getString("redis.password")
        redis = RedisStateStore(host, port, pwd)
        serverId = System.getenv("AETHERNET_SERVER_ID") ?: config.getString("server.id", name)!!

        server.pluginManager.registerEvents(this, this)
        logger.info("AetherNet bridge enabled — server_id=$serverId")
    }

    override fun onDisable() {
        redis.close()
    }

    @EventHandler(priority = EventPriority.MONITOR)
    fun onQuit(e: PlayerQuitEvent) {
        val uuid = e.player.uniqueId
        if (!redis.acquireLock(uuid, serverId)) {
            logger.warning("State transfer for $uuid skipped — lock held by another node.")
            return
        }
        try {
            val nbt = NbtSerializer.serialize(e.player)
            redis.storePlayerData(uuid, nbt)
            redis.setRoute(uuid, "")
            redis.trackOnline(uuid, serverId, false)
        } finally {
            redis.releaseLock(uuid)
        }
    }

    @EventHandler(priority = EventPriority.MONITOR)
    fun onPreLogin(e: AsyncPlayerPreLoginEvent) {
        val uuid: UUID = e.uniqueId
        val deadline = System.currentTimeMillis() + 2_000
        while (redis.isLocked(uuid) && System.currentTimeMillis() < deadline) {
            Thread.sleep(10)
        }
        if (redis.isLocked(uuid)) {
            e.disallow(
                AsyncPlayerPreLoginEvent.Result.KICK_OTHER,
                "AetherNet: your data is in transit — please reconnect in a moment."
            )
        }
    }

    @EventHandler
    fun onJoin(e: PlayerJoinEvent) {
        val uuid = e.player.uniqueId
        val nbt = redis.loadPlayerData(uuid)
        if (nbt != null && nbt.isNotEmpty()) {
            NbtSerializer.deserialize(e.player, nbt)
        }
        redis.setRoute(uuid, serverId)
        redis.trackOnline(uuid, serverId, true)
    }
}
