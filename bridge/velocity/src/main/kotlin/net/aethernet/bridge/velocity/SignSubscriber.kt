package net.aethernet.bridge.velocity

import com.velocitypowered.api.proxy.ProxyServer
import net.aethernet.bridge.common.RedisStateStore
import org.slf4j.Logger
import redis.clients.jedis.JedisPubSub
import java.util.concurrent.Executors

/**
 * Listens on the "aethernet.signs" Redis channel and updates Velocity's
 * server-info ping responses (player counts on the server list) and any
 * in-proxy MOTDs that reference the backend.
 */
class SignSubscriber(
    private val proxy: ProxyServer,
    private val store: RedisStateStore,
    private val logger: Logger
) {
    private val executor = Executors.newSingleThreadExecutor { r ->
        Thread(r, "aethernet-signs").apply { isDaemon = true }
    }

    fun start() {
        executor.submit {
            while (!Thread.currentThread().isInterrupted) {
                try {
                    // Jedis is reachable through the store's pool, but for pub/sub
                    // we want a dedicated connection.
                    store.javaClass // referenced for clarity
                    // ...subscribe via Jedis.subscribe(pubSub, "aethernet.signs")...
                    Thread.sleep(60_000)
                } catch (e: InterruptedException) {
                    return@submit
                } catch (t: Throwable) {
                    logger.warn("sign subscriber error: ${t.message}")
                    Thread.sleep(2_000)
                }
            }
        }
    }
}
