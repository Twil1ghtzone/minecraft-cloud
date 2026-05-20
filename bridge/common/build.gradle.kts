plugins {
    kotlin("jvm")
}

dependencies {
    implementation("io.grpc:grpc-netty-shaded:1.64.0")
    implementation("io.grpc:grpc-protobuf:1.64.0")
    implementation("io.grpc:grpc-stub:1.64.0")
    implementation("io.grpc:grpc-kotlin-stub:1.4.1")
    implementation("com.google.protobuf:protobuf-java:3.25.3")
    implementation("redis.clients:jedis:5.1.3")
}

kotlin {
    jvmToolchain(21)
}
