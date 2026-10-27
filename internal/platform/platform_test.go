package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hook/internal/store"
)

// fakeAPI serves canned JSON per path and checks the bearer token.
func fakeAPI(t *testing.T, token string, routes map[string]any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.URL.Path
		if pid := r.URL.Query().Get("projectId"); pid != "" {
			key += "?" + pid
		}
		body, ok := routes[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestVercel(t *testing.T) {
	now := time.Now().Add(-time.Hour).UnixMilli()
	VercelAPI = fakeAPI(t, "vtok", map[string]any{
		"/v9/projects": map[string]any{"projects": []any{
			map[string]any{"id": "prj_ok", "name": "marketing", "framework": "nextjs"},
			map[string]any{"id": "prj_bad", "name": "dashboard", "framework": "vite"},
		}},
		"/v6/deployments?prj_ok":  map[string]any{"deployments": []any{map[string]any{"uid": "d1", "url": "marketing-abc.vercel.app", "state": "READY", "created": now}}},
		"/v6/deployments?prj_bad": map[string]any{"deployments": []any{map[string]any{"uid": "d2", "url": "dash-x.vercel.app", "readyState": "ERROR", "created": now}}},
		"/v9/projects/prj_ok/domains": map[string]any{"domains": []any{
			map[string]any{"name": "marketing.vercel.app", "verified": true},
			map[string]any{"name": "www.example.com", "verified": true, "redirect": "example.com"},
			map[string]any{"name": "example.com", "verified": true},
		}},
	})
	acct := &store.Integration{ID: 1, Kind: "vercel", Config: map[string]string{"token": "vtok"}}
	ps, err := Projects(context.Background(), acct)
	if err != nil || len(ps) != 2 {
		t.Fatalf("projects: %v %v", ps, err)
	}
	if ps[0].State != "ready" || ps[0].URL != "https://example.com" || ps[0].Name != "marketing" {
		t.Fatalf("ok project: %+v", ps[0])
	}
	if ps[1].State != "failed" || ps[1].URL != "https://dash-x.vercel.app" {
		t.Fatalf("failed project (readyState fallback, deployment URL): %+v", ps[1])
	}
	if r := Check(context.Background(), acct, "prj_ok"); !r.OK {
		t.Fatalf("ready deploy should be up: %+v", r)
	}
	if r := Check(context.Background(), acct, "prj_bad"); r.OK || !strings.Contains(r.Msg, "failed (ERROR)") {
		t.Fatalf("failed deploy should be down: %+v", r)
	}
	bad := &store.Integration{ID: 2, Kind: "vercel", Config: map[string]string{"token": "wrong"}}
	if _, err := Verify(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("bad token: %v", err)
	}
}

func TestRender(t *testing.T) {
	RenderAPI = fakeAPI(t, "rtok", map[string]any{
		"/v1/services": []any{
			map[string]any{"cursor": "a", "service": map[string]any{"id": "srv-api", "name": "api", "type": "web_service", "suspended": "not_suspended",
				"serviceDetails": map[string]any{"url": "https://api.onrender.com", "plan": "free"}}},
			map[string]any{"cursor": "b", "service": map[string]any{"id": "srv-old", "name": "old", "type": "web_service", "suspended": "suspended"}},
		},
		"/v1/services/srv-api":         map[string]any{"id": "srv-api", "name": "api", "type": "web_service", "suspended": "not_suspended", "serviceDetails": map[string]any{"url": "https://api.onrender.com", "plan": "free"}},
		"/v1/services/srv-api/deploys": []any{map[string]any{"deploy": map[string]any{"status": "build_failed", "finishedAt": time.Now().Format(time.RFC3339)}}},
		"/v1/services/srv-old":         map[string]any{"id": "srv-old", "name": "old", "suspended": "suspended"},
	})
	acct := &store.Integration{ID: 3, Kind: "render", Config: map[string]string{"token": "rtok"}}
	ps, err := Projects(context.Background(), acct)
	if err != nil || len(ps) != 2 {
		t.Fatalf("services: %v %v", ps, err)
	}
	if !ps[0].FreePlan || ps[0].State != "failed" || ps[0].Kind != "web service" {
		t.Fatalf("api service: %+v", ps[0])
	}
	if ps[1].State != "suspended" {
		t.Fatalf("suspended service: %+v", ps[1])
	}
	if r := Check(context.Background(), acct, "srv-old"); r.OK || r.Msg != "Service is suspended" {
		t.Fatalf("suspended check: %+v", r)
	}
	if r := Check(context.Background(), acct, "srv-api"); r.OK || !strings.Contains(r.Msg, "build_failed") {
		t.Fatalf("failed deploy check: %+v", r)
	}
}
