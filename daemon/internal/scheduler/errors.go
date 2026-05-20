package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

var (
	ErrNoHost          = errors.New("scheduler: no viable host")
	ErrTemplateMissing = errors.New("scheduler: template missing")
	ErrServerMissing   = errors.New("scheduler: server missing")
)

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func shortID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
