package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"hook/internal/checks"
	"hook/internal/fleet"
	"hook/internal/probe"
	"hook/internal/store"
)

type fakeProber struct {
	out   []fleet.Outcome
	asked *probe.Spec
}

func (f *fakeProber) Probe(_ context.Context, spec probe.Spec, _ int) []fleet.Outcome {
	f.asked = &spec
	return f.out
}

func TestConfirm(t *testing.T) {
	failed := checks.Result{Msg: "Connection refused"}
	m := &store.Monitor{Type: "http", Target: "https://example.com", Timeout: 10}
	ok := func(loc string) fleet.Outcome {
		return fleet.Outcome{Location: loc, Result: probe.Result{OK: true, Latency: 120 * time.Millisecond}}
	}
	bad := func(loc string) fleet.Outcome {
		return fleet.Outcome{Location: loc, Result: probe.Result{Msg: "refused"}}
	}

	// Every other location reaches it: a hub-side problem, not an outage.
	p := &fakeProber{out: []fleet.Outcome{ok("fsn-1"), ok("sgp-1")}}
	s := &Scheduler{prober: p}
	r := s.confirm(context.Background(), m, failed)
	if !r.OK || !r.Partial || !strings.Contains(r.Msg, "reachable from fsn-1 (120 ms), sgp-1") {
		t.Fatalf("all remote OK should be partial: %+v", r)
	}

	// Another location agrees: down stands, with who confirmed it.
	p.out = []fleet.Outcome{bad("fsn-1"), ok("sgp-1")}
	r = s.confirm(context.Background(), m, failed)
	if r.OK || !strings.Contains(r.Msg, "confirmed from fsn-1") || !strings.Contains(r.Msg, "reachable from sgp-1") {
		t.Fatalf("a confirming location should keep it down: %+v", r)
	}

	// No locations answered: fall back to the hub's own result.
	p.out = nil
	if r = s.confirm(context.Background(), m, failed); r.OK || r.Msg != failed.Msg {
		t.Fatalf("no answers should leave the result alone: %+v", r)
	}

	// Never asked: successes, opted-out monitors, monitors with headers, databases.
	p.asked = nil
	s.confirm(context.Background(), m, checks.Result{OK: true})
	s.confirm(context.Background(), &store.Monitor{Type: "http", Config: store.MonitorConfig{NoConfirm: true}}, failed)
	s.confirm(context.Background(), &store.Monitor{Type: "http", Config: store.MonitorConfig{Headers: map[string]string{"Authorization": "x"}}}, failed)
	s.confirm(context.Background(), &store.Monitor{Type: "postgres"}, failed)
	if p.asked != nil {
		t.Fatalf("should not have probed, but asked for %+v", p.asked)
	}
}
