package fleet

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"hook/internal/alert"
	"hook/internal/events"
	"hook/internal/proto"
	"hook/internal/secret"
	"hook/internal/store"
)

func setup(t *testing.T) (*Fleet, *store.Store, *store.Host) {
	t.Helper()
	dir := t.TempDir()
	box, _ := secret.Load("", dir)
	st, err := store.Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := New(st, alert.NewDispatcher(st, "", log), events.NewBroker(), log)
	h := &store.Host{Name: "vps", Thresholds: store.Thresholds{CPU: 90, Mem: 90, Disk: 85, Minutes: 2, Offline: true}}
	if err := st.CreateHost(h, "hk_x"); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	return f, st, h
}

// feed delivers a frame that arrives live, a second after it was sampled.
func feed(t *testing.T, f *Fleet, id int64, ts time.Time, cpu float64) {
	t.Helper()
	f.now = func() time.Time { return ts.Add(time.Second) }
	send(t, f, id, ts, cpu)
}

// send delivers a frame at the fleet's current clock.
func send(t *testing.T, f *Fleet, id int64, ts time.Time, cpu float64) {
	t.Helper()
	b, _ := json.Marshal(proto.Metrics{TS: ts.UnixMilli(), CPU: cpu, MemTotal: 100, MemUsed: 10})
	if err := f.handle(id, envelope{Type: proto.TypeMetrics, Data: b}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
}

func openKinds(t *testing.T, st *store.Store, id int64) map[string]store.HostEvent {
	t.Helper()
	evs, err := st.HostEvents(id, 50)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.HostEvent{}
	for _, e := range evs {
		if e.ResolvedAt == nil {
			out[e.Kind] = e
		}
	}
	return out
}

func TestSustainedThreshold(t *testing.T) {
	f, st, h := setup(t)
	t0 := time.Now().Add(-10 * time.Minute)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

	// 95% for 1 minute: not long enough to alert (limit holds 2 min).
	for s := 0; s <= 60; s += 5 {
		feed(t, f, h.ID, at(s), 95)
	}
	if len(openKinds(t, st, h.ID)) != 0 {
		t.Fatal("alerted before the sustain window")
	}
	// A dip resets the window.
	feed(t, f, h.ID, at(65), 40)
	for s := 70; s <= 190; s += 5 {
		feed(t, f, h.ID, at(s), 97)
	}
	ev, ok := openKinds(t, st, h.ID)["cpu"]
	if !ok {
		t.Fatal("expected an open cpu event after 2 sustained minutes")
	}
	if ev.StartedAt != at(70).UnixMilli() {
		t.Fatalf("event should start when the breach began, got %v", time.UnixMilli(ev.StartedAt))
	}
	feed(t, f, h.ID, at(195), 99)
	if openKinds(t, st, h.ID)["cpu"].Peak != 99 {
		t.Fatal("peak not tracked")
	}

	// 88% is under the limit but inside the hysteresis band: stays open.
	for s := 200; s <= 320; s += 5 {
		feed(t, f, h.ID, at(s), 88)
	}
	if _, ok := openKinds(t, st, h.ID)["cpu"]; !ok {
		t.Fatal("resolved inside the hysteresis band")
	}
	// Clearly lower for a full minute resolves it.
	for s := 325; s <= 390; s += 5 {
		feed(t, f, h.ID, at(s), 30)
	}
	if _, ok := openKinds(t, st, h.ID)["cpu"]; ok {
		t.Fatal("expected the cpu event to resolve")
	}
}

func TestReplayedFramesDontAlert(t *testing.T) {
	f, st, h := setup(t)
	old := time.Now().Add(-90 * time.Minute) // buffered while the hub was unreachable
	for s := 0; s <= 600; s += 5 {
		send(t, f, h.ID, old.Add(time.Duration(s)*time.Second), 100)
	}
	if len(openKinds(t, st, h.ID)) != 0 {
		t.Fatal("backlog frames should be stored but never alert")
	}
	pts, _ := st.HostSeries(h.ID, old.UnixMilli(), "raw")
	if len(pts) != 121 {
		t.Fatalf("stored %d frames, want 121", len(pts))
	}
}

// Regression: a reconnecting agent's hello flipped the host online without
// refreshing lastSeen, so a watchdog tick before the first metrics frame
// marked it offline again and opened a duplicate offline event.
func TestReconnectDoesNotReopenOffline(t *testing.T) {
	f, st, h := setup(t)
	t0 := time.Now().Add(-10 * time.Minute)
	feed(t, f, h.ID, t0, 10) // online

	f.sweep(t0.Add(2 * time.Minute)) // silent: goes offline
	if k := openKinds(t, st, h.ID); len(k) != 1 {
		t.Fatalf("want one open offline event, got %v", k)
	}

	back := t0.Add(3 * time.Minute)
	f.now = func() time.Time { return back }
	hello, _ := json.Marshal(proto.HostInfo{Hostname: "vps"})
	if err := f.handle(h.ID, envelope{Type: proto.TypeHello, Data: hello}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	f.sweep(back.Add(5 * time.Second)) // watchdog fires before any metrics frame

	evs, _ := st.HostEvents(h.ID, 10)
	if len(evs) != 1 || evs[0].ResolvedAt == nil {
		t.Fatalf("want exactly one resolved offline event, got %+v", evs)
	}
}

func snap(t *testing.T, f *Fleet, id int64, at time.Time, cs []proto.Container, us []proto.Unit) {
	t.Helper()
	f.now = func() time.Time { return at }
	b, _ := json.Marshal(proto.Snapshot{TS: at.UnixMilli(), Docker: &proto.Docker{Available: true, Containers: cs}, Units: us})
	if err := f.handle(id, envelope{Type: proto.TypeSnapshot, Data: b}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
}

func TestContainersAndServices(t *testing.T) {
	f, st, h := setup(t)
	h.Thresholds.Units = []string{"nginx.service"}
	h.Thresholds.Containers, h.Thresholds.Services = true, true
	f.SetHost(h)
	t0 := time.Now().Add(-time.Hour)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	run := func(name string, restarts int) proto.Container {
		return proto.Container{Name: name, State: "running", Status: "Up", Restarts: restarts}
	}
	nginx := func(active string) []proto.Unit {
		return []proto.Unit{{Name: "nginx.service", Active: active, Sub: "x"}}
	}

	// An old exited container never alerts; only ones we saw running do.
	snap(t, f, h.ID, at(0), []proto.Container{run("web", 0), {Name: "old", State: "exited", Status: "Exited (0)"}}, nginx("active"))
	if len(openKinds(t, st, h.ID)) != 0 {
		t.Fatal("nothing should be open yet")
	}
	snap(t, f, h.ID, at(1), []proto.Container{{Name: "web", State: "exited", Status: "Exited (137)"}, {Name: "old", State: "exited"}}, nginx("failed"))
	evs, _ := st.HostEvents(h.ID, 10)
	open := map[string]string{}
	for _, e := range evs {
		if e.ResolvedAt == nil {
			open[e.Kind+"/"+e.Subject] = e.Detail
		}
	}
	if len(open) != 2 || open["container/web"] != "stopped (Exited (137))" || open["service/nginx.service"] != "failed (x)" {
		t.Fatalf("want web stopped and nginx failed, got %v", open)
	}

	// A snapshot from a freshly restarted agent without units must not clear nginx.
	snap(t, f, h.ID, at(2), []proto.Container{{Name: "web", State: "exited"}}, nil)
	snap(t, f, h.ID, at(3), []proto.Container{run("web", 1)}, nginx("active"))
	evs, _ = st.HostEvents(h.ID, 10)
	for _, e := range evs {
		if e.ResolvedAt == nil {
			t.Fatalf("everything should be resolved, still open: %+v", e)
		}
	}
	if len(evs) != 2 {
		t.Fatalf("no duplicate events expected, got %d", len(evs))
	}

	// Unhealthy is a problem even without a prior state change.
	snap(t, f, h.ID, at(4), []proto.Container{{Name: "web", State: "running", Health: "unhealthy", Restarts: 1}}, nginx("active"))
	if _, ok := openKinds(t, st, h.ID)["container"]; !ok {
		t.Fatal("unhealthy container should open an event")
	}
}

func TestCrashRestarted(t *testing.T) {
	running := proto.Container{Name: "web", State: "running", Restarts: 3}
	for _, tc := range []struct {
		name       string
		c          proto.Container
		wasRunning bool
		prev       int
		known      bool
		want       bool
	}{
		{"crashed and auto-restarted between snapshots", running, true, 2, true, true},
		{"same count", running, true, 3, true, false},
		{"was stopped, now started (recovery or docker start)", running, false, 2, true, false},
		{"first snapshot after hub start", running, true, 0, false, false},
		{"not running now", proto.Container{State: "exited", Restarts: 5}, true, 2, true, false},
	} {
		if got := crashRestarted(tc.c, tc.wasRunning, tc.prev, tc.known); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
