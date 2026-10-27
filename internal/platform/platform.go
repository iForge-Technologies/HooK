// Package platform talks to the Vercel and Render APIs with a read-only token:
// it lists projects/services and reports the state of the latest production
// deploy. It never changes anything on either platform.
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"hook/internal/checks"
	"hook/internal/store"
)

// Base URLs; tests point them at fakes.
var (
	VercelAPI = "https://api.vercel.com"
	RenderAPI = "https://api.render.com"
)

var Kinds = []string{"vercel", "render"}

// Project is a Vercel project or Render service, normalised.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`      // framework (vercel) or service type (render)
	URL       string `json:"url"`       // public URL to monitor, if any
	State     string `json:"state"`     // latest production deploy: ready | building | failed | canceled | none | suspended
	Detail    string `json:"detail"`    // raw provider state
	UpdatedAt int64  `json:"updatedAt"` // unix ms of that deploy
	FreePlan  bool   `json:"freePlan"`  // Render free instances sleep after 15 min idle
	Dashboard string `json:"dashboard"`
}

var client = &http.Client{Timeout: 20 * time.Second}

func get(ctx context.Context, base, path, token string, q url.Values, out any) error {
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Hook-Monitor/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("the API token was rejected (expired, revoked or missing scope)")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("rate limited by the platform API; try a longer check interval")
	case resp.StatusCode >= 300:
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return json.Unmarshal(body, out)
}

// Projects lists an account's projects with their latest production deploy,
// cached for a minute so the Platforms page doesn't burn API quota.
func Projects(ctx context.Context, in *store.Integration) ([]Project, error) {
	key := fmt.Sprintf("%d:%s", in.ID, in.Config["token"])
	cache.Lock()
	if c, ok := cache.m[key]; ok && time.Since(c.at) < time.Minute {
		cache.Unlock()
		return c.projects, nil
	}
	cache.Unlock()
	var ps []Project
	var err error
	switch in.Kind {
	case "vercel":
		ps, err = vercelProjects(ctx, in.Config["token"], in.Config["teamId"])
	case "render":
		ps, err = renderServices(ctx, in.Config["token"])
	default:
		err = fmt.Errorf("unknown platform %q", in.Kind)
	}
	if err != nil {
		return nil, err
	}
	cache.Lock()
	cache.m[key] = cached{at: time.Now(), projects: ps}
	cache.Unlock()
	return ps, nil
}

type cached struct {
	at       time.Time
	projects []Project
}

var cache = struct {
	sync.Mutex
	m map[string]cached
}{m: map[string]cached{}}

// Forget drops cached projects after an account changes.
func Forget(id int64) {
	cache.Lock()
	defer cache.Unlock()
	prefix := fmt.Sprintf("%d:", id)
	for k := range cache.m {
		if strings.HasPrefix(k, prefix) {
			delete(cache.m, k)
		}
	}
}

// Check is a deploy monitor's probe: down when the latest production deploy
// failed or the service is suspended. The site may still be serving the
// previous deploy, which the message says.
func Check(ctx context.Context, in *store.Integration, projectID string) checks.Result {
	start := time.Now()
	var p *Project
	var err error
	switch in.Kind {
	case "vercel":
		p, err = vercelLatest(ctx, in.Config["token"], in.Config["teamId"], projectID)
	case "render":
		p, err = renderLatest(ctx, in.Config["token"], projectID)
	default:
		err = fmt.Errorf("unknown platform %q", in.Kind)
	}
	lat := time.Since(start)
	if err != nil {
		return checks.Result{Latency: lat, Msg: err.Error()}
	}
	ago := ""
	if p.UpdatedAt > 0 {
		ago = " · " + humanAgo(time.Since(time.UnixMilli(p.UpdatedAt)))
	}
	switch p.State {
	case "ready":
		return checks.Result{OK: true, Latency: lat, Msg: "Production deploy live" + ago}
	case "building":
		return checks.Result{OK: true, Latency: lat, Msg: "Deploying (" + p.Detail + ")" + ago}
	case "canceled":
		return checks.Result{OK: true, Latency: lat, Msg: "Latest deploy canceled; previous one still live" + ago}
	case "suspended":
		return checks.Result{Latency: lat, Msg: "Service is suspended"}
	case "none":
		return checks.Result{Latency: lat, Msg: "No production deploys found"}
	}
	return checks.Result{Latency: lat, Msg: "Latest production deploy failed (" + p.Detail + ")" + ago + "; the previous deploy may still be serving"}
}

func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// Verify checks a token by listing projects (used before saving an account).
func Verify(ctx context.Context, in *store.Integration) (int, error) {
	Forget(in.ID)
	ps, err := Projects(ctx, in)
	return len(ps), err
}
