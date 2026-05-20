// Package firewall manages host iptables rules for AetherNet container ports.
//
// Rules are inserted into the AETHERNET_FW chain which is hooked into
// INPUT and FORWARD. Each rule is tagged with a comment containing the
// server ID so they can be bulk-pruned on container removal.
package firewall

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
)

const chain = "AETHERNET_FW"

// Manager manages iptables rules for AetherNet.
type Manager struct {
	mu  sync.Mutex
	log *slog.Logger
}

func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log}
}

// EnsureChain creates the AETHERNET_FW chain and hooks it into INPUT/FORWARD
// if it doesn't already exist.
func (m *Manager) EnsureChain() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Create chain (idempotent: -N returns error if exists, we ignore it)
	_ = ipt("-N", chain)
	// Hook chain into INPUT and FORWARD if not already hooked
	_ = ipt("-C", "INPUT", "-j", chain)
	if err := ipt("-C", "INPUT", "-j", chain); err != nil {
		_ = ipt("-I", "INPUT", "-j", chain)
	}
	if err := ipt("-C", "FORWARD", "-j", chain); err != nil {
		_ = ipt("-I", "FORWARD", "-j", chain)
	}
	m.log.Info("iptables chain ensured", "chain", chain)
	return nil
}

// OpenPort allows inbound TCP and UDP traffic on hostPort destined for containerIP,
// tagged with a comment for later cleanup by serverID.
func (m *Manager) OpenPort(serverID, containerIP string, hostPort uint32, proto string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	comment := serverTag(serverID)
	p := strings.ToLower(proto)
	if p != "tcp" && p != "udp" {
		p = "tcp"
	}
	args := []string{
		"-A", chain,
		"-p", p,
		"--dport", fmt.Sprintf("%d", hostPort),
		"-d", containerIP,
		"-j", "ACCEPT",
		"-m", "comment", "--comment", comment,
	}
	if err := ipt(args...); err != nil {
		return fmt.Errorf("open port %d/%s for %s: %w", hostPort, p, serverID, err)
	}
	m.log.Info("firewall: port opened", "server_id", serverID, "port", hostPort, "proto", p)
	return nil
}

// ClosePort removes the accept rule for a specific port.
func (m *Manager) ClosePort(serverID, containerIP string, hostPort uint32, proto string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	comment := serverTag(serverID)
	p := strings.ToLower(proto)
	if p != "tcp" && p != "udp" {
		p = "tcp"
	}
	args := []string{
		"-D", chain,
		"-p", p,
		"--dport", fmt.Sprintf("%d", hostPort),
		"-d", containerIP,
		"-j", "ACCEPT",
		"-m", "comment", "--comment", comment,
	}
	if err := ipt(args...); err != nil {
		return fmt.Errorf("close port %d/%s for %s: %w", hostPort, p, serverID, err)
	}
	m.log.Info("firewall: port closed", "server_id", serverID, "port", hostPort, "proto", p)
	return nil
}

// FlushServerRules removes ALL iptables rules tagged with serverID.
// Called on container stop/deletion.
func (m *Manager) FlushServerRules(serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	comment := serverTag(serverID)
	// List all rules in the chain and delete matching ones.
	out, err := exec.Command("iptables", "-S", chain).Output()
	if err != nil {
		return nil // chain doesn't exist or iptables not available
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if !strings.Contains(line, comment) {
			continue
		}
		// Convert -A to -D
		delLine := strings.Replace(line, "-A "+chain, "-D "+chain, 1)
		parts := strings.Fields(delLine)
		if len(parts) == 0 {
			continue
		}
		_ = ipt(parts...)
	}
	m.log.Info("firewall: flushed all rules for server", "server_id", serverID)
	return nil
}

// BlockIP inserts a DROP rule for a malicious source IP at the top of the chain.
func (m *Manager) BlockIP(ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ipt("-I", chain, "-s", ip, "-j", "DROP", "-m", "comment", "--comment", "aethernet-ddos-block")
}

// UnblockIP removes the DROP rule for an IP.
func (m *Manager) UnblockIP(ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ipt("-D", chain, "-s", ip, "-j", "DROP", "-m", "comment", "--comment", "aethernet-ddos-block")
}

func serverTag(id string) string {
	return "aethernet-srv-" + id
}

func ipt(args ...string) error {
	cmd := exec.Command("iptables", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %v: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
