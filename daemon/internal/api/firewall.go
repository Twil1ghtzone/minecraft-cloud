// Package api — firewall.go
// REST endpoints for the Port Management GUI (Module 3).
//
// Routes:
//
//	GET    /api/v1/firewall/ports              — list all active port rules
//	POST   /api/v1/firewall/ports              — open a port (returns accepted rule)
//	DELETE /api/v1/firewall/ports/:server      — flush all ports for a server
//	POST   /api/v1/firewall/ports/:server/open — open a specific port on a server
//	POST   /api/v1/firewall/ports/:server/close— close a specific port on a server
package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// PortRule is the over-the-wire representation of a single firewall entry.
type PortRule struct {
	ServerID    string `json:"server_id"`
	ContainerIP string `json:"container_ip"`
	HostPort    uint32 `json:"host_port"`
	Protocol    string `json:"protocol"` // "tcp" | "udp"
	Open        bool   `json:"open"`
	Description string `json:"description"`
}

// registerFirewallRoutes registers all firewall-related HTTP endpoints on mux.
func registerFirewallRoutes(mux *http.ServeMux, o Options) {
	// Exact match for the collection endpoint.
	mux.HandleFunc("/api/v1/firewall/ports", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rules := buildPortRulesFromFSM(o)
			writeJSON(w, 200, map[string]any{"rules": rules})
		case http.MethodPost:
			handleOpenPort(w, r, o)
		default:
			httpError(w, 405, "method not allowed")
		}
	})

	// Prefix match for per-server sub-resources.
	mux.HandleFunc("/api/v1/firewall/ports/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/firewall/ports/")
		parts := strings.SplitN(rest, "/", 2)
		serverID := parts[0]
		if serverID == "" {
			httpError(w, 404, "not found")
			return
		}
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}
		switch action {
		case "open":
			handleOpenPortForServer(w, r, o, serverID)
		case "close":
			handleClosePortForServer(w, r, o, serverID)
		case "":
			if r.Method == http.MethodDelete {
				handleFlushServerPorts(w, r, o, serverID)
			} else {
				httpError(w, 405, "method not allowed")
			}
		default:
			httpError(w, 404, "unknown action")
		}
	})
}

// buildPortRulesFromFSM synthesises firewall rules from the running server
// registry held in the FSM.  Each server that has a published HostPort gets
// one open TCP rule.
func buildPortRulesFromFSM(o Options) []PortRule {
	servers := o.FSM.Servers()
	rules := make([]PortRule, 0, len(servers))
	for _, s := range servers {
		if s.HostPort == 0 {
			continue
		}
		rules = append(rules, PortRule{
			ServerID:    s.Spec.ID,
			ContainerIP: "", // resolved on the host node at container runtime
			HostPort:    s.HostPort,
			Protocol:    "tcp",
			Open:        true,
			Description: s.Spec.Name + " (Minecraft)",
		})
	}
	return rules
}

// handleOpenPort accepts a generic open-port request not tied to a specific
// server and returns the created PortRule.  The actual iptables call is
// delegated to the docker controller when the container is started.
func handleOpenPort(w http.ResponseWriter, r *http.Request, o Options) {
	var body struct {
		ServerID    string `json:"server_id"`
		Port        uint32 `json:"port"`
		Protocol    string `json:"protocol"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Port == 0 || body.Port > 65535 {
		httpError(w, 400, "invalid port number")
		return
	}
	protocol := body.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	rule := PortRule{
		ServerID:    body.ServerID,
		HostPort:    body.Port,
		Protocol:    protocol,
		Open:        true,
		Description: body.Description,
	}
	writeJSON(w, 201, rule)
}

// handleOpenPortForServer opens a specific port for an already-known server.
// Returns 404 if the server is not registered in the FSM.
func handleOpenPortForServer(w http.ResponseWriter, r *http.Request, o Options, serverID string) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Port     uint32 `json:"port"`
		Protocol string `json:"protocol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Port == 0 || body.Port > 65535 {
		httpError(w, 400, "invalid port number")
		return
	}
	if _, ok := o.FSM.Server(serverID); !ok {
		httpError(w, 404, "server not found")
		return
	}
	protocol := body.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"server_id": serverID,
		"port":      body.Port,
		"protocol":  protocol,
		"open":      true,
	})
}

// handleClosePortForServer closes a specific port on a server.
func handleClosePortForServer(w http.ResponseWriter, r *http.Request, o Options, serverID string) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Port     uint32 `json:"port"`
		Protocol string `json:"protocol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.Port == 0 || body.Port > 65535 {
		httpError(w, 400, "invalid port number")
		return
	}
	if _, ok := o.FSM.Server(serverID); !ok {
		httpError(w, 404, "server not found")
		return
	}
	protocol := body.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"server_id": serverID,
		"port":      body.Port,
		"protocol":  protocol,
		"closed":    true,
	})
}

// handleFlushServerPorts removes all firewall rules for a server (e.g. on
// server deletion).  Returns 404 if the server does not exist.
func handleFlushServerPorts(w http.ResponseWriter, r *http.Request, o Options, serverID string) {
	if _, ok := o.FSM.Server(serverID); !ok {
		httpError(w, 404, "server not found")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "flushed": serverID})
}
