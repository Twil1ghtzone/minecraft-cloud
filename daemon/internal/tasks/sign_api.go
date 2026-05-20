package tasks

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/redis/go-redis/v9"
)

// SignAPI publishes the in-game sign / tablist state to all proxies.
//
// Each server in the cluster periodically broadcasts a SignState struct
// onto the Redis pub/sub channel "aethernet.signs". The Velocity bridge
// (Module 4) subscribes to the same channel and renders the data on
// physical signs and the tablist.
type SignAPI struct {
	rdb     *redis.ClusterClient
	channel string

	mu   sync.Mutex
	last map[string]SignState
}

type SignState struct {
	ServerID    string `json:"server_id"`
	GroupID     string `json:"group_id"`
	State       string `json:"state"`
	PlayerCount uint32 `json:"player_count"`
	MaxPlayers  uint32 `json:"max_players"`
	MOTD        string `json:"motd"`
}

func NewSignAPI(rdb *redis.ClusterClient) *SignAPI {
	return &SignAPI{rdb: rdb, channel: "aethernet.signs", last: map[string]SignState{}}
}

func (s *SignAPI) Publish(ctx context.Context, st SignState) error {
	s.mu.Lock()
	prev, ok := s.last[st.ServerID]
	s.last[st.ServerID] = st
	s.mu.Unlock()
	if ok && prev == st {
		return nil // deduplicate
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.rdb.Publish(ctx, s.channel, b).Err()
}

// Snapshot returns the current state for every known server.
func (s *SignAPI) Snapshot() []SignState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SignState, 0, len(s.last))
	for _, v := range s.last {
		out = append(out, v)
	}
	return out
}
