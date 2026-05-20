// Package keepalived manages the Virtual IP (VIP) via keepalived.
//
// AetherNet uses a single floating VIP that follows the Raft leader.
// The Go daemon writes /etc/keepalived/keepalived.conf and signals
// keepalived to reload whenever the Raft leadership changes.
package keepalived

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

var keepalivedTmpl = template.Must(template.New("ka").Parse(`
global_defs {
    router_id {{ .RouterID }}
    enable_script_security
}

vrrp_script chk_haproxy {
    script "/bin/sh -c 'kill -0 $(cat /run/haproxy/haproxy.pid 2>/dev/null) 2>/dev/null'"
    interval 2
    weight -20
    fall 2
    rise 2
}

vrrp_instance AETHERNET_VIP {
    state {{ .State }}
    interface {{ .Interface }}
    virtual_router_id {{ .RouterID_Int }}
    priority {{ .Priority }}
    advert_int 1
    authentication {
        auth_type PASS
        auth_pass {{ .AuthPass }}
    }
    virtual_ipaddress {
        {{ .VIP }}/{{ .CIDRPrefix }}
    }
    track_script {
        chk_haproxy
    }
    notify_master "/etc/keepalived/notify.sh MASTER"
    notify_backup "/etc/keepalived/notify.sh BACKUP"
    notify_fault  "/etc/keepalived/notify.sh FAULT"
}
`))

type Config struct {
	RouterID    string // e.g. "aether-node-1"
	RouterIDInt int    // VRRP router ID (1-255)
	Interface   string // network interface, e.g. "eth0"
	VIP         string // virtual IP, e.g. "10.0.0.100"
	CIDRPrefix  int    // e.g. 24
	AuthPass    string // VRRP authentication password
	Logger      *slog.Logger
}

type Manager struct {
	cfg Config
	log *slog.Logger
}

func New(cfg Config) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Interface == "" {
		cfg.Interface = "eth0"
	}
	if cfg.CIDRPrefix == 0 {
		cfg.CIDRPrefix = 24
	}
	if cfg.RouterIDInt == 0 {
		cfg.RouterIDInt = 51
	}
	return &Manager{cfg: cfg, log: cfg.Logger}
}

// SetLeader writes a MASTER keepalived.conf (high priority).
func (m *Manager) SetLeader() error {
	return m.writeConfig("MASTER", 150)
}

// SetBackup writes a BACKUP keepalived.conf (normal priority).
func (m *Manager) SetBackup() error {
	return m.writeConfig("BACKUP", 100)
}

func (m *Manager) writeConfig(state string, priority int) error {
	data := struct {
		RouterID     string
		RouterID_Int int
		Interface    string
		VIP          string
		CIDRPrefix   int
		AuthPass     string
		State        string
		Priority     int
	}{
		RouterID:     m.cfg.RouterID,
		RouterID_Int: m.cfg.RouterIDInt,
		Interface:    m.cfg.Interface,
		VIP:          m.cfg.VIP,
		CIDRPrefix:   m.cfg.CIDRPrefix,
		AuthPass:     m.cfg.AuthPass,
		State:        state,
		Priority:     priority,
	}
	var buf bytes.Buffer
	if err := keepalivedTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("keepalived: template: %w", err)
	}
	if err := os.MkdirAll("/etc/keepalived", 0o755); err != nil {
		return err
	}
	tmpPath := "/etc/keepalived/keepalived.conf.tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("keepalived: write: %w", err)
	}
	if err := os.Rename(tmpPath, "/etc/keepalived/keepalived.conf"); err != nil {
		return fmt.Errorf("keepalived: rename: %w", err)
	}
	if err := m.reload(); err != nil {
		return err
	}
	m.log.Info("keepalived: config written", "state", state, "priority", priority, "vip", m.cfg.VIP)
	return nil
}

func (m *Manager) reload() error {
	out, err := exec.Command("systemctl", "reload-or-restart", "keepalived").CombinedOutput()
	if err != nil {
		return fmt.Errorf("keepalived reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WatchLeaderChanges watches the leaderCh channel and updates keepalived
// state accordingly. Call in a goroutine.
func (m *Manager) WatchLeaderChanges(leaderCh <-chan string, myNodeID string) {
	for leaderAddr := range leaderCh {
		var err error
		if leaderAddr != "" && strings.Contains(leaderAddr, myNodeID) {
			err = m.SetLeader()
		} else {
			err = m.SetBackup()
		}
		if err != nil {
			m.log.Warn("keepalived: update failed", "err", err)
		}
	}
}
