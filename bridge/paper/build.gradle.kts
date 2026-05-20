plugins {
    kotlin("jvm")
    id("com.github.johnrengelman.shadow")
    kotlin("kapt")
}

dependencies {
    compileOnly("io.papermc.paper:paper-api:1.21.1-R0.1-SNAPSHOT")
    implementation(project(":common"))
    // Lettuce for Redis Cluster client
    implementation("io.lettuce:lettuce-core:6.3.2.RELEASE")
    // NBT serialization
    implementation("net.kyori:adventure-nbt:4.17.0")
    // McMMO soft-dependency (compileOnly)
    compileOnly("com.gmail.nossr50.mcMMO:mcMMO:2.1.221") {
        isTransitive = false
    }
}

tasks.shadowJar {
    archiveClassifier.set("")
    mergeServiceFiles()
    relocate("io.lettuce", "net.aethernet.shade.lettuce")
    relocate("io.netty", "net.aethernet.shade.netty")
}
