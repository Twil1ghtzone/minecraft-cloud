package net.aethernet.bridge.paper

import org.bukkit.Bukkit
import org.bukkit.entity.Player
import org.bukkit.inventory.ItemStack
import org.bukkit.util.io.BukkitObjectInputStream
import org.bukkit.util.io.BukkitObjectOutputStream
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.util.zip.GZIPInputStream
import java.util.zip.GZIPOutputStream

/**
 * Bukkit's BukkitObjectOutputStream serializes ItemStacks together with
 * their NBT tags, so we use that to capture the parts of the profile a
 * vanilla server cares about, then gzip the result.
 *
 * Things not serialized here (because they live in third-party plugin
 * data, not in vanilla state) include skill plugins, economy balances,
 * etc. Those plugins are expected to register their own ProfileExtension
 * via the PDC-based hook in [PlayerProfileExtensions].
 */
object NbtSerializer {

    fun serialize(p: Player): ByteArray {
        val out = ByteArrayOutputStream()
        GZIPOutputStream(out).use { gz ->
            BukkitObjectOutputStream(gz).use { os ->
                os.writeObject(p.inventory.contents)
                os.writeObject(p.inventory.armorContents)
                os.writeObject(p.inventory.extraContents)
                os.writeObject(p.enderChest.contents)
                os.writeDouble(p.health)
                os.writeInt(p.foodLevel)
                os.writeFloat(p.saturation)
                os.writeInt(p.totalExperience)
                os.writeInt(p.level)
                os.writeFloat(p.exp)
                os.writeObject(p.activePotionEffects.toTypedArray())
                // Extension data registered by other plugins:
                os.writeObject(PlayerProfileExtensions.collect(p))
            }
        }
        return out.toByteArray()
    }

    fun deserialize(p: Player, blob: ByteArray) {
        ByteArrayInputStream(blob).use { ba ->
            GZIPInputStream(ba).use { gz ->
                BukkitObjectInputStream(gz).use { ois ->
                    @Suppress("UNCHECKED_CAST")
                    p.inventory.contents      = ois.readObject() as Array<ItemStack?>
                    @Suppress("UNCHECKED_CAST")
                    p.inventory.armorContents = ois.readObject() as Array<ItemStack?>
                    @Suppress("UNCHECKED_CAST")
                    p.inventory.extraContents = ois.readObject() as Array<ItemStack?>
                    @Suppress("UNCHECKED_CAST")
                    p.enderChest.contents     = ois.readObject() as Array<ItemStack?>
                    p.health        = ois.readDouble()
                    p.foodLevel     = ois.readInt()
                    p.saturation    = ois.readFloat()
                    p.totalExperience = ois.readInt()
                    p.level         = ois.readInt()
                    p.exp           = ois.readFloat()
                    val pots = ois.readObject() as Array<*>
                    p.activePotionEffects.forEach { p.removePotionEffect(it.type) }
                    pots.forEach {
                        if (it is org.bukkit.potion.PotionEffect) p.addPotionEffect(it)
                    }
                    val ext = ois.readObject()
                    if (ext is Map<*, *>) {
                        @Suppress("UNCHECKED_CAST")
                        PlayerProfileExtensions.apply(p, ext as Map<String, ByteArray>)
                    }
                }
            }
        }
        // Force the server to immediately persist what we just loaded so
        // the next time the player rejoins (without going through us),
        // they get the right state from disk.
        Bukkit.getScheduler().runTask(
            Bukkit.getPluginManager().getPlugin("AetherNetBridge")!!
        ) { _ -> p.saveData() }
    }
}
