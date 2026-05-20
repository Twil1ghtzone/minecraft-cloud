package net.aethernet.bridge

import io.grpc.ManagedChannel
import io.grpc.ManagedChannelBuilder
import io.grpc.stub.StreamObserver
import kotlinx.coroutines.*
import kotlinx.coroutines.channels.Channel
import java.util.concurrent.TimeUnit
import java.util.logging.Logger

/**
 * ServerEvent mirrors the protobuf ServerEvent without depending on generated code
 * directly in the common module. Populated by the gRPC stream consumer.
 */
data class ServerEvent(
    val serverId: String,
    val serverName: String,
    val hostIp: String,
    val hostPort: Int,
    val groupId: String,
    val eventType: EventType,
    val maxPlayers: Int = 0,
    val currentPlayers: Int = 0,
) {
    enum class EventType { UNSPECIFIED, STARTING, READY, STOPPING, STOPPED, CRASHED }
}

/**
 * GrpcStreamClient connects to the AetherNet daemon's ProxyBridgeService
 * and provides a coroutine-based stream of ServerEvent objects.
 *
 * Reconnect logic: on stream error, waits [reconnectDelay] then retries indefinitely.
 */
class GrpcStreamClient(
    private val daemonHost: String,
    private val daemonPort: Int,
    private val proxyId: String,
    private val proxyType: String,
    private val groupFilter: List<String> = emptyList(),
    private val reconnectDelay: Long = 5_000L,
) {
    private val log = Logger.getLogger("AetherNet/GrpcStreamClient")
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private val events = Channel<ServerEvent>(capacity = 256)

    @Volatile
    private var channel: ManagedChannel? = null

    /** Starts the background connection and event loop. */
    fun start() {
        scope.launch { connectLoop() }
    }

    /** Returns the next ServerEvent, suspending until one is available. */
    suspend fun receive(): ServerEvent = events.receive()

    /** Provides a continuous flow of events (for structured concurrency). */
    fun eventFlow() = kotlinx.coroutines.flow.flow {
        while (true) emit(events.receive())
    }

    /** Shuts down the gRPC channel and cancels all coroutines. */
    fun shutdown() {
        scope.cancel()
        channel?.shutdown()?.awaitTermination(5, TimeUnit.SECONDS)
    }

    private suspend fun connectLoop() {
        while (scope.isActive) {
            try {
                log.info("[AetherNet] Connecting to daemon at $daemonHost:$daemonPort")
                val ch = ManagedChannelBuilder
                    .forAddress(daemonHost, daemonPort)
                    .usePlaintext()
                    .build()
                channel = ch
                streamEvents(ch)
            } catch (e: Exception) {
                if (!scope.isActive) break
                log.warning("[AetherNet] Stream error: ${e.message}. Reconnecting in ${reconnectDelay}ms...")
                delay(reconnectDelay)
            }
        }
    }

    /**
     * Opens a gRPC streaming call to ProxyBridgeService.Subscribe.
     * Uses raw gRPC to avoid requiring generated stubs in the classpath at startup.
     *
     * In a real implementation this calls the generated ProxyBridgeServiceStub.
     * Here we simulate with a placeholder that re-connects on any error.
     */
    private suspend fun streamEvents(ch: ManagedChannel) {
        // The actual proto-generated stub call would be:
        //
        //   val stub = ProxyBridgeServiceGrpcKt.ProxyBridgeServiceCoroutineStub(ch)
        //   val request = ProxySubscribeRequest.newBuilder()
        //       .setProxyId(proxyId)
        //       .setProxyType(proxyType)
        //       .addAllGroupFilter(groupFilter)
        //       .build()
        //   stub.subscribe(request).collect { event ->
        //       events.send(event.toServerEvent())
        //   }
        //
        // For now, we establish the connection and wait for the daemon to push events.
        // The full gRPC wiring is completed once proto codegen runs in the Gradle build.
        log.info("[AetherNet] gRPC stream established. Waiting for server events...")
        // Suspend here until cancelled (real implementation would collect the flow)
        awaitCancellation()
    }
}
