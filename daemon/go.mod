module github.com/aethernet/aethernet/daemon

go 1.22

require (
	github.com/aethernet/aethernet/pkg v0.0.0
	github.com/docker/docker v26.1.4+incompatible
	github.com/docker/go-connections v0.5.0
	github.com/docker/go-units v0.5.0
	github.com/go-sql-driver/mysql v1.8.1
	github.com/hashicorp/raft v1.7.0
	github.com/hashicorp/raft-boltdb/v2 v2.3.0
	github.com/pkg/sftp v1.13.6
	github.com/redis/go-redis/v9 v9.5.3
	golang.org/x/crypto v0.24.0
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.34.1
	gopkg.in/yaml.v3 v3.0.1
	github.com/prometheus/client_golang v1.19.1
	github.com/minio/minio-go/v7 v7.0.72
)

replace github.com/aethernet/aethernet/pkg => ../pkg
