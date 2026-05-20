package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aethernet/aethernet/pkg/types"
)

// TemplateManager handles the on-disk layout for both static and dynamic
// templates.
//
// Static templates persist between runs: any change a server makes in /data
// stays in the template volume.
//
// Dynamic templates clone fresh on startup and the writable scratch is
// destroyed on shutdown.
type TemplateManager struct {
	Root string
}

// MaterializeScratch creates the per-server writable directory for `srv`.
// For static templates we hard-link the base into scratch (so writes flow
// back); for dynamic, we copy.
func (m *TemplateManager) MaterializeScratch(_ context.Context, tpl types.Template, srv types.Server, scratchRoot string) (string, error) {
	target := filepath.Join(scratchRoot, srv.Spec.ID)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(m.Root, tpl.ID)
	if _, err := os.Stat(base); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return target, nil // empty base, server boots from blank slate
		}
		return "", fmt.Errorf("stat template base: %w", err)
	}
	switch tpl.Mode {
	case "static":
		// Static: bind-mount semantics — we leave the scratch empty and
		// rely on the docker controller to mount the template as /template
		// (read-only) and scratch as /data (writable). Server JARs read
		// /template; writes go to /data. A separate sync step periodically
		// merges /data back into the template volume.
		return target, nil
	case "dynamic":
		// Dynamic: copy entire tree into scratch.
		return target, copyTree(base, target)
	}
	return "", fmt.Errorf("unknown template mode %q", tpl.Mode)
}

// CleanupScratch removes the per-server scratch directory for a dynamic
// template. Static templates leave their state in place.
func (m *TemplateManager) CleanupScratch(tpl types.Template, srv types.Server, scratchRoot string) error {
	if tpl.Mode != "dynamic" {
		return nil
	}
	return os.RemoveAll(filepath.Join(scratchRoot, srv.Spec.ID))
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
