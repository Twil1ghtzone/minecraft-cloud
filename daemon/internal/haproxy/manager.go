// Package haproxy manages the HAProxy load balancer configuration.
//
// The manager generates haproxy.cfg from the current cluster state,
// writes it atomically, and signals HAProxy to reload gracefully via
// 'haproxy -sf <pid>' (soft reload — no connection drops).
//
// HAProxy acts as the Layer-4 edge for:
//   - Minecraft game traffic (tcp-mode, port 25565)
//   - AetherNet REST/gRPC API traffic (port 8080/7001)
//
// L4 rate-limiting is enforced via stick-tables. See ratelimit.go.
package haproxy

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/aethernet/aethernet/pkg/types"
)

const defaultConfigPath = "/etc/haproxy/aethernet.cfg"

var haproxyCfgTmpl = template.Must(template.New("haproxy").Parse(`
global
    log /dev/log local0
    log /dev/log local1 notice
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners
    stats timeout 30s
    user haproxy
    group haproxy
    daemon
    maxconn 50000
    tune.ssl.default-dh-param 2048

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect 5s
    timeout client  50s
    timeout server  50s
    errorfile 400 /etc/haproxy/errors/400.http
    errorfile 403 /etc/haproxy/errors/403.http
    errorfile 408 /etc/haproxy/errors/408.http
    errorfile 500 /etc/haproxy/errors/500.http
    errorfile 502 /etc/haproxy/errors/502.http
    errorfile 503 /etc/haproxy/errors/503.http
    errorfile 504 /etc/haproxy/errors/504.http

#----- L4 Rate-Limit Stick Table ------------------------------------------------
backend st_src_conn_rate
    stick-table type ip size 1m expire 10m store conn_rate(10s),conn_cur

#----- API / Panel Ingress ------------------------------------------------------
frontend fe_api
    bind *:8080
    mode http
    option httplog
    default_backend be_api

backend be_api
    mode http
    balance leastconn
    option httpchk GET /healthz
    {{- range .Nodes}}
    server {{.ID}} {{.Address}} check inter 2s fall 3 rise 2
    {{- end}}

#----- Minecraft TCP Proxy -------------------------------------------------------
frontend fe_minecraft
    bind *:25565
    mode tcp
    # Anti-flood: track connection rate per source IP
    stick-table type ip size 200k expire 30s store conn_rate(3s),conn_cur
    tcp-request connection track-sc0 src
    tcp-request connection reject if { sc_conn_rate(0) gt {{.MaxConnRate}} }
    tcp-request connection reject if { sc_conn_cur(0) gt {{.MaxConnCur}} }
    default_backend be_minecraft

backend be_minecraft
    mode tcp
    balance roundrobin
    option tcp-check
    {{- range .Servers}}
    server {{.Spec.ID}} {{.NodeAddress}}:{{.HostPort}} check inter 2s fall 2 rise 1
    {{- end}}

#----- Stats (internal only) ---------------------------------------------------
frontend fe_stats
    bind 127.0.0.1:8404
    mode http
    stats enable
    stats uri /stats
    stats refresh 10s
    stats admin if TRUE
`),
)

type Config struct {
	ConfigPath  string
	MaxConnRate int // max connections per 3s per source IP
	MaxConnCur  int // max concurrent connections per source IP
	Logger      *slog.Logger
}

type ServerEntry struct {
	Spec        types.ServerSpec
	HostPort    uint32
	NodeAddress string
}

type Manager struct {
	cfg     Config
	mu      sync.Mutex
	pidFile string
	log     *slog.Logger
}

func New(cfg Config) *Manager {
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = defaultConfigPath
	}
	if cfg.MaxConnRate == 0 {
		cfg.MaxConnRate = 30
	}
	if cfg.MaxConnCur == 0 {
		cfg.MaxConnCur = 50
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Manager{
		cfg:     cfg,
		pidFile: "/run/haproxy/haproxy.pid",
		log:     cfg.Logger,
	}
}

// Sync regenerates haproxy.cfg and performs a graceful reload.
func (m *Manager) Sync(nodes []types.Node, servers []ServerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Filter only UP nodes for the API backend
	upNodes := make([]types.Node, 0, len(nodes))
	for _, n := range nodes {
		if n.State == types.NodeUp {
			upNodes = append(upNodes, n)
		}
	}

	// Filter only READY servers for Minecraft backend
	readyServers := make([]ServerEntry, 0, len(servers))
	for _, s := range servers {
		if s.HostPort > 0 {
			readyServers = append(readyServers, s)
		}
	}

	data := struct {
		Nodes       []types.Node
		Servers     []ServerEntry
		MaxConnRate int
		MaxConnCur  int
	}{
		Nodes:       upNodes,
		Servers:     readyServers,
		MaxConnRate: m.cfg.MaxConnRate,
		MaxConnCur:  m.cfg.MaxConnCur,
	}

	var buf bytes.Buffer
	if err := haproxyCfgTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("haproxy: template: %w", err)
	}

	// Validate with haproxy -c
	tmpPath := m.cfg.ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("haproxy: write tmp: %w", err)
	}
	if out, err := exec.Command("haproxy", "-c", "-f", tmpPath).CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("haproxy: config validation failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Atomic rename
	if err := os.Rename(tmpPath, m.cfg.ConfigPath); err != nil {
		return fmt.Errorf("haproxy: rename: %w", err)
	}

	// Graceful reload
	return m.reload()
}

// reload sends the current HAProxy process a graceful reload.
func (m *Manager) reload() error {
	pids := m.readPIDs()
	args := []string{"-f", m.cfg.ConfigPath, "-p", m.pidFile, "-D"}
	for _, p := range pids {
		args = append(args, "-sf", p)
	}
	out, err := exec.Command("haproxy", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("haproxy reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	m.log.Info("haproxy: reloaded", "old_pids", pids)
	return nil
}

func (m *Manager) readPIDs() []string {
	data, err := os.ReadFile(m.pidFile)
	if err != nil {
		return nil
	}
	lines := strings.Fields(string(data))
	pids := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, err := strconv.Atoi(l); err == nil {
			pids = append(pids, l)
		}
	}
	return pids
}

// StartIfNotRunning starts haproxy with our config if it's not already running.
func (m *Manager) StartIfNotRunning() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(m.cfg.ConfigPath); os.IsNotExist(err) {
		m.log.Warn("haproxy: config not found, skipping start", "path", m.cfg.ConfigPath)
		return nil
	}
	pids := m.readPIDs()
	if len(pids) > 0 {
		// Check if process actually exists
		if _, err := os.Stat(filepath.Join("/proc", pids[0])); err == nil {
			return nil // already running
		}
	}
	out, err := exec.Command("haproxy", "-f", m.cfg.ConfigPath, "-p", m.pidFile, "-D").CombinedOutput()
	if err != nil {
		return fmt.Errorf("haproxy start: %w: %s", err, strings.TrimSpace(string(out)))
	}
	m.log.Info("haproxy: started")
	return nil
}

// WatchAndSync runs a periodic sync loop driven by FSM changes.
// Call this in a goroutine.
func (m *Manager) WatchAndSync(
	ctx interface{ Done() <-chan struct{} },
	nodesFn func() []types.Node,
	serversFn func() []ServerEntry,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	type doner interface{ Done() <-chan struct{} }
	for {
		select {
		case <-ctx.(doner).Done():
			return
		case <-ticker.C:
			if err := m.Sync(nodesFn(), serversFn()); err != nil {
				m.log.Warn("haproxy: sync failed", "err", err)
			}
		}
	}
}
