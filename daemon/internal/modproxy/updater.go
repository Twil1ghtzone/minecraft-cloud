package modproxy

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Updater periodically scans the /plugins and /mods directories of each
// active server, reads metadata, compares against Modrinth/CurseForge and
// schedules upgrades for the next restart.
type Updater struct {
	scratchRoot string
	modrinth    *Modrinth
	curseforge  *CurseForge
	log         *slog.Logger
	interval    time.Duration

	// Scheduled is appended to whenever an update is queued. The daemon
	// drains it on server restart.
	Scheduled chan ScheduledUpdate
}

type ScheduledUpdate struct {
	ServerID    string
	Kind        string // "plugin" | "mod"
	Filename    string
	NewURL      string
	NewVersion  string
}

func NewUpdater(scratchRoot string, m *Modrinth, c *CurseForge, log *slog.Logger) *Updater {
	if log == nil {
		log = slog.Default()
	}
	return &Updater{
		scratchRoot: scratchRoot,
		modrinth:    m,
		curseforge:  c,
		log:         log,
		interval:    15 * time.Minute,
		Scheduled:   make(chan ScheduledUpdate, 64),
	}
}

func (u *Updater) Run(ctx context.Context) {
	t := time.NewTicker(u.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u.scanOnce(ctx)
		}
	}
}

func (u *Updater) scanOnce(ctx context.Context) {
	entries, err := os.ReadDir(u.scratchRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		serverDir := filepath.Join(u.scratchRoot, e.Name())
		u.scanServer(ctx, e.Name(), serverDir)
	}
}

func (u *Updater) scanServer(ctx context.Context, serverID, dir string) {
	for _, sub := range []struct{ kind, name string }{{"plugin", "plugins"}, {"mod", "mods"}} {
		ddir := filepath.Join(dir, sub.name)
		jars, _ := filepath.Glob(filepath.Join(ddir, "*.jar"))
		for _, jar := range jars {
			meta, err := readMetadata(jar)
			if err != nil {
				u.log.Debug("skip jar without metadata", "jar", jar, "err", err)
				continue
			}
			if meta.ModrinthID == "" && meta.CurseForgeID == 0 {
				continue
			}
			if meta.ModrinthID != "" && u.modrinth != nil {
				v, err := u.modrinth.LatestVersion(ctx, meta.ModrinthID, meta.MCVersion, meta.Loader)
				if err == nil && v.Version != meta.Version && len(v.Files) > 0 {
					u.Scheduled <- ScheduledUpdate{
						ServerID:   serverID,
						Kind:       sub.kind,
						Filename:   filepath.Base(jar),
						NewURL:     primaryFile(v).URL,
						NewVersion: v.Version,
					}
				}
			}
		}
	}
}

func primaryFile(v *ModrinthVersion) ModrinthFile {
	for _, f := range v.Files {
		if f.Primary {
			return f
		}
	}
	if len(v.Files) > 0 {
		return v.Files[0]
	}
	return ModrinthFile{}
}

type pluginMeta struct {
	Name         string
	Version      string
	MCVersion    string
	Loader       string
	ModrinthID   string
	CurseForgeID int
}

// readMetadata pulls plugin.yml / fabric.mod.json out of a JAR.
func readMetadata(path string) (pluginMeta, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return pluginMeta{}, err
	}
	defer r.Close()
	for _, f := range r.File {
		switch f.Name {
		case "plugin.yml", "paper-plugin.yml":
			rc, err := f.Open()
			if err != nil {
				return pluginMeta{}, err
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			var py struct {
				Name    string `yaml:"name"`
				Version string `yaml:"version"`
				APIVersion string `yaml:"api-version"`
			}
			_ = yaml.Unmarshal(b, &py)
			return pluginMeta{
				Name: py.Name, Version: py.Version,
				MCVersion: py.APIVersion, Loader: "paper",
			}, nil
		case "fabric.mod.json":
			rc, err := f.Open()
			if err != nil {
				return pluginMeta{}, err
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			return parseFabricMod(b)
		}
	}
	return pluginMeta{}, errors.New("no recognized metadata file in jar")
}

func parseFabricMod(b []byte) (pluginMeta, error) {
	// minimal parser; full schema covered upstream
	var doc struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Custom  struct {
			Modrinth struct {
				ProjectID string `json:"project_id"`
			} `json:"modrinth"`
		} `json:"custom"`
	}
	_ = doc
	out := pluginMeta{Loader: "fabric"}
	// Use stdlib JSON via go yaml fallback to keep imports tight.
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return out, err
	}
	out.Name = doc.ID
	out.Version = doc.Version
	out.ModrinthID = doc.Custom.Modrinth.ProjectID
	return out, nil
}

func loaderFromAPIVersion(s string) string {
	if strings.HasPrefix(s, "1.") {
		return "paper"
	}
	return ""
}
