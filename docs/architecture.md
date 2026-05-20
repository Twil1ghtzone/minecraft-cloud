# AetherNet — Architecture

## Design pillars

1. **No single point of failure.** Every node runs the same daemon and panel
   binary. Reads are served locally from a Raft-replicated state machine;
   writes are forwarded over gRPC to the current leader.
2. **OCI isolation per game server.** Each Minecraft instance is a Docker
   container with a read-only base layer and an NVMe scratch volume.
3. **Live ingress.** Velocity is notified of server lifecycle transitions
   in real time via a gRPC stream — no config reloads, no disconnects.
4. **Atomic player state.** Inventory/enderchest/skill data is locked in
   Redis on quit, serialized to NBT, and unlocked on the destination
   server's pre-login event.

## Consensus

The cluster forms a single Raft group. A write is committed when a quorum
acknowledges it:

```
Q = floor(N/2) + 1
```

| N (nodes) | Q | Tolerable failures |
|-----------|---|--------------------|
| 1 | 1 | 0 |
| 3 | 2 | 1 |
| 5 | 3 | 2 |
| 7 | 4 | 3 |

Even-N clusters are discouraged because they raise quorum without raising
fault tolerance.

### Leader election timings

- `HeartbeatTimeout`: 500ms (leader → followers)
- `ElectionTimeout`:  1000ms (follower → candidate threshold)
- A node is declared dead after 1000ms of missed heartbeats and the leader
  begins evacuating its workload.

## State machine

The Raft FSM holds:

- `nodes`        — cluster membership and last heartbeat
- `servers`      — minecraft instance definitions and current placement
- `groups`       — tasks/groups (Module 7)
- `templates`    — static + dynamic server templates
- `databases`    — provisioned MySQL/Redis databases
- `tokens`       — REST API bearer tokens
- `players`      — minimal routing info (last server, last seen)

Player **state** (inventory etc.) is kept in Redis, not Raft — it is
high-churn and short-lived, and Raft is poorly suited to that workload.

## Data flow: starting a Minecraft server

```
Panel ──HTTP──> Local daemon ──gRPC──> Leader
                                         │
                                         ▼
                                  Apply(StartServer)
                                         │
                              ┌──────────┴───────────┐
                              ▼                      ▼
                         Replicate                Schedule
                       (commit on Q)         (pick least-loaded
                                              node by free RAM)
                              │                      │
                              └──────────┬───────────┘
                                         ▼
                              Target node: docker run
                                         │
                                         ▼
                              READY → gRPC stream → Velocity
                              Velocity registers backend live
```

## Failover

When the leader observes `now - lastHeartbeat[N] > 1000ms`:

1. The node is marked `Down` in the FSM.
2. For each server whose `Placement == N`:
   - If the server is in a HA-required group, it is re-scheduled on the
     least-loaded surviving node.
   - Otherwise its status becomes `Orphaned` and it can be revived manually.
3. The orphaned panel sessions continue to read from the local replica;
   they only stall on writes until a new leader is elected (≤ 1000ms).

## Two consensus systems?

Yes: Raft (for control plane) **and** Galera (for tenant SQL data). This
is intentional. Raft holds small, structured state and benefits from a
single leader; Galera holds arbitrary tenant SQL workloads, which we do
not want to funnel through a single elected leader.

The two systems are independent — Galera is what users see when they
provision a database through the panel; Raft is what holds the
"which database is provisioned" metadata.

## State sync (Module 6) — race-free transfer

```
[Player on Server A]                        [Server B]

quit event
  │
  ├── SETNX lock:player:{UUID}
  │   (TTL 30s, fails closed)
  ├── serialize → NBT byte array
  ├── SET player:data:{UUID} = NBT
  └── DEL lock:player:{UUID}

                                             pre-login
                                              │
                                              ├── WAIT lock:player:{UUID}
                                              │   (poll every 10ms, max 2s)
                                              ├── GET player:data:{UUID}
                                              ├── deserialize NBT
                                              └── inject into PlayerJoinEvent
```

If lock acquisition fails on quit (already held), the player is held on
the source server and the transfer is aborted — never duplicated.

## Security boundaries

- **SFTP**: chroot per container, key auth only, no shell. Login form is
  `serverID.username`.
- **Panel**: session cookies signed with a per-cluster HMAC key (in Raft FSM).
- **REST**: bearer tokens stored hashed (argon2id) in Raft FSM, scoped
  per route prefix.
- **gRPC internal**: mTLS with a cluster CA, rotated on bootstrap.
