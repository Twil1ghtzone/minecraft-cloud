// Package modproxy implements REST clients for the Modrinth and CurseForge
// repositories and a background updater that scans /plugins and /mods
// directories of running servers.
package modproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const modrinthBase = "https://api.modrinth.com/v2"

type Modrinth struct {
	http      *http.Client
	userAgent string
}

func NewModrinth(ua string) *Modrinth {
	if ua == "" {
		ua = "AetherNet/0.1 (+https://aether.invalid)"
	}
	return &Modrinth{
		http:      &http.Client{Timeout: 15 * time.Second},
		userAgent: ua,
	}
}

func (m *Modrinth) Search(ctx context.Context, query string, facets []string) ([]ModrinthProject, error) {
	u, _ := url.Parse(modrinthBase + "/search")
	q := u.Query()
	q.Set("query", query)
	if len(facets) > 0 {
		// Modrinth wants JSON-encoded array of array form.
		facetJSON, _ := json.Marshal([][]string{facets})
		q.Set("facets", string(facetJSON))
	}
	u.RawQuery = q.Encode()
	var resp struct {
		Hits []ModrinthProject `json:"hits"`
	}
	if err := m.get(ctx, u.String(), &resp); err != nil {
		return nil, err
	}
	return resp.Hits, nil
}

// LatestVersion returns the latest version of a project that supports the
// given Minecraft version and loader (fabric, forge, paper, velocity, ...).
func (m *Modrinth) LatestVersion(ctx context.Context, slugOrID, mcVersion, loader string) (*ModrinthVersion, error) {
	u, _ := url.Parse(modrinthBase + "/project/" + slugOrID + "/version")
	q := u.Query()
	if mcVersion != "" {
		gv, _ := json.Marshal([]string{mcVersion})
		q.Set("game_versions", string(gv))
	}
	if loader != "" {
		lv, _ := json.Marshal([]string{loader})
		q.Set("loaders", string(lv))
	}
	u.RawQuery = q.Encode()
	var versions []ModrinthVersion
	if err := m.get(ctx, u.String(), &versions); err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions for %s on %s/%s", slugOrID, mcVersion, loader)
	}
	// Modrinth returns newest-first.
	return &versions[0], nil
}

func (m *Modrinth) Download(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", m.userAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func (m *Modrinth) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", m.userAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("modrinth %s: %s — %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ModrinthProject struct {
	ProjectID   string   `json:"project_id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	Downloads   uint64   `json:"downloads"`
}

type ModrinthVersion struct {
	ID         string           `json:"id"`
	ProjectID  string           `json:"project_id"`
	Name       string           `json:"name"`
	Version    string           `json:"version_number"`
	GameVers   []string         `json:"game_versions"`
	Loaders    []string         `json:"loaders"`
	Files      []ModrinthFile   `json:"files"`
	DatePublished time.Time     `json:"date_published"`
	Dependencies []ModrinthDep  `json:"dependencies"`
}

type ModrinthFile struct {
	URL      string            `json:"url"`
	Filename string            `json:"filename"`
	Primary  bool              `json:"primary"`
	Size     uint64            `json:"size"`
	Hashes   map[string]string `json:"hashes"`
}

type ModrinthDep struct {
	ProjectID string `json:"project_id"`
	Type      string `json:"dependency_type"` // required, optional, embedded, incompatible
}
