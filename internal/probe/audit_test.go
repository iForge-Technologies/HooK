package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// Regression tests for the 2026-10-27 security audit.

// Public-only probes used the server's own resolver and returned private
// answers and addresses to the hub.
func TestPublicOnlyRevealsNothingPrivate(t *testing.T) {
	ctx := context.Background()
	for _, s := range []Spec{
		{Type: "dns", Target: "db.corp.internal", Timeout: 2},                          // system resolver
		{Type: "dns", Target: "db.corp.internal", DNSResolver: "10.0.0.2", Timeout: 2}, // private resolver
		{Type: "tcp", Target: "127.0.0.1:1", Timeout: 2},
		{Type: "http", Target: "http://127.0.0.1:1/", Timeout: 2},
	} {
		r := RunPublicOnly(ctx, s)
		if r.OK || !WasRefused(r) {
			t.Fatalf("%s %s: want a refusal, got %+v", s.Type, s.Target, r)
		}
		if strings.Contains(r.Msg, "127.0.0.1") || strings.Contains(r.Msg, "10.0.0") {
			t.Fatalf("%s: refusal leaks an address: %q", s.Type, r.Msg)
		}
	}
}

func TestPublicTarget(t *testing.T) {
	ctx := context.Background()
	if !publicTarget(ctx, "8.8.8.8") || publicTarget(ctx, "127.0.0.1") || publicTarget(ctx, "169.254.169.254") {
		t.Fatal("publicTarget misclassifies IP literals")
	}
	if isPublic(netip.MustParseAddr("192.168.1.1")) {
		t.Fatal("private address counted as public")
	}
}

// A monitored target redirected the hub's own check to its private network.
// Once the configured target is public, no hop may land on a private address.
func TestHubCheckOfPublicTargetStaysPublic(t *testing.T) {
	internal := 0
	in := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internal++ }))
	defer in.Close()
	out := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, in.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer out.Close()
	spec := Spec{Type: "http", Target: out.URL, Timeout: 3, FollowRedirects: true}

	// Unpinned (an admin-chosen private target): redirects are followed as before.
	if r := Run(context.Background(), spec); !r.OK || internal != 1 {
		t.Fatalf("unpinned check: %+v, internal hits %d", r, internal)
	}
	targetIsPublic = func(context.Context, string) bool { return true }
	defer func() { targetIsPublic = publicTarget }()
	if r := Run(context.Background(), spec); r.OK || internal != 1 {
		t.Fatalf("pinned check reached a private address: %+v, internal hits %d", r, internal)
	}
}
