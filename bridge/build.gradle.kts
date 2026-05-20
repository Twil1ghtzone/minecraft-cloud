plugins {
    kotlin("jvm") version "1.9.24" apply false
    id("com.github.johnrengelman.shadow") version "8.1.1" apply false
}

subprojects {
    repositories {
        mavenCentral()
        maven("https://repo.papermc.io/repository/maven-public/")
        maven("https://oss.sonatype.org/content/repositories/snapshots/")
    }
}
