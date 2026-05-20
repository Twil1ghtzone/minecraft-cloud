package net.aethernet.cloud

import org.bukkit.event.EventHandler
import org.bukkit.event.EventPriority
import org.bukkit.event.Listener
import org.bukkit.event.player.AsyncPlayerPreLoginEvent
import org.bukkit.event.player.PlayerJoinEvent
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.logging.Logger

/**
 * Anti-exploit pre-login stall handler.
 *
 * On AsyncPlayerPreLoginEvent (async thread):
 *  1. Check if 'lock:player:{UUID}' exists in Redis
 *  2. If lock present, stall login (sleep-poll) until lock disappears or timeout
 *  3. After lock cleared, fetch 'player:data:{UUID}' from Redis
 *  4. Store the fetched NBT data for injection on PlayerJoinEvent
 */
class PlayerPreLoginHandler(private val plugin: CloudBridgePlugin) : Listener {

    companion object {
        // Max time to wait for the quit lock to be released (ms)
        const val LOCK_WAIT_TIMEOUT_MS = 5_000L
        // Poll interval while waiting for lock release
        const val POLL_INTERVAL_MS = 100L
    }

    private val log: Logger get() = plugin.logger

    // Pending profile data indexed by UUID — set in pre-login, consumed in join
    private val pendingProfiles = ConcurrentHashMap<UUID, ByteArray>()

    @EventHandler(priority = EventPriority.LOW)
    fun onAsyncPreLogin(event: AsyncPlayerPreLoginEvent) {
        val uuid = event.uniqueId
        val lockKey = "lock:player:$uuid"
        val dataKey = "player:data:$uuid"
        val redis = plugin.redisClient

        // 1. Check lock
        if (redis.exists(lockKey)) {
            log.info("[AetherNet] Login stalled for $uuid — waiting for quit lock release...")
            // 2. Wait for lock to be released
            val released = redis.waitForKeyDeletion(lockKey, LOCK_WAIT_TIMEOUT_MS, POLL_INTERVAL_MS)
            if (!released) {
                log.warning("[AetherNet] Lock wait timeout for $uuid — proceeding anyway (data may be stale)")
            } else {
                log.fine("[AetherNet] Lock released for $uuid, proceeding with login")
            }
        }

        // 3. Fetch profile from Redis
        val profileData = redis.get(dataKey)
        if (profileData != null && profileData.isNotEmpty()) {
            pendingProfiles[uuid] = profileData
            log.info("[AetherNet] Loaded profile from Redis for $uuid (${profileData.size} bytes)")
        } else {
            log.fine("[AetherNet] No Redis profile for $uuid — using default/disk profile")
        }
    }

    @EventHandler(priority = EventPriority.HIGHEST)
    fun onPlayerJoin(event: PlayerJoinEvent) {
        val player = event.player
        val profileData = pendingProfiles.remove(player.uniqueId) ?: return

        // 4. Inject profile on the main thread (next tick for safety)
        plugin.server.scheduler.runTask(plugin) {
            try {
                NbtSerializer.deserialize(player, profileData)
                log.info("[AetherNet] Profile injected for ${player.name}")
            } catch (e: Exception) {
                log.severe("[AetherNet] Failed to inject profile for ${player.name}: ${e.message}")
            }
        }
    }
}
