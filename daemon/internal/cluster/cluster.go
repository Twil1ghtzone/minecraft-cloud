// Package cluster wraps hashicorp/raft and provides a small surface area
// the rest of the daemon talks to (Apply, IsLeader, LeaderAddr, Members,
// Subscribe).
package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/pkg/quorum"
	"github.com/hashicorp/raft"
	boltdb "github.com/hashicorp/raft-boltdb/v2"
)

type JoinHook func(ctx context.Context, addr, token string) error

type Options struct {
	NodeID        string
	BindAddr      string
	AdvertiseAddr string
	DataDir       string
	Bootstrap     bool
	Logger        *slog.Logger
	FSM           *raftfsm.FSM
}

type Cluster struct {
	opts     Options
	raft     *raft.Raft
	log      *slog.Logger
	tr       *raft.NetworkTransport
	joinHook JoinHook
}

// Heartbeat / election timings — matches the spec:
//   HeartbeatTimeout 500ms, ElectionTimeout 1000ms.
func tuneRaftConfig(cfg *raft.Config, nodeID string) {
	cfg.LocalID = raft.ServerID(nodeID)
	cfg.HeartbeatTimeout = 500 * time.Millisecond
	cfg.ElectionTimeout = 1000 * time.Millisecond
	cfg.LeaderLeaseTimeout = 500 * time.Millisecond
	cfg.CommitTimeout = 50 * time.Millisecond
	cfg.SnapshotInterval = 2 * time.Minute
	cfg.SnapshotThreshold = 8192
}

func New(opts Options) (*Cluster, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}

	cfg := raft.DefaultConfig()
	tuneRaftConfig(cfg, opts.NodeID)

	advAddr, err := net.ResolveTCPAddr("tcp", opts.AdvertiseAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve advertise: %w", err)
	}
	tr, err := raft.NewTCPTransport(opts.BindAddr, advAddr, 4, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}

	logStore, err := boltdb.NewBoltStore(filepath.Join(opts.DataDir, "raft-log.bolt"))
	if err != nil {
		return nil, fmt.Errorf("log store: %w", err)
	}
	stableStore, err := boltdb.NewBoltStore(filepath.Join(opts.DataDir, "raft-stable.bolt"))
	if err != nil {
		return nil, fmt.Errorf("stable store: %w", err)
	}
	snapStore, err := raft.NewFileSnapshotStore(opts.DataDir, 3, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("snap store: %w", err)
	}

	r, err := raft.NewRaft(cfg, opts.FSM, logStore, stableStore, snapStore, tr)
	if err != nil {
		return nil, fmt.Errorf("raft: %w", err)
	}

	c := &Cluster{opts: opts, raft: r, log: opts.Logger, tr: tr}

	if opts.Bootstrap {
		hasState, _ := raft.HasExistingState(logStore, stableStore, snapStore)
		if !hasState {
			bootCfg := raft.Configuration{
				Servers: []raft.Server{
					{ID: raft.ServerID(opts.NodeID), Address: tr.LocalAddr()},
				},
			}
			if err := r.BootstrapCluster(bootCfg).Error(); err != nil {
				return nil, fmt.Errorf("bootstrap: %w", err)
			}
			opts.Logger.Info("bootstrapped new single-node cluster", "node_id", opts.NodeID)
		}
	}

	return c, nil
}

func (c *Cluster) Close() error {
	if c.raft == nil {
		return nil
	}
	return c.raft.Shutdown().Error()
}

// IsLeader returns true if this node currently holds the Raft leadership.
func (c *Cluster) IsLeader() bool { return c.raft.State() == raft.Leader }

// LeaderAddr returns the gRPC address of the current leader, or "" if none.
func (c *Cluster) LeaderAddr() string {
	addr, _ := c.raft.LeaderWithID()
	return string(addr)
}

func (c *Cluster) LeaderID() string {
	_, id := c.raft.LeaderWithID()
	return string(id)
}

// Apply commits a command to the FSM. Must be called on the leader.
func (c *Cluster) Apply(ctx context.Context, data []byte, timeout time.Duration) error {
	if !c.IsLeader() {
		return ErrNotLeader
	}
	fut := c.raft.Apply(data, timeout)
	done := make(chan error, 1)
	go func() { done <- fut.Error() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Cluster) AddVoter(id, addr string) error {
	if !c.IsLeader() {
		return ErrNotLeader
	}
	return c.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 10*time.Second).Error()
}

func (c *Cluster) RemoveServer(id string) error {
	if !c.IsLeader() {
		return ErrNotLeader
	}
	return c.raft.RemoveServer(raft.ServerID(id), 0, 10*time.Second).Error()
}

func (c *Cluster) Members() ([]Member, error) {
	cfg := c.raft.GetConfiguration()
	if err := cfg.Error(); err != nil {
		return nil, err
	}
	out := make([]Member, 0)
	for _, s := range cfg.Configuration().Servers {
		out = append(out, Member{
			ID:      string(s.ID),
			Address: string(s.Address),
			Voter:   s.Suffrage == raft.Voter,
		})
	}
	return out, nil
}

func (c *Cluster) Quorum() int {
	m, err := c.Members()
	if err != nil {
		return 0
	}
	return quorum.Size(len(m))
}

// JoinExisting contacts an existing node and asks to be admitted.
// The actual gRPC client is injected via SetJoinHook to avoid an import cycle.
func (c *Cluster) JoinExisting(ctx context.Context, addr, token string) error {
	if addr == "" {
		return errors.New("empty join address")
	}
	if c.joinHook == nil {
		return errors.New("cluster join hook not wired")
	}
	return c.joinHook(ctx, addr, token)
}

func (c *Cluster) SetJoinHook(h JoinHook) { c.joinHook = h }

func (c *Cluster) Raft() *raft.Raft { return c.raft }

// ObserveLeaderChanges emits the address of each newly elected leader.
func (c *Cluster) ObserveLeaderChanges(ctx context.Context) <-chan string {
	out := make(chan string, 8)
	ch := make(chan raft.Observation, 8)
	obs := raft.NewObserver(ch, false, func(o *raft.Observation) bool {
		_, ok := o.Data.(raft.LeaderObservation)
		return ok
	})
	c.raft.RegisterObserver(obs)
	go func() {
		defer c.raft.DeregisterObserver(obs)
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case o, ok := <-ch:
				if !ok {
					return
				}
				if lo, isLO := o.Data.(raft.LeaderObservation); isLO {
					select {
					case out <- string(lo.LeaderAddr):
					default:
					}
				}
			}
		}
	}()
	return out
}

type Member struct {
	ID      string
	Address string
	Voter   bool
}

var ErrNotLeader = errors.New("not leader")
