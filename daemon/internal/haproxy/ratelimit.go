package haproxy

import (
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RateLimiter tracks known abusive IPs and syncs DROP rules to iptables.
// It uses a simple exponential decay: IPs are unblocked after BlockTTL
// unless they continue flooding.
type RateLimiter struct {
	mu       sync.Mutex
	blocked  map[string]time.Time // ip -> blocked_until
	log      *slog.Logger
	blockTTL time.Duration
}

func NewRateLimiter(log *slog.Logger) *RateLimiter {
	if log == nil {
		log = slog.Default()
	}
	return &RateLimiter{
		blocked:  make(map[string]time.Time),
		log:      log,
		blockTTL: 5 * time.Minute,
	}
}

// BlockFloodIP adds an IP to the kernel DROP list.
func (r *RateLimiter) BlockFloodIP(ip string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("ratelimit: invalid IP: %s", ip)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	until := time.Now().Add(r.blockTTL)
	if existing, ok := r.blocked[ip]; ok && existing.After(time.Now()) {
		// Already blocked; extend TTL
		r.blocked[ip] = until
		return nil
	}
	r.blocked[ip] = until
	// Add iptables DROP rule
	if err := iptBlock(ip); err != nil {
		r.log.Warn("ratelimit: iptables block failed", "ip", ip, "err", err)
		return err
	}
	r.log.Warn("ratelimit: blocked flood IP", "ip", ip, "until", until.Format(time.RFC3339))
	return nil
}

// UnblockExpired removes IPs whose block TTL has expired.
func (r *RateLimiter) UnblockExpired() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for ip, until := range r.blocked {
		if now.After(until) {
			_ = iptUnblock(ip)
			delete(r.blocked, ip)
			r.log.Info("ratelimit: unblocked expired IP", "ip", ip)
		}
	}
}

// RunExpiryLoop periodically removes expired blocks. Call in a goroutine.
func (r *RateLimiter) RunExpiryLoop(ctx interface{ Done() <-chan struct{} }) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	type doner interface{ Done() <-chan struct{} }
	for {
		select {
		case <-ctx.(doner).Done():
			return
		case <-ticker.C:
			r.UnblockExpired()
		}
	}
}

func iptBlock(ip string) error {
	args := []string{"-I", "INPUT", "-s", ip, "-j", "DROP",
		"-m", "comment", "--comment", "aethernet-ratelimit"}
	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func iptUnblock(ip string) error {
	args := []string{"-D", "INPUT", "-s", ip, "-j", "DROP",
		"-m", "comment", "--comment", "aethernet-ratelimit"}
	_, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		// Non-fatal: rule may already be gone
		return nil
	}
	return nil
}
