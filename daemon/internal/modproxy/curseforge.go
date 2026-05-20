package modproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const curseforgeBase = "https://api.curseforge.com/v1"

const (
	// Minecraft game ID on the CurseForge "Eternal" API.
	curseforgeGameMinecraft = 432
)

type CurseForge struct {
	http      *http.Client
	apiKey    string
	userAgent string
}

func NewCurseForge(apiKey, ua string) *CurseForge {
	if ua == "" {
		ua = "AetherNet/0.1"
	}
	return &CurseForge{
		http:      &http.Client{Timeout: 15 * time.Second},
		apiKey:    apiKey,
		userAgent: ua,
	}
}

func (c *CurseForge) Search(ctx context.Context, query string, classID int) ([]CFMod, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("curseforge: no api key configured")
	}
	u, _ := url.Parse(curseforgeBase + "/mods/search")
	q := u.Query()
	q.Set("gameId", strconv.Itoa(curseforgeGameMinecraft))
	q.Set("searchFilter", query)
	if classID != 0 {
		q.Set("classId", strconv.Itoa(classID))
	}
	u.RawQuery = q.Encode()
	var resp struct {
		Data []CFMod `json:"data"`
	}
	if err := c.get(ctx, u.String(), &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// LatestFile returns the newest file for the given mod compatible with both
// the supplied Minecraft version and the loader id.
//
// Loader IDs in the CurseForge API:
//
//	1 = Forge, 2 = Cauldron, 3 = LiteLoader, 4 = Fabric,
//	5 = Quilt, 6 = NeoForge
func (c *CurseForge) LatestFile(ctx context.Context, modID int, mcVersion string, loaderID int) (*CFFile, error) {
	u, _ := url.Parse(fmt.Sprintf("%s/mods/%d/files", curseforgeBase, modID))
	q := u.Query()
	if mcVersion != "" {
		q.Set("gameVersion", mcVersion)
	}
	if loaderID != 0 {
		q.Set("modLoaderType", strconv.Itoa(loaderID))
	}
	q.Set("pageSize", "1")
	u.RawQuery = q.Encode()
	var resp struct {
		Data []CFFile `json:"data"`
	}
	if err := c.get(ctx, u.String(), &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("curseforge: no compatible file for mod %d", modID)
	}
	return &resp.Data[0], nil
}

func (c *CurseForge) Download(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func (c *CurseForge) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("curseforge %s: %s — %s", url, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type CFMod struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Summary string `json:"summary"`
}

type CFFile struct {
	ID           int      `json:"id"`
	ModID        int      `json:"modId"`
	FileName     string   `json:"fileName"`
	DownloadURL  string   `json:"downloadUrl"`
	GameVersions []string `json:"gameVersions"`
	FileLength   uint64   `json:"fileLength"`
	FileDate     time.Time `json:"fileDate"`
}
