plugins {
    kotlin("jvm")
    id("com.google.protobuf")
}

dependencies {
    api("io.grpc:grpc-stub:1.63.0")
    api("io.grpc:grpc-protobuf:1.63.0")
    api("io.grpc:grpc-netty-shaded:1.63.0")
    api("com.google.protobuf:protobuf-kotlin:3.25.3")
    implementation(kotlin("stdlib"))
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.8.0")
}

protobuf {
    protoc {
        artifact = "com.google.protobuf:protoc:3.25.3"
    }
    plugins {
        create("grpc") {
            artifact = "io.grpc:protoc-gen-grpc-java:1.63.0"
        }
        create("grpckt") {
            artifact = "io.grpc:protoc-gen-grpc-kotlin:1.4.1:jdk8@jar"
        }
    }
    generateProtoTasks {
        all().forEach {
            it.plugins {
                create("grpc")
                create("grpckt")
            }
            it.builtins {
                create("kotlin")
            }
        }
    }
}
