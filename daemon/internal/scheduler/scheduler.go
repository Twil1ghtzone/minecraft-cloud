// Package scheduler is responsible for:
//
//  1. Placement: choosing a host node when a server is started.
//  2. Failover: re-scheduling HA-required servers off a dead node.
//  3. Group autoscaling: spawning/despawning dynamic templates to keep
//     each group's instance count between Min and Max.
//
// Only the Raft leader runs the scheduler loop; followers track state
// via FSM events and act only when promoted to leader.
package scheduler

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/cluster"
	"github.com/aethernet/aethernet/daemon/internal/docker"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/pkg/types"
)

type Options struct {
	Cluster *cluster.Cluster
	FSM     *raftfsm.FSM
	Docker  *docker.Controller
	Logger  *slog.Logger

	// HeartbeatDeadline: a node missed this much heartbeat -> Down.
	HeartbeatDeadline time.Duration
	// FailoverGrace: extra wait before evacuating an orphaned server,
	// to absorb brief network blips.
	FailoverGrace time.Duration
}

type Scheduler struct {
	opts Options
	mu   sync.Mutex
}

func New(o Options) *Scheduler {
	if o.HeartbeatDeadline == 0 {
		o.HeartbeatDeadline = 1000 * time.Millisecond
	}
	if o.FailoverGrace == 0 {
		o.FailoverGrace = 2 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Scheduler{opts: o}
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.opts.Cluster.IsLeader() {
				continue
			}
			s.tick(ctx)
		}
	}
}

// tick runs every scheduler heartbeat on the leader. It does liveness
// checks and group autoscaling decisions.
func (s *Scheduler) tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	deadline := s.opts.HeartbeatDeadline + s.opts.FailoverGrace

	// 1. Liveness check.
	for _, node := range s.opts.FSM.Nodes() {
		if node.State == types.NodeDown {
			continue
		}
		if now.Sub(node.LastHeartbeat) > deadline {
			s.opts.Logger.Warn("node missed heartbeat deadline; marking down",
				"node_id", node.ID,
				"last_hb_age_ms", now.Sub(node.LastHeartbeat).Milliseconds(),
			)
			s.markNodeDown(ctx, node.ID)
			s.evacuate(ctx, node.ID)
		} else if now.Sub(node.LastHeartbeat) > s.opts.HeartbeatDeadline && node.State != types.NodeSuspect {
			node.State = types.NodeSuspect
			_ = s.applyNode(ctx, node)
		}
	}

	// 2. Group autoscaling.
	servers := s.opts.FSM.Servers()
	byGroup := map[string][]types.Server{}
	for _, srv := range servers {
		if srv.Spec.GroupID == "" {
			continue
		}
		byGroup[srv.Spec.GroupID] = append(byGroup[srv.Spec.GroupID], srv)
	}
	for _, g := range s.opts.FSM.Groups() {
		current := byGroup[g.ID]
		if len(current) < g.MinInstances {
			needed := g.MinInstances - len(current)
			for i := 0; i < needed; i++ {
				_ = s.spawnGroupInstance(ctx, g)
			}
		} else if g.MaxInstances > 0 && len(current) > g.MaxInstances {
			// Stop the least-loaded instance (fewest players).
			sort.Slice(current, func(i, j int) bool {
				return current[i].PlayerCount < current[j].PlayerCount
			})
			drop := len(current) - g.MaxInstances
			for i := 0; i < drop; i++ {
				_ = s.stopServer(ctx, current[i].Spec.ID)
			}
		}
	}
}

func (s *Scheduler) markNodeDown(ctx context.Context, nodeID string) {
	n, ok := s.opts.FSM.Node(nodeID)
	if !ok {
		return
	}
	n.State = types.NodeDown
	_ = s.applyNode(ctx, n)
}

func (s *Scheduler) applyNode(ctx context.Context, n types.Node) error {
	data, err := raftfsm.Encode(raftfsm.CmdNodeUpsert, n, "", time.Now().UnixNano())
	if err != nil {
		return err
	}
	return s.opts.Cluster.Apply(ctx, data, 2*time.Second)
}

// evacuate re-schedules HA-required servers off a dead node.
func (s *Scheduler) evacuate(ctx context.Context, deadNodeID string) {
	orphans := s.opts.FSM.ServersOnNode(deadNodeID)
	for _, srv := range orphans {
		if !srv.Spec.HARequired {
			// Mark orphaned; operator-decided revival.
			srv.State = types.ServerOrphaned
			data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
			_ = s.opts.Cluster.Apply(ctx, data, 2*time.Second)
			continue
		}
		target, err := s.pickHost(srv.Spec, deadNodeID)
		if err != nil {
			s.opts.Logger.Error("failover: no viable host", "server_id", srv.Spec.ID, "err", err)
			continue
		}
		srv.NodeID = target.ID
		srv.State = types.ServerStarting
		srv.ContainerID = ""
		srv.HostPort = 0
		data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
		if err := s.opts.Cluster.Apply(ctx, data, 2*time.Second); err != nil {
			s.opts.Logger.Error("failover apply", "err", err)
			continue
		}
		s.opts.Logger.Info("evacuated server to new host",
			"server_id", srv.Spec.ID,
			"from", deadNodeID,
			"to", target.ID,
		)
	}
}

// PickHost is the public entrypoint used by API handlers to choose a host.
// It returns ErrNoHost if no node has enough free memory.
func (s *Scheduler) PickHost(spec types.ServerSpec) (types.Node, error) {
	return s.pickHost(spec, "")
}

// pickHost: simple "least loaded by free memory" placement, ignoring `excludeNode`.
func (s *Scheduler) pickHost(spec types.ServerSpec, excludeNode string) (types.Node, error) {
	candidates := []types.Node{}
	for _, n := range s.opts.FSM.Nodes() {
		if n.State != types.NodeUp || n.ID == excludeNode {
			continue
		}
		if n.Resources.FreeMemoryMB() < spec.MemoryMB {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		return types.Node{}, ErrNoHost
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Resources.FreeMemoryMB() > candidates[j].Resources.FreeMemoryMB()
	})
	return candidates[0], nil
}

func (s *Scheduler) spawnGroupInstance(ctx context.Context, g types.Group) error {
	tpl, ok := s.opts.FSM.Template(g.TemplateID)
	if !ok {
		return ErrTemplateMissing
	}
	spec := types.ServerSpec{
		ID:            newID("srv"),
		Name:          g.Name + "-" + shortID(),
		TemplateID:    tpl.ID,
		GroupID:       g.ID,
		Image:         tpl.Image,
		MemoryMB:      2048,
		CPUQuota:      1.0,
		MinecraftPort: 25565,
		HARequired:    g.HARequired,
	}
	host, err := s.pickHost(spec, "")
	if err != nil {
		return err
	}
	srv := types.Server{
		Spec:   spec,
		NodeID: host.ID,
		State:  types.ServerStarting,
	}
	data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
	return s.opts.Cluster.Apply(ctx, data, 2*time.Second)
}

func (s *Scheduler) stopServer(ctx context.Context, id string) error {
	srv, ok := s.opts.FSM.Server(id)
	if !ok {
		return ErrServerMissing
	}
	srv.State = types.ServerStopping
	data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
	return s.opts.Cluster.Apply(ctx, data, 2*time.Second)
}
