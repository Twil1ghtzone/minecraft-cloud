// Package api — rbac.go
// RBAC token authentication middleware and REST endpoints (Module 3).
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/pkg/types"
)

func registerRBACRoutes(mux *http.ServeMux, o Options) {
	mux.HandleFunc("/api/v1/tokens", handleTokens(o))
	mux.HandleFunc("/api/v1/tokens/", handleOneToken(o))
}

func handleTokens(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// List tokens (excluding sensitive hashes)
			tokens := o.FSM.Tokens()
			writeJSON(w, 200, map[string]any{"tokens": tokens})

		case http.MethodPost:
			// Create token
			var body struct {
				Description string   `json:"description"`
				Scopes      []string `json:"scopes"`
				ExpiresIn   int64    `json:"expires_in"` // seconds
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				httpError(w, 400, "invalid body")
				return
			}

			raw := generateSecureToken()
			id := tokenID(raw)
			hash := tokenHash(raw)

			var expiresAt time.Time
			if body.ExpiresIn > 0 {
				expiresAt = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
			}

			token := types.Token{
				ID:          id,
				Hash:        hash,
				Description: body.Description,
				Scopes:      body.Scopes,
				CreatedAt:   time.Now(),
				ExpiresAt:   expiresAt,
			}

			data, err := raftfsm.Encode(raftfsm.CmdTokenUpsert, token, "", time.Now().UnixNano())
			if err != nil {
				httpError(w, 500, err.Error())
				return
			}

			if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
				httpError(w, 500, err.Error())
				return
			}

			// Return raw token only once on creation
			writeJSON(w, 201, map[string]any{
				"token":    raw,
				"metadata": token,
			})

		default:
			httpError(w, 405, "method not allowed")
		}
	}
}

func handleOneToken(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/tokens/")
		if id == "" {
			httpError(w, 404, "not found")
			return
		}

		if r.Method != http.MethodDelete {
			httpError(w, 405, "method not allowed")
			return
		}

		data, err := raftfsm.Encode(raftfsm.CmdTokenRemove, map[string]string{"id": id}, "", time.Now().UnixNano())
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}

		if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
			httpError(w, 500, err.Error())
			return
		}

		writeJSON(w, 200, map[string]any{"ok": true})
	}
}

// RBACMiddleware checks Bearer Token and verifies required scopes
func RBACMiddleware(next http.Handler, o Options, requiredScope string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow health/readiness probes without auth
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		// Also allow panel web assets (if served by API)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			httpError(w, 401, "authorization header missing")
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			httpError(w, 401, "invalid authorization header format")
			return
		}

		rawToken := parts[1]
		hash := tokenHash(rawToken)

		// Search token in FSM
		tokens := o.FSM.Tokens()
		var foundToken *types.Token
		for _, t := range tokens {
			if t.Hash == hash {
				foundToken = &t
				break
			}
		}

		if foundToken == nil {
			httpError(w, 401, "unauthorized: invalid token")
			return
		}

		// Check expiry
		if !foundToken.ExpiresAt.IsZero() && time.Now().After(foundToken.ExpiresAt) {
			httpError(w, 401, "unauthorized: token expired")
			return
		}

		// Check scope
		if requiredScope != "" && !hasScope(foundToken.Scopes, requiredScope) {
			httpError(w, 403, "forbidden: insufficient permissions")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func hasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == "admin" || s == required {
			return true
		}
	}
	return false
}
