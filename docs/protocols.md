# AetherNet — On-the-wire protocols

## gRPC: internal node-to-node

All cluster-internal traffic uses gRPC over mTLS. Cert chain is rooted at a
CA created on cluster bootstrap and stored in the Raft FSM under `pki/ca`.
Per-node certificates are issued by the leader on join.

Services:

- `ClusterService`  — Raft membership, leader hint, FSM snapshots
- `DaemonService`   — server lifecycle (Start/Stop/Restart/Status/Logs)
- `ServerService`   — high-level scheduling decisions
- `DatabaseService` — provision/drop databases
- `VelocityService` — backend registration/deregistration stream

## REST: external

Bearer auth via `Authorization: Bearer <token>`. Tokens are scoped:

| Scope | Allows |
|-------|--------|
| `read:cluster` | GET /api/v1/cluster/* |
| `write:cluster` | mutate cluster membership (very rare) |
| `read:servers` | list and inspect servers |
| `write:servers` | start/stop/restart/delete servers |
| `read:logs` | stream container logs |
| `read:databases` | list databases |
| `write:databases` | provision/drop databases |
| `admin:*` | all of the above |

Endpoints (non-exhaustive):

```
POST   /api/v1/cluster/servers/start         { template_id, group_id?, env? }
DELETE /api/v1/cluster/servers/{id}
GET    /api/v1/cluster/servers/{id}
GET    /api/v1/cluster/servers/{id}/logs     (chunked / SSE)
POST   /api/v1/cluster/servers/{id}/exec     { command }

POST   /api/v1/databases                     { name, engine }
DELETE /api/v1/databases/{id}
GET    /api/v1/databases

GET    /api/v1/cluster/nodes
GET    /api/v1/cluster/leader

POST   /api/v1/groups                        { name, template_id, min, max }
POST   /api/v1/templates                     { name, mode: static|dynamic, ... }
```

## SFTP

Native Go SFTP server on TCP/2022, one per node. Authentication:

```
User: <server-uuid>.<username>
Auth: ssh public key
```

Resolution:

1. The server-uuid is looked up in the Raft FSM to find the host node.
2. If the server is on **this** node, the SFTP server resolves
   `/` to the container's data volume mountpoint (chroot via the
   `WithChroot` handler middleware).
3. If the server is on **another** node, this node refuses the
   connection with a server-banner pointing at the correct host.

## Redis keyspace (Module 6)

```
lock:player:{UUID}     — SETNX with 30s TTL, value = source server id
player:data:{UUID}     — gzipped NBT payload
player:route:{UUID}    — last server id (for reconnect hints)
server:players:{SID}   — SET of UUIDs currently on that server (for Sign API)
sign:state             — published hash, fanned out to all Velocity proxies
```
