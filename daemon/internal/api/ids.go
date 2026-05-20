package api

import (
	"crypto/rand"
	"encoding/hex"
)

func newServerID() string   { return prefixID("srv") }
func newDatabaseID() string { return prefixID("db") }
func newTokenID() string    { return prefixID("tok") }

func prefixID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
