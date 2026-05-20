// Package cluster — quorum_guard.go
// QuorumGuard is an HTTP middleware that rejects state-mutating requests
// (POST, PUT, DELETE, PATCH) when the cluster is below quorum.
// It also provides the BelowQuorum() method used by Apply().
package cluster

import (
	"encoding/json"
	"net/http"
	"strings"
)

// BelowQuorum reports true when fewer than Q = floor(N/2)+1 nodes are Up.
// The cluster enforces this by counting nodes with state == NodeUp in the FSM.
// This is conservative: nodes in Suspect state are NOT counted.
func (c *Cluster) BelowQuorum(upCount int) bool {
	members, err := c.Members()
	if err != nil {
		// If we can't read members, assume split-brain.
		return true
	}
	q := c.Quorum()
	return upCount < q || len(members) < q
}

// QuorumGuardMiddleware wraps an HTTP handler and rejects mutations when
// the cluster is below quorum. It gets the current up-node count via a
// callback to avoid importing the FSM package (avoids circular deps).
func QuorumGuardMiddleware(next http.Handler, upCount func() int, c *Cluster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutating := r.Method == http.MethodPost ||
			r.Method == http.MethodPut ||
			r.Method == http.MethodDelete ||
			r.Method == http.MethodPatch

		if mutating && !strings.HasPrefix(r.URL.Path, "/healthz") && !strings.HasPrefix(r.URL.Path, "/readyz") {
			if c.BelowQuorum(upCount()) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-AetherNet-Quorum", "false")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":   "cluster below quorum — node in read-only safety mode",
					"quorum":  c.Quorum(),
					"members": func() int { m, _ := c.Members(); return len(m) }(),
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
