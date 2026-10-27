package platform

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Render API (read-only use):
//   GET /v1/services                  list services
//   GET /v1/services/{id}             one service (suspended flag)
//   GET /v1/services/{id}/deploys     latest deploy

type renderService struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Suspended      string `json:"suspended"`
	DashboardURL   string `json:"dashboardUrl"`
	ServiceDetails struct {
		URL  string `json:"url"`
		Plan string `json:"plan"`
	} `json:"serviceDetails"`
}

func renderState(s string) string {
	switch s {
	case "live":
		return "ready"
	case "created", "build_in_progress", "update_in_progress", "pre_deploy_in_progress":
		return "building"
	case "canceled", "deactivated":
		return "canceled"
	case "":
		return "none"
	}
	return "failed" // build_failed, update_failed, pre_deploy_failed, …
}

func (s renderService) project() Project {
	p := Project{ID: s.ID, Name: s.Name, Kind: strings.ReplaceAll(s.Type, "_", " "), URL: s.ServiceDetails.URL, State: "none",
		FreePlan: s.ServiceDetails.Plan == "free", Dashboard: s.DashboardURL}
	if s.Suspended == "suspended" {
		p.State, p.Detail = "suspended", "suspended"
	}
	return p
}

func renderServices(ctx context.Context, token string) ([]Project, error) {
	var resp []struct {
		Service renderService `json:"service"`
	}
	if err := get(ctx, RenderAPI, "/v1/services", token, url.Values{"limit": {"100"}}, &resp); err != nil {
		return nil, err
	}
	out := make([]Project, len(resp))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, r := range resp {
		out[i] = r.Service.project()
		if out[i].State == "suspended" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if st, detail, at, err := renderDeploy(ctx, token, out[i].ID); err == nil {
				out[i].State, out[i].Detail, out[i].UpdatedAt = st, detail, at
			}
		}(i)
	}
	wg.Wait()
	return out, nil
}

func renderDeploy(ctx context.Context, token, id string) (state, detail string, at int64, err error) {
	var resp []struct {
		Deploy struct {
			Status     string    `json:"status"`
			CreatedAt  time.Time `json:"createdAt"`
			FinishedAt time.Time `json:"finishedAt"`
		} `json:"deploy"`
	}
	if err := get(ctx, RenderAPI, "/v1/services/"+url.PathEscape(id)+"/deploys", token, url.Values{"limit": {"1"}}, &resp); err != nil {
		return "", "", 0, err
	}
	if len(resp) == 0 {
		return "none", "", 0, nil
	}
	d := resp[0].Deploy
	t := d.FinishedAt
	if t.IsZero() {
		t = d.CreatedAt
	}
	return renderState(d.Status), d.Status, t.UnixMilli(), nil
}

func renderLatest(ctx context.Context, token, id string) (*Project, error) {
	var s renderService
	if err := get(ctx, RenderAPI, "/v1/services/"+url.PathEscape(id), token, nil, &s); err != nil {
		return nil, err
	}
	p := s.project()
	if p.State == "suspended" {
		return &p, nil
	}
	st, detail, at, err := renderDeploy(ctx, token, id)
	if err != nil {
		return nil, err
	}
	p.State, p.Detail, p.UpdatedAt = st, detail, at
	return &p, nil
}
