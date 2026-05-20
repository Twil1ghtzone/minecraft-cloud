package net.aethernet.bridge.velocity

import java.nio.file.Files
import java.nio.file.Path
import java.util.Properties

data class AetherConfig(
    val daemonAddress: String,
    val velocityId: String,
    val redisHost: String,
    val redisPort: Int,
    val redisPassword: String?
) {
    companion object {
        fun loadOrCreate(path: Path): AetherConfig {
            if (!Files.exists(path)) {
                Files.createDirectories(path.parent)
                Files.writeString(path, defaults)
            }
            val props = Properties().apply {
                Files.newBufferedReader(path).use { load(it) }
            }
            return AetherConfig(
                daemonAddress = props.getProperty("daemon.address", "127.0.0.1:7001"),
                velocityId    = props.getProperty("velocity.id", "velocity-${System.currentTimeMillis()}"),
                redisHost     = props.getProperty("redis.host", "127.0.0.1"),
                redisPort     = props.getProperty("redis.port", "6379").toInt(),
                redisPassword = props.getProperty("redis.password").takeUnless { it.isNullOrBlank() }
            )
        }

        private val defaults = """
            # AetherNet Velocity bridge configuration
            daemon.address=127.0.0.1:7001
            velocity.id=velocity-1
            redis.host=127.0.0.1
            redis.port=6379
            redis.password=
        """.trimIndent()
    }
}
