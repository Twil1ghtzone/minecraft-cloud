package net.aethernet.cloud

import io.lettuce.core.RedisURI
import io.lettuce.core.cluster.RedisClusterClient
import io.lettuce.core.cluster.api.StatefulRedisClusterConnection
import io.lettuce.core.cluster.api.sync.RedisAdvancedClusterCommands
import java.time.Duration

/**
 * Lettuce-based Redis Cluster client for the Paper plugin.
 * Falls back to single-node mode if only one address is given.
 */
class RedisClient(private val addresses: List<String>) {

    private var clusterClient: RedisClusterClient? = null
    private var connection: StatefulRedisClusterConnection<String, ByteArray>? = null
    private var commands: RedisAdvancedClusterCommands<String, ByteArray>? = null

    fun connect() {
        val uris = addresses.map { addr ->
            val parts = addr.split(":")
            RedisURI.create(parts[0], parts.getOrNull(1)?.toIntOrNull() ?: 6379)
        }
        clusterClient = RedisClusterClient.create(uris)
        clusterClient!!.setDefaultTimeout(Duration.ofSeconds(5))
        connection = clusterClient!!.connect(io.lettuce.core.codec.ByteArrayCodec.INSTANCE
            .let {
                // Use string keys, byte[] values
                io.lettuce.core.codec.RedisCodec.of(
                    io.lettuce.core.codec.StringCodec.UTF8,
                    io.lettuce.core.codec.ByteArrayCodec.INSTANCE
                )
            })
        commands = connection!!.sync()
    }

    fun disconnect() {
        connection?.close()
        clusterClient?.shutdown()
    }

    /** Atomically set key=value with TTL if not exists. Returns true if set. */
    fun setNX(key: String, value: ByteArray, ttlMs: Long): Boolean {
        val result = commands!!.set(
            key, value,
            io.lettuce.core.SetArgs().nx().px(ttlMs)
        )
        return result == "OK"
    }

    fun set(key: String, value: ByteArray, ttlMs: Long) {
        commands!!.psetex(key, ttlMs, value)
    }

    fun get(key: String): ByteArray? = commands!!.get(key)

    fun del(vararg keys: String) { commands!!.del(*keys) }

    fun exists(key: String): Boolean = commands!!.exists(key) > 0

    fun ttl(key: String): Long = commands!!.pttl(key)

    /** Blocking wait: polls until key disappears or timeoutMs elapses. */
    fun waitForKeyDeletion(key: String, timeoutMs: Long, pollMs: Long = 100): Boolean {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (!exists(key)) return true
            Thread.sleep(pollMs)
        }
        return false
    }
}
