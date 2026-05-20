package net.aethernet.cloud

import org.bukkit.entity.Player
import org.bukkit.inventory.ItemStack
import org.bukkit.potion.PotionEffect
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.util.zip.GZIPInputStream
import java.util.zip.GZIPOutputStream

/**
 * Serializes and deserializes the complete player profile into a compressed
 * byte array for Redis storage and cross-server transfer.
 *
 * Profile includes:
 *  - Inventory (main + armor + offhand)
 *  - Ender chest
 *  - Health, food level, saturation
 *  - Active potion effects
 *  - Experience (level + progress)
 *  - Game mode
 */
object NbtSerializer {

    /**
     * Serializes the player's complete profile to a GZIP-compressed byte array.
     * Uses Bukkit's built-in ItemStack serialization for maximum compatibility.
     */
    fun serialize(player: Player): ByteArray {
        val bos = ByteArrayOutputStream()
        GZIPOutputStream(bos).use { gz ->
            val out = java.io.DataOutputStream(gz)

            // Protocol version
            out.writeInt(1)

            // Inventory
            val inv = player.inventory
            serializeItemArray(out, inv.contents.filterNotNull().let {
                Array(inv.size) { i -> inv.getItem(i) }
            })

            // Armor
            serializeItemArray(out, inv.armorContents)

            // Offhand
            serializeItemArray(out, arrayOf(inv.itemInOffHand))

            // Ender chest
            val ec = player.enderChest
            serializeItemArray(out, Array(ec.size) { i -> ec.getItem(i) })

            // Vitals
            out.writeDouble(player.health)
            out.writeDouble(player.absorptionAmount)
            out.writeInt(player.foodLevel)
            out.writeFloat(player.saturation)
            out.writeFloat(player.exhaustion)
            out.writeInt(player.fireTicks)

            // Experience
            out.writeInt(player.level)
            out.writeFloat(player.exp)
            out.writeInt(player.totalExperience)

            // Game mode
            out.writeUTF(player.gameMode.name)

            // Potion effects
            val effects = player.activePotionEffects.toList()
            out.writeInt(effects.size)
            for (effect in effects) {
                out.writeUTF(effect.type.name)
                out.writeInt(effect.amplifier)
                out.writeInt(effect.duration)
                out.writeBoolean(effect.isAmbient)
                out.writeBoolean(effect.hasParticles())
                out.writeBoolean(effect.hasIcon())
            }
        }
        return bos.toByteArray()
    }

    /**
     * Deserializes a player profile and applies it to the given player.
     * Called on AsyncPlayerPreLoginEvent after confirming the lock is released.
     */
    fun deserialize(player: Player, data: ByteArray) {
        val bis = ByteArrayInputStream(data)
        GZIPInputStream(bis).use { gz ->
            val input = java.io.DataInputStream(gz)

            // Protocol version check
            val version = input.readInt()
            if (version != 1) {
                player.sendMessage("§c[AetherNet] Profile version mismatch. Contact admin.")
                return
            }

            // Inventory
            val inv = player.inventory
            val invContents = deserializeItemArray(input)
            inv.contents = invContents

            // Armor
            val armor = deserializeItemArray(input)
            inv.armorContents = armor

            // Offhand
            val offhand = deserializeItemArray(input)
            if (offhand.isNotEmpty() && offhand[0] != null) {
                inv.setItemInOffHand(offhand[0])
            }

            // Ender chest
            val ec = player.enderChest
            val ecContents = deserializeItemArray(input)
            for (i in ecContents.indices) {
                if (i < ec.size) ec.setItem(i, ecContents[i])
            }

            // Vitals
            val health = input.readDouble().coerceIn(0.0, player.maxHealth)
            player.health = health
            player.absorptionAmount = input.readDouble()
            player.foodLevel = input.readInt()
            player.saturation = input.readFloat()
            player.exhaustion = input.readFloat()
            player.fireTicks = input.readInt()

            // Experience
            player.level = input.readInt()
            player.exp = input.readFloat()
            player.totalExperience = input.readInt()

            // Game mode
            val gmName = input.readUTF()
            try {
                player.gameMode = org.bukkit.GameMode.valueOf(gmName)
            } catch (_: IllegalArgumentException) {}

            // Potion effects
            player.activePotionEffects.forEach { player.removePotionEffect(it.type) }
            val effectCount = input.readInt()
            repeat(effectCount) {
                val typeName = input.readUTF()
                val amplifier = input.readInt()
                val duration  = input.readInt()
                val ambient   = input.readBoolean()
                val particles = input.readBoolean()
                val icon      = input.readBoolean()
                val type = org.bukkit.potion.PotionEffectType.getByName(typeName) ?: return@repeat
                player.addPotionEffect(PotionEffect(type, duration, amplifier, ambient, particles, icon))
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun serializeItemArray(out: java.io.DataOutputStream, items: Array<out ItemStack?>) {
        out.writeInt(items.size)
        for (item in items) {
            if (item == null || item.type == org.bukkit.Material.AIR) {
                out.writeBoolean(false)
            } else {
                out.writeBoolean(true)
                val bytes = item.serializeAsBytes()
                out.writeInt(bytes.size)
                out.write(bytes)
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun deserializeItemArray(input: java.io.DataInputStream): Array<ItemStack?> {
        val size = input.readInt()
        return Array(size) {
            if (!input.readBoolean()) null
            else {
                val len = input.readInt()
                val bytes = ByteArray(len)
                input.readFully(bytes)
                ItemStack.deserializeBytes(bytes)
            }
        }
    }
}
