package monitor

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hook/internal/checks"
	"hook/internal/fleet"
	"hook/internal/probe"
	"hook/internal/store"
)

// Regression tests for the 2026-10-27 security audit.

// One agent answering "reachable" silenced every outage.
func TestOneLocationCannotOverrideDown(t *testing.T) {
	failed := checks.Result{Msg: "Connection refused"}
	m := &store.Monitor{Type: "http", Target: "https://example.com", Timeout: 10}
	p := &fakeProber{out: []fleet.Outcome{{Location: "evil", Result: probe.Result{OK: true}}}}
	s := &Scheduler{prober: p}
	if r := s.confirm(context.Background(), m, failed); r.OK || r.Partial {
		t.Fatalf("a single remote OK overrode the hub's failure: %+v", r)
	}
}

// URL credentials were sent to remote agents.
func TestURLCredentialsStayOnTheHub(t *testing.T) {
	failed := checks.Result{Msg: "Connection refused"}
	for _, target := range []string{"https://user:pass@example.com/", "https://example.com/health?token=secret"} {
		p := &fakeProber{}
		s := &Scheduler{prober: p}
		s.confirm(context.Background(), &store.Monitor{Type: "http", Target: target, Timeout: 10}, failed)
		if p.asked != nil {
			t.Fatalf("%s was sent to remote locations", target)
		}
	}
}

// A push token could turn every request into an alert.
func TestFlapLimit(t *testing.T) {
	st := &state{}
	now := time.Now()
	n := 0
	for i := range 50 {
		if st.mayAlert(now.Add(time.Duration(i) * time.Second)) {
			n++
		}
	}
	if n != flapLimit {
		t.Fatalf("%d alerts in under a minute, want %d", n, flapLimit)
	}
	if !st.mayAlert(now.Add(flapWindow + time.Minute)) {
		t.Fatal("alerts should resume after the window")
	}
}

// A panic inside one check (e.g. a database driver fed a hostile reply)
// ended the whole hub.
func TestCheckPanicIsContained(t *testing.T) {
	s := &Scheduler{log: slog.New(slog.NewTextHandler(io.Discard, nil))} // no store: probe panics
	r := s.safeProbe(context.Background(), &store.Monitor{Name: "x", Type: "vercel"})
	if r.OK || !strings.Contains(r.Msg, "malformed") {
		t.Fatalf("want a failed result, got %+v", r)
	}
}
