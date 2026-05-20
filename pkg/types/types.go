// Package types holds Go-native value types shared between daemon and panel.
// These mirror the canonical protobuf messages but avoid forcing every
// caller to depend on the generated bindings.
package types

import "time"

type NodeState uint8

const (
	NodeUnknown NodeState = iota
	NodeUp
	NodeSuspect
	NodeDown
	NodeDraining
)

func (s NodeState) String() string {
	switch s {
	case NodeUp:
		return "up"
	case NodeSuspect:
		return "suspect"
	case NodeDown:
		return "down"
	case NodeDraining:
		return "draining"
	}
	return "unknown"
}

type Resources struct {
	MemoryTotalMB uint64  `json:"memory_total_mb"`
	MemoryUsedMB  uint64  `json:"memory_used_mb"`
	CPUCores      uint32  `json:"cpu_cores"`
	CPULoad       float64 `json:"cpu_load"`
	DiskTotalMB   uint64  `json:"disk_total_mb"`
	DiskUsedMB    uint64  `json:"disk_used_mb"`
}

// FreeMemoryMB is the headroom used by the scheduler when picking a host.
func (r Resources) FreeMemoryMB() uint64 {
	if r.MemoryUsedMB >= r.MemoryTotalMB {
		return 0
	}
	return r.MemoryTotalMB - r.MemoryUsedMB
}

type Node struct {
	ID            string    `json:"id"`
	Address       string    `json:"address"`
	State         NodeState `json:"state"`
	Resources     Resources `json:"resources"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	IsLeader      bool      `json:"is_leader"`
	Version       string    `json:"version"`
}

type ServerState uint8

const (
	ServerStopped ServerState = iota
	ServerStarting
	ServerReady
	ServerStopping
	ServerCrashed
	ServerOrphaned
)

func (s ServerState) String() string {
	switch s {
	case ServerStopped:
		return "stopped"
	case ServerStarting:
		return "starting"
	case ServerReady:
		return "ready"
	case ServerStopping:
		return "stopping"
	case ServerCrashed:
		return "crashed"
	case ServerOrphaned:
		return "orphaned"
	}
	return "unknown"
}

type ServerSpec struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	TemplateID     string            `json:"template_id"`
	GroupID        string            `json:"group_id,omitempty"`
	Image          string            `json:"image"`
	MemoryMB       uint64            `json:"memory_mb"`
	CPUQuota       float64           `json:"cpu_quota"`
	Env            map[string]string `json:"env,omitempty"`
	MinecraftPort  uint32            `json:"minecraft_port"`
	HARequired     bool              `json:"ha_required"`
}

type Server struct {
	Spec        ServerSpec  `json:"spec"`
	NodeID      string      `json:"node_id"`
	State       ServerState `json:"state"`
	PlayerCount uint32      `json:"player_count"`
	MaxPlayers  uint32      `json:"max_players"`
	StartedAt   time.Time   `json:"started_at,omitempty"`
	ContainerID string      `json:"container_id,omitempty"`
	HostPort    uint32      `json:"host_port,omitempty"`
}

type Group struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TemplateID  string `json:"template_id"`
	MinInstances int   `json:"min_instances"`
	MaxInstances int   `json:"max_instances"`
	HARequired   bool  `json:"ha_required"`
}

type Template struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Mode  string `json:"mode"` // "static" or "dynamic"
	Image string `json:"image"`
	BaseVolume string `json:"base_volume"`
}

type Database struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Engine    string    `json:"engine"`
	Username  string    `json:"username"`
	Host      string    `json:"host"`
	Port      uint32    `json:"port"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes uint64    `json:"size_bytes"`
}

type Token struct {
	ID          string    `json:"id"`
	Hash        string    `json:"hash"` // sha256 full hex
	Description string    `json:"description"`
	Scopes      []string  `json:"scopes"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
}
