package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// generateSecureToken creates a cryptographically random API token
// with the prefix "aether_".
func generateSecureToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "aether_" + hex.EncodeToString(b)
}

// tokenID derives a short identifier from the raw token.
// This is stored in the FSM (not the raw token).
func tokenID(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:8])
}

// tokenHash derives a SHA-256 hash of the raw token for verification.
// In production this would use argon2id for hardened storage.
func tokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}

// splitPath splits a URL path into non-empty segments.
func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
