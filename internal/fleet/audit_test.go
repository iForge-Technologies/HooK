package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"hook/internal/probe"
	"hook/internal/proto"
)

// Regression tests for the 2026-10-27 security audit: one agent token must
// only affect its own server, and only within bounds.

func TestSanitizeMetrics(t *testing.T) {
	m := proto.Metrics{CPU: math.Inf(1), IOWait: math.NaN(), DiskPct: 400, Load1: -3,
		MemUsed: 1e300, MemTotal: 1e-10, NetRx: math.Inf(1), NetRxBytes: math.MaxUint64}
	sanitize(&m)
	if _, err := json.Marshal(m); err != nil {
		t.Fatalf("sanitized frame still can't be encoded: %v", err)
	}
	if m.CPU != 100 || m.IOWait != 0 || m.DiskPct != 100 || m.Load1 != 0 || m.MemUsed > m.MemTotal || m.NetRx != maxValue || m.NetRxBytes != maxValue {
		t.Fatalf("not clamped: %+v", m)
	}
}

// Extreme values from one agent blanked the kiosk board and the fleet list.
func TestExtremeMetricsStayEncodable(t *testing.T) {
	f, st, h := setup(t)
	now := time.Now()
	f.now = func() time.Time { return now }
	b := []byte(`{"ts":` + fmt.Sprint(now.UnixMilli()) + `,"cpu":1.7e308,"memUsed":1e300,"memTotal":1e-10,"diskPct":10}`)
	if err := f.handle(h.ID, envelope{Type: proto.TypeMetrics, Data: b}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	m, _ := f.Live(h.ID)
	if _, err := json.Marshal(m); err != nil {
		t.Fatalf("live frame can't be encoded: %v", err)
	}
	pts, _ := st.HostSeries(h.ID, 0, "raw")
	if len(pts) != 1 || math.IsInf(pts[0].CPU, 0) {
		t.Fatalf("stored %+v", pts)
	}
}

// Distinct millisecond timestamps let one agent add rows at any rate.
func TestAtMostOneRowPerSecond(t *testing.T) {
	f, st, h := setup(t)
	base := time.Now().Add(-time.Minute).Truncate(time.Second)
	f.now = func() time.Time { return base.Add(2 * time.Second) }
	for i := range 500 {
		send(t, f, h.ID, base.Add(time.Duration(i)*time.Millisecond), 10)
	}
	pts, _ := st.HostSeries(h.ID, 0, "raw")
	if len(pts) != 1 {
		t.Fatalf("500 frames in one second stored %d rows, want 1", len(pts))
	}
}

func TestFrameBudgetSurvivesReconnects(t *testing.T) {
	f, _, h := setup(t)
	now := time.Now()
	n := 0
	for f.allowFrame(h.ID, now) {
		n++
	}
	if n != int(frameBurst) {
		t.Fatalf("burst allowed %d frames, want %d", n, int(frameBurst))
	}
	if !f.allowFrame(h.ID, now.Add(2*time.Second)) {
		t.Fatal("budget should refill over time")
	}
}

// One snapshot listing 20,000 crash-looping containers opened 20,000 events
// and sent 20,000 alerts.
func TestContainerFloodIsBudgeted(t *testing.T) {
	f, st, h := setup(t)
	h.Thresholds.Containers = true
	f.SetHost(h)
	var cs []proto.Container
	for i := range 20_000 {
		cs = append(cs, proto.Container{Name: fmt.Sprint("c", i), State: "restarting"})
	}
	snap(t, f, h.ID, time.Now(), cs, nil)
	evs, _ := st.HostEvents(h.ID, 100_000)
	if len(evs) > eventBudget+1 {
		t.Fatalf("one snapshot opened %d events, want at most %d", len(evs), eventBudget+1)
	}
	found := false
	for _, e := range evs {
		found = found || e.Subject == summarySubject
	}
	if !found {
		t.Fatal("expected one summary event for the suppressed problems")
	}
}

// An agent could make itself a confirmation location by saying probes:true.
func TestProbeLocationNeedsAdminApproval(t *testing.T) {
	f, _, h := setup(t)
	h.Info.Probes = true
	f.SetHost(h)
	f.mu.Lock()
	f.conns[h.ID] = &agentConn{}
	f.mu.Unlock()
	if locs := f.Locations(); len(locs) != 0 {
		t.Fatalf("self-declared agent became a location: %v", locs)
	}
	f.mu.Lock() // as SetHost after the admin enables it (without sending to the fake connection)
	f.live[h.ID].host.ProbeLocation = true
	f.mu.Unlock()
	if locs := f.Locations(); len(locs) != 1 {
		t.Fatalf("approved agent not listed: %v", locs)
	}
}

func TestProbeAnswerOnlyFromItsServer(t *testing.T) {
	f, _, _ := setup(t)
	ch := make(chan Outcome, 1)
	f.pending["p1"] = pendingProbe{hostID: 1, location: "a", ch: ch}
	f.deliver(2, proto.ProbeResult{ID: "p1", OK: true})
	select {
	case <-ch:
		t.Fatal("another server answered the probe")
	default:
	}
	f.deliver(1, proto.ProbeResult{ID: "p1", OK: true})
	if o := <-ch; !o.OK {
		t.Fatal("the probed server's answer was lost")
	}
}

// A connection whose token check raced with a deletion survived it.
func TestDeletedServerCannotConnect(t *testing.T) {
	f, _, h := setup(t)
	f.Remove(h.ID) // as deleteServer does; the store row is still here
	srv := httptest.NewServer(httpHandler(f))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{HTTPHeader: map[string][]string{"Authorization": {"Bearer hk_x"}}})
	if err != nil {
		return // refused outright is fine too
	}
	defer ws.CloseNow()
	if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
		t.Fatalf("want the connection closed as stale, got %v", err)
	}
}

func TestForRemoteNeverCarriesURLCredentials(t *testing.T) {
	s := probe.Spec{Type: "http", Target: "https://user:pass@example.com/health", Headers: map[string]string{"X": "y"}}.ForRemote()
	if strings.Contains(s.Target, "pass") || s.Headers != nil {
		t.Fatalf("credentials left in remote spec: %+v", s)
	}
}

func httpHandler(f *Fleet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { f.HandleWS(w, r, "127.0.0.1") }
}
