package net.aethernet.bridge.paper

import org.bukkit.entity.Player

/**
 * Hook for third-party plugins to participate in the cross-server state
 * blob. A skill plugin, for instance, registers an extension here and
 * provides its own serializer/deserializer so the player's skill levels
 * also travel between servers.
 */
object PlayerProfileExtensions {

    interface Extension {
        val key: String
        fun serialize(p: Player): ByteArray
        fun deserialize(p: Player, data: ByteArray)
    }

    private val extensions = mutableListOf<Extension>()

    fun register(ext: Extension) { extensions += ext }

    fun collect(p: Player): Map<String, ByteArray> {
        return extensions.associate { it.key to it.serialize(p) }
    }

    fun apply(p: Player, data: Map<String, ByteArray>) {
        extensions.forEach { ext ->
            data[ext.key]?.let { ext.deserialize(p, it) }
        }
    }
}
