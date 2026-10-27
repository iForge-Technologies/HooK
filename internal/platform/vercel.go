package platform

import (
	"context"
	"net/url"
	"strings"
	"sync"
)

// Vercel REST API (read-only use):
//   GET /v9/projects                       list projects
//   GET /v6/deployments?projectId&target   latest production deployment
//   GET /v9/projects/{id}/domains          custom domains to monitor

func teamQuery(teamID string) url.Values {
	q := url.Values{}
	if teamID != "" {
		q.Set("teamId", teamID)
	}
	return q
}

type vercelDeployment struct {
	UID        string `json:"uid"`
	URL        string `json:"url"`
	State      string `json:"state"`
	ReadyState string `json:"readyState"`
	Created    int64  `json:"created"`
	CreatedAt  int64  `json:"createdAt"`
}

func (d vercelDeployment) state() string {
	if d.State != "" {
		return d.State
	}
	return d.ReadyState
}

func vercelState(s string) string {
	switch strings.ToUpper(s) {
	case "READY":
		return "ready"
	case "BUILDING", "QUEUED", "INITIALIZING":
		return "building"
	case "CANCELED":
		return "canceled"
	case "":
		return "none"
	}
	return "failed" // ERROR and anything new
}

func vercelProjects(ctx context.Context, token, teamID string) ([]Project, error) {
	q := teamQuery(teamID)
	q.Set("limit", "100")
	var resp struct {
		Projects []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Framework string `json:"framework"`
		} `json:"projects"`
	}
	if err := get(ctx, VercelAPI, "/v9/projects", token, q, &resp); err != nil {
		return nil, err
	}
	out := make([]Project, len(resp.Projects))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // stay well under Vercel's rate limits
	for i, p := range resp.Projects {
		out[i] = Project{ID: p.ID, Name: p.Name, Kind: p.Framework, State: "none"}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if latest, err := vercelLatest(ctx, token, teamID, id); err == nil {
				latest.Name, latest.Kind = out[i].Name, out[i].Kind
				out[i] = *latest
			}
			if u := vercelDomain(ctx, token, teamID, id); u != "" {
				out[i].URL = u
			}
		}(i, p.ID)
	}
	wg.Wait()
	return out, nil
}

func vercelLatest(ctx context.Context, token, teamID, projectID string) (*Project, error) {
	q := teamQuery(teamID)
	q.Set("projectId", projectID)
	q.Set("target", "production")
	q.Set("limit", "1")
	var resp struct {
		Deployments []vercelDeployment `json:"deployments"`
	}
	if err := get(ctx, VercelAPI, "/v6/deployments", token, q, &resp); err != nil {
		return nil, err
	}
	p := &Project{ID: projectID, State: "none", Dashboard: "https://vercel.com/dashboard"}
	if len(resp.Deployments) > 0 {
		d := resp.Deployments[0]
		p.Detail = d.state()
		p.State = vercelState(p.Detail)
		p.UpdatedAt = max(d.Created, d.CreatedAt)
		if d.URL != "" {
			p.URL = "https://" + d.URL
		}
	}
	return p, nil
}

// vercelDomain prefers a verified custom domain that isn't a redirect.
func vercelDomain(ctx context.Context, token, teamID, projectID string) string {
	var resp struct {
		Domains []struct {
			Name     string `json:"name"`
			Redirect string `json:"redirect"`
			Verified bool   `json:"verified"`
		} `json:"domains"`
	}
	if err := get(ctx, VercelAPI, "/v9/projects/"+url.PathEscape(projectID)+"/domains", token, teamQuery(teamID), &resp); err != nil {
		return ""
	}
	best := ""
	for _, d := range resp.Domains {
		if d.Redirect != "" || !d.Verified {
			continue
		}
		if best == "" || (strings.HasSuffix(best, ".vercel.app") && !strings.HasSuffix(d.Name, ".vercel.app")) {
			best = d.Name
		}
	}
	if best == "" {
		return ""
	}
	return "https://" + best
}
