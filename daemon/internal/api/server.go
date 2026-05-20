// Package api hosts both the gRPC services and the REST HTTP server.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/cluster"
	"github.com/aethernet/aethernet/daemon/internal/database"
	"github.com/aethernet/aethernet/daemon/internal/docker"
	"github.com/aethernet/aethernet/daemon/internal/firewall"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/daemon/internal/scheduler"
	"google.golang.org/grpc"
)

type Options struct {
	GRPCListen string
	HTTPListen string
	Cluster    *cluster.Cluster
	FSM        *raftfsm.FSM
	Scheduler  *scheduler.Scheduler
	Docker     *docker.Controller
	Firewall   *firewall.Manager
	Workbench  *database.Workbench
	Logger     *slog.Logger
}

type Server struct {
	opts Options
	grpc *grpc.Server
	http *http.Server

	closeOnce sync.Once
}

func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s := &Server{opts: o}
	return s
}

func (s *Server) Serve(ctx context.Context) error {
	gln, err := net.Listen("tcp", s.opts.GRPCListen)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	hln, err := net.Listen("tcp", s.opts.HTTPListen)
	if err != nil {
		_ = gln.Close()
		return fmt.Errorf("http listen: %w", err)
	}

	s.grpc = grpc.NewServer()
	registerGRPCServices(s.grpc, s.opts)

	mux := http.NewServeMux()
	registerHTTPRoutes(mux, s.opts)
	s.http = &http.Server{
		Handler:           withAuth(mux, s.opts),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Wire cluster join hook to a gRPC client so cluster.JoinExisting() works.
	s.opts.Cluster.SetJoinHook(func(jctx context.Context, addr, token string) error {
		return grpcJoin(jctx, addr, token, s.opts)
	})

	errCh := make(chan error, 2)
	go func() { errCh <- s.grpc.Serve(gln) }()
	go func() { errCh <- s.http.Serve(hln) }()

	select {
	case <-ctx.Done():
		s.shutdown()
		return ctx.Err()
	case err := <-errCh:
		s.shutdown()
		return err
	}
}

func (s *Server) Shutdown(ctx context.Context) {
	s.shutdown()
}

func (s *Server) shutdown() {
	s.closeOnce.Do(func() {
		if s.grpc != nil {
			s.grpc.GracefulStop()
		}
		if s.http != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.http.Shutdown(ctx)
		}
	})
}

// ----- auth middleware ------------------------------------------------------

// withAuth wraps the mux with bearer-token authentication. Routes under
// /api/v1 require a valid token; /healthz and /readyz are public.
func withAuth(next http.Handler, o Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, "Bearer ") {
			httpError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		raw := strings.TrimPrefix(authz, "Bearer ")
		hash := sha256.Sum256([]byte(raw))
		id := hex.EncodeToString(hash[:8])
		tok, ok := o.FSM.Token(id)
		if !ok {
			httpError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if !tok.ExpiresAt.IsZero() && time.Now().After(tok.ExpiresAt) {
			httpError(w, http.StatusUnauthorized, "token expired")
			return
		}
		required := scopeForPath(r.Method, r.URL.Path)
		if required != "" && !hasScope(tok.Scopes, required) {
			httpError(w, http.StatusForbidden, "missing scope: "+required)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func scopeForPath(method, path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/cluster/nodes"), strings.HasPrefix(path, "/api/v1/cluster/leader"):
		return "read:cluster"
	case strings.HasPrefix(path, "/api/v1/cluster/servers"):
		if method == http.MethodGet {
			return "read:servers"
		}
		return "write:servers"
	case strings.HasPrefix(path, "/api/v1/databases"):
		if method == http.MethodGet {
			return "read:databases"
		}
		return "write:databases"
	case strings.HasPrefix(path, "/api/v1/templates"), strings.HasPrefix(path, "/api/v1/groups"):
		if method == http.MethodGet {
			return "read:cluster"
		}
		return "write:cluster"
	}
	return ""
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want || s == "admin:*" {
			return true
		}
	}
	return false
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		// Best-effort.
	}
}
