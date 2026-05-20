plugins {
    kotlin("jvm")
    id("com.github.johnrengelman.shadow")
}

dependencies {
    implementation(project(":common"))
    compileOnly("com.velocitypowered:velocity-api:3.3.0-SNAPSHOT")
    kapt("com.velocitypowered:velocity-api:3.3.0-SNAPSHOT")
}

tasks {
    shadowJar {
        archiveClassifier.set("")
        relocate("redis.clients.jedis", "net.aethernet.shaded.jedis")
    }
}

kotlin { jvmToolchain(21) }
