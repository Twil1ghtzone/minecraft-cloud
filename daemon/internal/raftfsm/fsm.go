// Package raftfsm implements the Raft finite state machine for AetherNet.
// All mutations to cluster-wide state flow through Apply() so that every
// node holds an identical, deterministic copy.
package raftfsm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aethernet/aethernet/pkg/types"
	"github.com/hashicorp/raft"
)

// FSM is the in-memory state machine. It is goroutine-safe.
type FSM struct {
	mu sync.RWMutex

	persistDir string

	nodes     map[string]*types.Node
	servers   map[string]*types.Server
	groups    map[string]*types.Group
	templates map[string]*types.Template
	databases map[string]*types.Database
	tokens    map[string]types.Token
	idem      map[string]int64 // idempotency key -> raft index

	subscribers []chan Event
	subMu       sync.Mutex
}

// types.Token is used from pkg/types

// Event is broadcast to subscribers whenever the FSM state changes.
type Event struct {
	Kind    string // matches CommandType
	Payload any    // typed copy of what was applied
}

func NewFSM(persistDir string) (*FSM, error) {
	if err := os.MkdirAll(persistDir, 0o755); err != nil {
		return nil, err
	}
	f := &FSM{
		persistDir: persistDir,
		nodes:      map[string]*types.Node{},
		servers:    map[string]*types.Server{},
		groups:     map[string]*types.Group{},
		templates:  map[string]*types.Template{},
		databases:  map[string]*types.Database{},
		tokens:     map[string]types.Token{},
		idem:       map[string]int64{},
	}
	return f, nil
}

// Subscribe returns a channel of FSM events. The channel is closed when
// the caller drops it; if the channel buffer fills up the event is
// dropped to avoid backpressuring the Raft apply loop.
func (f *FSM) Subscribe(buf int) chan Event {
	ch := make(chan Event, buf)
	f.subMu.Lock()
	f.subscribers = append(f.subscribers, ch)
	f.subMu.Unlock()
	return ch
}

func (f *FSM) publish(kind string, payload any) {
	f.subMu.Lock()
	subs := append([]chan Event(nil), f.subscribers...)
	f.subMu.Unlock()
	ev := Event{Kind: kind, Payload: payload}
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// subscriber too slow, drop.
		}
	}
}

// Apply implements raft.FSM. It must be deterministic.
func (f *FSM) Apply(log *raft.Log) any {
	cmd, err := Decode(log.Data)
	if err != nil {
		return fmt.Errorf("decode command: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	// idempotency
	if cmd.Idem != "" {
		if _, seen := f.idem[cmd.Idem]; seen {
			return nil // already applied
		}
		f.idem[cmd.Idem] = int64(log.Index)
	}

	switch cmd.Type {
	case CmdNodeUpsert, CmdHeartbeat:
		var n types.Node
		if err := json.Unmarshal(cmd.Payload, &n); err != nil {
			return err
		}
		f.nodes[n.ID] = &n
		f.publish(string(cmd.Type), n)
	case CmdNodeRemove:
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		delete(f.nodes, p.ID)
		f.publish(string(cmd.Type), p)
	case CmdServerUpsert:
		var s types.Server
		if err := json.Unmarshal(cmd.Payload, &s); err != nil {
			return err
		}
		f.servers[s.Spec.ID] = &s
		f.publish(string(cmd.Type), s)
	case CmdServerRemove:
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		delete(f.servers, p.ID)
		f.publish(string(cmd.Type), p)
	case CmdServerSetState:
		var p struct {
			ID    string            `json:"id"`
			State types.ServerState `json:"state"`
		}
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		if srv, ok := f.servers[p.ID]; ok {
			srv.State = p.State
			f.publish(string(cmd.Type), *srv)
		}
	case CmdServerSetHost:
		var p struct {
			ID       string `json:"id"`
			NodeID   string `json:"node_id"`
			ContainerID string `json:"container_id"`
			HostPort uint32 `json:"host_port"`
		}
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		if srv, ok := f.servers[p.ID]; ok {
			srv.NodeID = p.NodeID
			srv.ContainerID = p.ContainerID
			srv.HostPort = p.HostPort
			f.publish(string(cmd.Type), *srv)
		}
	case CmdGroupUpsert:
		var g types.Group
		if err := json.Unmarshal(cmd.Payload, &g); err != nil {
			return err
		}
		f.groups[g.ID] = &g
		f.publish(string(cmd.Type), g)
	case CmdGroupRemove:
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		delete(f.groups, p.ID)
		f.publish(string(cmd.Type), p)
	case CmdTemplateUpsert:
		var t types.Template
		if err := json.Unmarshal(cmd.Payload, &t); err != nil {
			return err
		}
		f.templates[t.ID] = &t
		f.publish(string(cmd.Type), t)
	case CmdTemplateRemove:
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		delete(f.templates, p.ID)
		f.publish(string(cmd.Type), p)
	case CmdDatabaseUpsert:
		var d types.Database
		if err := json.Unmarshal(cmd.Payload, &d); err != nil {
			return err
		}
		f.databases[d.ID] = &d
		f.publish(string(cmd.Type), d)
	case CmdDatabaseRemove:
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		delete(f.databases, p.ID)
		f.publish(string(cmd.Type), p)
	case CmdTokenUpsert:
		var t types.Token
		if err := json.Unmarshal(cmd.Payload, &t); err != nil {
			return err
		}
		f.tokens[t.ID] = t
	case CmdTokenRemove:
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(cmd.Payload, &p); err != nil {
			return err
		}
		delete(f.tokens, p.ID)
	default:
		return fmt.Errorf("unknown command type: %s", cmd.Type)
	}
	return nil
}

// Snapshot serializes the FSM for log compaction.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	snap := &snapshot{
		Nodes:     copyMap(f.nodes),
		Servers:   copyMap(f.servers),
		Groups:    copyMap(f.groups),
		Templates: copyMap(f.templates),
		Databases: copyMap(f.databases),
		Tokens:    copyMapByValue(f.tokens),
		Idem:      copyMapByValue(f.idem),
		Taken:     time.Now().UnixNano(),
	}
	return snap, nil
}

