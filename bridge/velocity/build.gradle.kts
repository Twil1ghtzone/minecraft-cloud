plugins {
    kotlin("jvm")
    id("com.github.johnrengelman.shadow")
}

dependencies {
    compileOnly("com.velocitypowered:velocity-api:3.3.0-SNAPSHOT")
    implementation(project(":common"))
    kapt("com.velocitypowered:velocity-api:3.3.0-SNAPSHOT")
}

tasks.shadowJar {
    archiveClassifier.set("")
    mergeServiceFiles()
}
