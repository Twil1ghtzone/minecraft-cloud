package net.aethernet.bridge.velocity

import org.slf4j.Logger
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Thin wrapper around the generated gRPC stub. Once `make proto` produces
 * the Kotlin bindings, this class delegates to
 *
 *   net.aethernet.bridge.proto.VelocityServiceGrpc.newStub(channel)
 *
 * For the skeleton we expose the same shape with a hand-rolled control
 * channel: connect, subscribe to the stream, decode events, hand them to
 * the consumer. The plugin itself does not need to know about gRPC.
 */
class DaemonControlClient(
    private val address: String,
    private val velocityId: String,
    private val logger: Logger
) {

    private val running = AtomicBoolean(false)
    private var thread: Thread? = null

    fun start(consumer: (BackendEvent) -> Unit) {
        if (!running.compareAndSet(false, true)) return
        thread = Thread({ runLoop(consumer) }, "aethernet-control-$velocityId").also {
            it.isDaemon = true
            it.start()
        }
    }

    fun stop() {
        running.set(false)
        thread?.interrupt()
    }

    private fun runLoop(consumer: (BackendEvent) -> Unit) {
        var backoffMs = 250L
        while (running.get()) {
            try {
                val channel = io.grpc.ManagedChannelBuilder.forTarget(address)
                    .usePlaintext()
                    .build()
                // Once proto is generated:
                //   val stub = VelocityServiceGrpc.newBlockingStub(channel)
                //   stub.subscribe(SubscribeRequest.newBuilder().setVelocityId(velocityId).build())
                //       .forEach { ev -> consumer(BackendEvent.from(ev)) }
                logger.info("connected to AetherNet daemon at $address (skeleton: stream not yet wired)")
                while (running.get()) {
                    Thread.sleep(60_000)
                }
                channel.shutdownNow()
                backoffMs = 250L
            } catch (e: InterruptedException) {
                return
            } catch (t: Throwable) {
                logger.warn("daemon control stream error: ${t.message}; reconnecting in ${backoffMs}ms")
                Thread.sleep(backoffMs)
                backoffMs = (backoffMs * 2).coerceAtMost(15_000)
            }
        }
    }
}

sealed interface BackendEvent {
    data class Ready(
        val serverId: String,
        val groupId: String,
        val host: String,
        val port: Int,
        val maxPlayers: Int
    ) : BackendEvent

    data class Gone(val serverId: String, val reason: String) : BackendEvent
    data class Players(val serverId: String, val count: Int) : BackendEvent
    data class GroupRefresh(val groupId: String, val serverIds: List<String>) : BackendEvent
}
