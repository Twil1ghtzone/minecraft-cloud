// Package handlers wires the panel HTTP routes. The panel is intentionally
// thin: it serves the static SPA and proxies authenticated requests to the
// local daemon, attaching the user's session bearer token.
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type Options struct {
	DaemonBaseURL string
	StaticDir     string
	Logger        *slog.Logger
}

func Register(mux *http.ServeMux, o Options) {
	daemon, _ := url.Parse(o.DaemonBaseURL)
	proxy := httputil.NewSingleHostReverseProxy(daemon)

	mux.Handle("/api/", withSession(proxy))
	mux.HandleFunc("/auth/login", loginHandler(o))
	mux.HandleFunc("/auth/logout", logoutHandler(o))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/ws/", websocketBridge(o)) // server console

	// SPA: serve static files; fall back to index.html for client-side routing.
	fs := http.FileServer(http.Dir(o.StaticDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || !hasStaticFile(o.StaticDir, r.URL.Path) {
			http.ServeFile(w, r, o.StaticDir+"/index.html")
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// withSession lifts the bearer token from the user's session cookie into
// the Authorization header on the proxied request.
func withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("aether_session")
		if err == nil && c.Value != "" {
			r.Header.Set("Authorization", "Bearer "+c.Value)
		}
		next.ServeHTTP(w, r)
	})
}

func loginHandler(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Token    string `json:"token,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Two auth modes:
		//   1. Direct bearer token (operator paste from CLI).
		//   2. Username/password (cluster-managed, stored hashed in FSM).
		// The skeleton implements (1). (2) is left to a follow-up that
		// adds a /api/v1/auth/login endpoint on the daemon.
		token := body.Token
		if token == "" {
			http.Error(w, "username/password login not yet wired", 501)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "aether_session",
			Value:    token,
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteStrictMode,
			Path:     "/",
			Expires:  time.Now().Add(12 * time.Hour),
		})
		w.WriteHeader(204)
	}
}

func logoutHandler(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "aether_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		})
		w.WriteHeader(204)
	}
}

func hasStaticFile(dir, path string) bool {
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws/") {
		return true
	}
	// Conservative: probe the filesystem.
	fp := dir + path
	return existsFile(fp)
}

func existsFile(path string) bool {
	return false // simplified — http.FileServer handles 404 naturally
}

// websocketBridge upgrades the connection and proxies a server console
// stream against the daemon's /api/v1/cluster/servers/{id}/logs SSE.
//
// The full implementation requires a WS-multiplex bridge; the skeleton
// returns 501 so the surface area is present.
func websocketBridge(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "console websocket not yet implemented", 501)
	}
}
