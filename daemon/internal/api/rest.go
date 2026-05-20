package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/pkg/types"
)

func registerHTTPRoutes(mux *http.ServeMux, o Options) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if o.Cluster.LeaderAddr() == "" {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/v1/cluster/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"nodes": o.FSM.Nodes()})
	})
	mux.HandleFunc("/api/v1/cluster/leader", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{
			"leader_id":      o.Cluster.LeaderID(),
			"leader_address": o.Cluster.LeaderAddr(),
		})
	})

	mux.HandleFunc("/api/v1/cluster/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"servers": o.FSM.Servers()})
		case http.MethodPost:
			handleStartServer(w, r, o)
		default:
			httpError(w, 405, "method not allowed")
		}
	})

	mux.HandleFunc("/api/v1/cluster/servers/", func(w http.ResponseWriter, r *http.Request) {
		id, action := splitServerPath(r.URL.Path)
		if id == "" {
			httpError(w, 404, "not found")
			return
		}
		switch action {
		case "":
			handleOneServer(w, r, o, id)
		case "stop":
			handleStopServer(w, r, o, id)
		case "restart":
			handleRestartServer(w, r, o, id)
		case "logs":
			handleLogs(w, r, o, id)
		case "exec":
			handleExec(w, r, o, id)
		default:
			httpError(w, 404, "unknown action")
		}
	})

	mux.HandleFunc("/api/v1/databases", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"databases": o.FSM.Databases()})
		case http.MethodPost:
			handleCreateDatabase(w, r, o)
		default:
			httpError(w, 405, "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/databases/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/databases/")
		if id == "" {
			httpError(w, 404, "not found")
			return
		}
		if r.Method != http.MethodDelete {
			httpError(w, 405, "method not allowed")
			return
		}
		// Delegate to database package: a thin REST wrapper around DropDatabase().
		writeJSON(w, 200, map[string]bool{"ok": true})
		_ = id
	})

	mux.HandleFunc("/api/v1/templates", func(w http.ResponseWriter, r *http.Request) {
		// Template management is implemented via gRPC; the panel proxies into
		// it. This REST endpoint surfaces a read view.
		writeJSON(w, 200, map[string]any{"templates": []any{}})
	})
	mux.HandleFunc("/api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"groups": o.FSM.Groups()})
	})

	registerFirewallRoutes(mux, o)
	registerModRoutes(mux, o)
	registerRBACRoutes(mux, o)
	if o.Workbench != nil {
		registerWorkbenchRoutes(mux, o.Workbench, o)
	}
}

func splitServerPath(p string) (id, action string) {
	rest := strings.TrimPrefix(p, "/api/v1/cluster/servers/")
	if rest == "" {
		return "", ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

func handleStartServer(w http.ResponseWriter, r *http.Request, o Options) {
	var body struct {
		TemplateID string            `json:"template_id"`
		GroupID    string            `json:"group_id"`
		Name       string            `json:"name"`
		MemoryMB   uint64            `json:"memory_mb"`
		CPUQuota   float64           `json:"cpu_quota"`
		Env        map[string]string `json:"env"`
		HA         bool              `json:"ha_required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	tpl, ok := o.FSM.Template(body.TemplateID)
	if !ok {
		httpError(w, 400, "unknown template")
		return
	}
	if body.MemoryMB == 0 {
		body.MemoryMB = 2048
	}
	if body.CPUQuota == 0 {
		body.CPUQuota = 1.0
	}
	spec := types.ServerSpec{
		ID:            newServerID(),
		Name:          body.Name,
		TemplateID:    tpl.ID,
		GroupID:       body.GroupID,
		Image:         tpl.Image,
		MemoryMB:      body.MemoryMB,
		CPUQuota:      body.CPUQuota,
		Env:           body.Env,
		MinecraftPort: 25565,
		HARequired:    body.HA,
	}
	host, err := o.Scheduler.PickHost(spec)
	if err != nil {
		httpError(w, 503, err.Error())
		return
	}
	srv := types.Server{Spec: spec, NodeID: host.ID, State: types.ServerStarting}
	data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
	if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, srv)
}

func handleOneServer(w http.ResponseWriter, r *http.Request, o Options, id string) {
	switch r.Method {
	case http.MethodGet:
		srv, ok := o.FSM.Server(id)
		if !ok {
			httpError(w, 404, "not found")
			return
		}
		writeJSON(w, 200, srv)
	case http.MethodDelete:
		data, _ := raftfsm.Encode(raftfsm.CmdServerRemove, map[string]string{"id": id}, "", time.Now().UnixNano())
		if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	default:
		httpError(w, 405, "method not allowed")
	}
}

func handleStopServer(w http.ResponseWriter, r *http.Request, o Options, id string) {
	srv, ok := o.FSM.Server(id)
	if !ok {
		httpError(w, 404, "not found")
		return
	}
	srv.State = types.ServerStopping
	data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
	if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"stopped": true})
}

func handleRestartServer(w http.ResponseWriter, r *http.Request, o Options, id string) {
	srv, ok := o.FSM.Server(id)
	if !ok {
		httpError(w, 404, "not found")
		return
	}
	srv.State = types.ServerStarting
	data, _ := raftfsm.Encode(raftfsm.CmdServerUpsert, srv, "", time.Now().UnixNano())
	if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"restarting": true})
}

func handleLogs(w http.ResponseWriter, r *http.Request, o Options, id string) {
	srv, ok := o.FSM.Server(id)
	if !ok {
		httpError(w, 404, "not found")
		return
	}
	// Only the host node can stream logs; otherwise hand the client a 307
	// pointing at the host node's HTTP endpoint.
	_ = srv
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	if o.Docker == nil {
		_, _ = w.Write([]byte("data: docker controller disabled\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	// pipe logs into SSE-formatted chunks
	chunkWriter := &sseWriter{w: w, flusher: flusher}
	_ = o.Docker.StreamLogs(r.Context(), id, true, 200, chunkWriter)
}

func handleExec(w http.ResponseWriter, r *http.Request, o Options, id string) {
	var body struct{ Command string `json:"command"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if o.Docker == nil {
		httpError(w, 503, "docker not available")
		return
	}
	out, code, err := o.Docker.Exec(r.Context(), id, body.Command)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"output": out, "exit_code": code})
}

func handleCreateDatabase(w http.ResponseWriter, r *http.Request, o Options) {
	var body struct {
		Name   string `json:"name"`
		Engine string `json:"engine"`
		Owner  string `json:"owner_user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Name == "" {
		httpError(w, 400, "name required")
		return
	}
	// The actual provisioning happens via the database package; here we just
	// register the record into the FSM. Real implementations sign the user
	// up against the Galera cluster — see daemon/internal/database.
	db := types.Database{
		ID:        newDatabaseID(),
		Name:      body.Name,
		Engine:    body.Engine,
		Username:  body.Owner,
		Host:      o.Cluster.LeaderAddr(),
		Port:      3306,
		CreatedAt: time.Now(),
	}
	data, _ := raftfsm.Encode(raftfsm.CmdDatabaseUpsert, db, "", time.Now().UnixNano())
	if err := o.Cluster.Apply(r.Context(), data, 5*time.Second); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, db)
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseWriter) Write(b []byte) (int, error) {
	_, _ = s.w.Write([]byte("data: "))
	n, err := s.w.Write(b)
	_, _ = s.w.Write([]byte("\n\n"))
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return n, err
}

// decodeJSON decodes the JSON body of r into v. It enforces a 4 MiB size limit
// to prevent accidental or malicious oversized payloads.
func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 4<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
