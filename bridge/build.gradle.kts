plugins {
    kotlin("jvm") version "1.9.23" apply false
    id("com.google.protobuf") version "0.9.4" apply false
    id("com.github.johnrengelman.shadow") version "8.1.1" apply false
}

allprojects {
    group = "net.aethernet"
    version = "0.1.0"

    repositories {
        mavenCentral()
        maven("https://repo.papermc.io/repository/maven-public/")
        maven("https://repo.velocitypowered.com/snapshots/")
        maven("https://oss.sonatype.org/content/repositories/snapshots/")
    }
}
