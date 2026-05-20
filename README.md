# AetherNet

Decentralized, highly-available Minecraft cloud operating system.

Architectural lineage:
- **Proxmox-style** multi-master clustering (HashiCorp Raft consensus)
- **Pterodactyl/Pelican-style** OCI container isolation
- **CloudNet-style** dynamic routing, templates, and tasks/groups

## Repository Layout

```
.
├── proto/                # gRPC service definitions
├── daemon/               # aether-daemon (Go) - runs on every cluster node
│   ├── cmd/aether-daemon # binary entrypoint
│   └── internal/         # raft, cluster, docker, sftp, db, state, modproxy, api
├── panel/                # web panel backend (Go) + static frontend
├── bridge/               # Minecraft Kotlin plugins
│   ├── velocity/         # proxy-side dynamic routing
│   └── paper/            # backend-side atomic state sync
├── installer/            # interactive bash bootstrapper
└── docs/                 # architecture and protocol notes
```

## Quick Start

```bash
# On each prospective cluster node (Debian/Ubuntu):
curl -fsSL https://example.invalid/install.sh | sudo bash
# or, from a checkout:
sudo ./installer/install.sh
```

The installer performs pre-flight validation, provisions Docker, MariaDB Galera,
Redis Cluster, and bootstraps `aether-daemon` into systemd. On the first node
the Raft cluster is initialized; subsequent nodes join via a one-time token.

## Components

| Module | Path | Description |
|--------|------|-------------|
| 1 | `daemon/internal/raft`, `cluster` | Multi-master consensus, failover |
| 2 | `daemon/internal/database`, `panel/internal/workbench` | Galera/Redis + DB workbench |
| 3 | `daemon/internal/docker`, `sftp` | OCI isolation + native SFTP |
| 4 | `bridge/velocity` | Live ingress routing for Velocity |
| 5 | `daemon/internal/modproxy` | Modrinth/CurseForge mod lifecycle |
| 6 | `bridge/paper` | Atomic player-state sync via Redis |
| 7 | `daemon/internal/tasks` | Tasks, groups, templates, sign/tab API |
| 8 | `daemon/internal/api`, `panel/internal/handlers` | REST + gRPC |
| 9 | `installer/install.sh` | Interactive deployment |

See `docs/architecture.md` for the design rationale and consensus math.

## Building

```bash
make proto         # regenerate protobuf bindings
make daemon        # build aether-daemon
make panel         # build aether-panel
make bridge        # build Velocity + Paper plugins (requires JDK 21)
make all
```

## License

Proprietary — internal project.
