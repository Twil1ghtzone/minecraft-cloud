package net.aethernet.bridge.common

import redis.clients.jedis.JedisPool
import redis.clients.jedis.params.SetParams
import java.time.Duration
import java.util.UUID

/**
 * Redis key conventions (shared between Paper and Velocity bridge):
 *
 *   lock:player:{UUID}     SETNX, ttl 30s, value = source server id
 *   player:data:{UUID}     gzipped NBT, ttl 5m (refreshed on each transfer)
 *   player:route:{UUID}    last server id, no ttl
 *   server:players:{SID}   SET of UUIDs currently online on that server
 *   sign:state             pub/sub channel — sign + tablist updates
 */
class RedisStateStore(host: String, port: Int, password: String? = null) {

    private val pool: JedisPool = run {
        val cfg = redis.clients.jedis.JedisPoolConfig().apply {
            maxTotal = 32
            maxIdle = 8
            minIdle = 2
        }
        if (password.isNullOrBlank()) {
            JedisPool(cfg, host, port)
        } else {
            JedisPool(cfg, host, port, 2000, password)
        }
    }

    /**
     * Acquire the transfer lock for [uuid].
     *
     * Returns `true` if we successfully claimed the lock. The TTL guards
     * against a crashed sender leaving the player stuck forever.
     */
    fun acquireLock(uuid: UUID, sourceServerId: String, ttl: Duration = Duration.ofSeconds(30)): Boolean {
        pool.resource.use { j ->
            val params = SetParams().nx().ex(ttl.seconds)
            return j.set("lock:player:$uuid", sourceServerId, params) == "OK"
        }
    }

    fun releaseLock(uuid: UUID) {
        pool.resource.use { j ->
            j.del("lock:player:$uuid")
        }
    }

    fun isLocked(uuid: UUID): Boolean {
        pool.resource.use { j ->
            return j.exists("lock:player:$uuid")
        }
    }

    fun storePlayerData(uuid: UUID, nbt: ByteArray, ttl: Duration = Duration.ofMinutes(5)) {
        pool.resource.use { j ->
            val key = "player:data:$uuid"
            j.set(key.toByteArray(Charsets.UTF_8), nbt)
            j.expire(key, ttl.seconds)
        }
    }

    fun loadPlayerData(uuid: UUID): ByteArray? {
        pool.resource.use { j ->
            return j.get("player:data:$uuid".toByteArray(Charsets.UTF_8))
        }
    }

    fun setRoute(uuid: UUID, serverId: String) {
        pool.resource.use { j -> j.set("player:route:$uuid", serverId) }
    }

    fun getRoute(uuid: UUID): String? {
        pool.resource.use { j -> return j.get("player:route:$uuid") }
    }

    fun trackOnline(uuid: UUID, serverId: String, online: Boolean) {
        pool.resource.use { j ->
            val key = "server:players:$serverId"
            if (online) j.sadd(key, uuid.toString()) else j.srem(key, uuid.toString())
        }
    }

    fun close() = pool.close()
}
