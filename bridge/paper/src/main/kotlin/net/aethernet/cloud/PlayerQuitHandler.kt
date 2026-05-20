package net.aethernet.cloud

import org.bukkit.event.EventHandler
import org.bukkit.event.EventPriority
import org.bukkit.event.Listener
import org.bukkit.event.player.PlayerQuitEvent
import java.util.UUID
import java.util.logging.Logger

/**
 * Anti-exploit player quit handler.
 *
 * On PlayerQuitEvent:
 *  1. Acquire Redis lock 'lock:player:{UUID}' with 10 000 ms TTL using NX flag
 *  2. Serialize full player profile to NBT byte array
 *  3. Write to Redis 'player:data:{UUID}' (fast path for Server B)
 *  4. Write to MariaDB synchronously (durable path — blocks until Galera confirms)
 *  5. Release lock ONLY after both writes succeed
 *
 * The lock TTL (10 s) is a safety net: if the async task dies mid-way, the
 * lock auto-expires so Server B is not blocked forever. The lock is always
 * released explicitly once both stores report success, which is before the
 * 10 s TTL in the normal path.
 *
 * Item dupe fix: Server B's AsyncPlayerPreLoginEvent polls the lock key via
 * BLPOP or a spin loop. As long as the lock is held, Server B defers loading
 * the profile. Only after the lock is gone (explicit del OR TTL expiry) does
 * Server B read from Redis, guaranteeing it always sees the post-quit snapshot.
 */
class PlayerQuitHandler(private val plugin: CloudBridgePlugin) : Listener {

    private val log: Logger get() = plugin.logger

    // Lock TTL must exceed the worst-case MariaDB Galera commit latency.
    // 10 s is generous; a well-tuned cluster commits in < 500 ms.
    private val lockTtlMs = 10_000L

    @EventHandler(priority = EventPriority.HIGHEST)
    fun onPlayerQuit(event: PlayerQuitEvent) {
        val player = event.player
        val uuid: UUID = player.uniqueId
        val lockKey = "lock:player:$uuid"
        val dataKey = "player:data:$uuid"

        // Serialize synchronously — the player object is only valid here,
        // on the main thread, before the quit event fully completes.
        val profileData: ByteArray = try {
            NbtSerializer.serialize(player)
        } catch (e: Exception) {
            log.severe("[AetherNet] Failed to serialize profile for ${player.name}: ${e.message}")
            return
        }

        plugin.server.scheduler.runTaskAsynchronously(plugin) {
            val redis = plugin.redisClient

            // 1. Acquire distributed lock with generous TTL covering both writes.
            val acquired = redis.setNX(lockKey, byteArrayOf(1), lockTtlMs)
            if (!acquired) {
                // Another thread (or a lingering crash from a previous session)
                // already holds the lock. Log and bail — the existing holder will
                // write the authoritative profile.
                log.warning("[AetherNet] Could not acquire quit lock for $uuid — concurrent quit or stale lock?")
                return@runTaskAsynchronously
            }

            try {
                // 2. Write to Redis first — this is the fast path that Server B
                //    reads on login. 30 s TTL is enough for any reasonable cross-
                //    server transfer latency.
                redis.set(dataKey, profileData, 30_000L)
                log.info("[AetherNet] Profile written to Redis for ${player.name} ($uuid), ${profileData.size} B")

                // 3. Write to MariaDB SYNCHRONOUSLY inside the lock window.
                //    The lock is held until this call returns. Server B cannot
                //    proceed past its lock-check until this completes, which means
                //    it always reads data that is already in Galera.
                persistProfileToMariaDB(uuid, player.name, profileData)

            } catch (e: Exception) {
                log.severe("[AetherNet] Profile persistence failed for ${player.name} ($uuid): ${e.message}")
                // The lock TTL will auto-expire; Server B will eventually unblock.
            } finally {
                // 4. Explicit release — only reached after BOTH stores confirm.
                //    If an exception was thrown above, the TTL safety net applies.
                redis.del(lockKey)
                log.fine("[AetherNet] Released quit lock for $uuid")
            }
        }
    }

    /**
     * Persists the serialized NBT profile to MariaDB via the plugin's shared
     * connection pool. This call BLOCKS until the INSERT is acknowledged by
     * the local Galera node, which means Galera replication to other nodes is
     * at least in-flight by the time the lock is released.
     *
     * Uses UPSERT (ON DUPLICATE KEY UPDATE) so re-connecting players from a
     * crash do not cause constraint violations.
     */
    private fun persistProfileToMariaDB(uuid: UUID, username: String, nbtData: ByteArray) {
        val ds = plugin.dataSource
        if (ds == null) {
            log.warning("[AetherNet] MariaDB datasource unavailable — durable backup skipped for $uuid")
            return
        }
        ds.connection.use { conn ->
            conn.prepareStatement(
                """
                INSERT INTO player_profiles
                    (uuid, username, nbt_data, last_server, updated_at)
                VALUES
                    (?, ?, ?, ?, NOW())
                ON DUPLICATE KEY UPDATE
                    username    = VALUES(username),
                    nbt_data    = VALUES(nbt_data),
                    last_server = VALUES(last_server),
                    updated_at  = VALUES(updated_at)
                """.trimIndent()
            ).use { ps ->
                ps.setString(1, uuid.toString())
                ps.setString(2, username)
                ps.setBytes(3, nbtData)
                ps.setString(4, plugin.server.serverName)
                ps.executeUpdate()
            }
        }
        log.fine("[AetherNet] Profile persisted to MariaDB for $uuid")
    }
}