func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var snap snapshot
	if err := json.NewDecoder(rc).Decode(&snap); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes = snap.Nodes
	f.servers = snap.Servers
	f.groups = snap.Groups
	f.templates = snap.Templates
	f.databases = snap.Databases
	f.tokens = snap.Tokens
	f.idem = snap.Idem
	return nil
}

// ---- read accessors ----------------------------------------------------------

func (f *FSM) Node(id string) (types.Node, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	n, ok := f.nodes[id]
	if !ok {
		return types.Node{}, false
	}
	return *n, true
}

func (f *FSM) Nodes() []types.Node {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]types.Node, 0, len(f.nodes))
	for _, n := range f.nodes {
		out = append(out, *n)
	}
	return out
}

func (f *FSM) Server(id string) (types.Server, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s, ok := f.servers[id]
	if !ok {
		return types.Server{}, false
	}
	return *s, true
}

func (f *FSM) Servers() []types.Server {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]types.Server, 0, len(f.servers))
	for _, s := range f.servers {
		out = append(out, *s)
	}
	return out
}

func (f *FSM) ServersOnNode(nodeID string) []types.Server {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []types.Server
	for _, s := range f.servers {
		if s.NodeID == nodeID {
			out = append(out, *s)
		}
	}
	return out
}

func (f *FSM) Groups() []types.Group {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]types.Group, 0, len(f.groups))
	for _, g := range f.groups {
		out = append(out, *g)
	}
	return out
}

func (f *FSM) Template(id string) (types.Template, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	t, ok := f.templates[id]
	if !ok {
		return types.Template{}, false
	}
	return *t, true
}

func (f *FSM) Databases() []types.Database {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]types.Database, 0, len(f.databases))
	for _, d := range f.databases {
		out = append(out, *d)
	}
	return out
}

func (f *FSM) Token(id string) (types.Token, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	t, ok := f.tokens[id]
	return t, ok
}

func (f *FSM) Tokens() []types.Token {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]types.Token, 0, len(f.tokens))
	for _, t := range f.tokens {
		out = append(out, t)
	}
	return out
}

// Persist a side-cache of the current snapshot for fast cold-starts.
func (f *FSM) FlushCache() error {
	snap, err := f.Snapshot()
	if err != nil {
		return err
	}
	tmp := filepath.Join(f.persistDir, "fsm.snap.tmp")
	final := filepath.Join(f.persistDir, "fsm.snap")
	fp, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := snap.(*snapshot).Persist(fileSink{fp}); err != nil {
		fp.Close()
		os.Remove(tmp)
		return err
	}
	if err := fp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// fileSink adapts *os.File to raft.SnapshotSink.
type fileSink struct{ *os.File }

func (fileSink) ID() string             { return "local" }
func (f fileSink) Cancel() error        { return f.File.Close() }
func (f fileSink) Write(b []byte) (int, error) { return f.File.Write(b) }
func (f fileSink) Close() error         { return f.File.Close() }

// ---- snapshot impl -----------------------------------------------------------

type snapshot struct {
	Nodes     map[string]*types.Node     `json:"nodes"`
	Servers   map[string]*types.Server   `json:"servers"`
	Groups    map[string]*types.Group    `json:"groups"`
	Templates map[string]*types.Template `json:"templates"`
	Databases map[string]*types.Database `json:"databases"`
	Tokens    map[string]types.Token     `json:"tokens"`
	Idem      map[string]int64           `json:"idem"`
	Taken     int64                      `json:"taken"`
}

func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	enc := json.NewEncoder(sink)
	if err := enc.Encode(s); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *snapshot) Release() {}

func copyMap[V any](m map[string]*V) map[string]*V {
	out := make(map[string]*V, len(m))
	for k, v := range m {
		cp := *v
		out[k] = &cp
	}
	return out
}

func copyMapByValue[V any](m map[string]V) map[string]V {
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ErrNotFound is returned by helpers when a record does not exist.
var ErrNotFound = errors.New("not found")
