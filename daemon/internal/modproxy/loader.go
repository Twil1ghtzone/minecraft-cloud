package modproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Loader is a Fabric/Forge/NeoForge injector. It downloads the loader JAR
// matching the requested Minecraft version, generates fabric.mod.json
// metadata when needed, and drops the result into the server's mod
// directory.
type Loader struct {
	http *http.Client
}

func NewLoader() *Loader {
	return &Loader{http: &http.Client{Timeout: 30 * 1_000_000_000}}
}

// InstallFabric pulls a server-side Fabric installer JAR for the given
// Minecraft version and writes a `fabric-server-launch.jar` into modDir.
func (l *Loader) InstallFabric(ctx context.Context, modDir, mcVersion, loaderVersion, installerVersion string) error {
	if loaderVersion == "" {
		v, err := l.fabricLatestLoader(ctx)
		if err != nil {
			return err
		}
		loaderVersion = v
	}
	if installerVersion == "" {
		v, err := l.fabricLatestInstaller(ctx)
		if err != nil {
			return err
		}
		installerVersion = v
	}
	url := fmt.Sprintf("https://meta.fabricmc.net/v2/versions/loader/%s/%s/%s/server/jar",
		mcVersion, loaderVersion, installerVersion)
	return l.downloadTo(ctx, url, filepath.Join(modDir, "fabric-server-launch.jar"))
}

// EnsureFabricModJSON writes a minimal fabric.mod.json next to a mod JAR
// when the source archive lacks one (e.g. when injecting a legacy plugin).
func (l *Loader) EnsureFabricModJSON(modDir, modID, version, mcVersion string) error {
	path := filepath.Join(modDir, "fabric.mod.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	doc := map[string]any{
		"schemaVersion": 1,
		"id":            modID,
		"version":       version,
		"name":          modID,
		"environment":   "server",
		"depends": map[string]string{
			"minecraft":    "~" + mcVersion,
			"fabricloader": "*",
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func (l *Loader) fabricLatestLoader(ctx context.Context) (string, error) {
	var versions []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := l.getJSON(ctx, "https://meta.fabricmc.net/v2/versions/loader", &versions); err != nil {
		return "", err
	}
	for _, v := range versions {
		if v.Stable {
			return v.Version, nil
		}
	}
	if len(versions) > 0 {
		return versions[0].Version, nil
	}
	return "", fmt.Errorf("no fabric loader versions")
}

func (l *Loader) fabricLatestInstaller(ctx context.Context) (string, error) {
	var versions []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := l.getJSON(ctx, "https://meta.fabricmc.net/v2/versions/installer", &versions); err != nil {
		return "", err
	}
	for _, v := range versions {
		if v.Stable {
			return v.Version, nil
		}
	}
	if len(versions) > 0 {
		return versions[0].Version, nil
	}
	return "", fmt.Errorf("no fabric installer versions")
}

func (l *Loader) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("get %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (l *Loader) downloadTo(ctx context.Context, url, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
