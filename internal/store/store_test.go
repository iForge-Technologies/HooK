package store

import (
	"path/filepath"
	"testing"
	"time"

	"hook/internal/proto"
	"hook/internal/secret"
)

func open(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	box, err := secret.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMonitorRoundTripAndUptime(t *testing.T) {
	s := open(t)
	ch := &Channel{Name: "tg", Type: "telegram", Config: map[string]string{"botToken": "x", "chatId": "1"}, Active: true}
	if err := s.SaveChannel(ch); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{Name: "site", Type: "http", Target: "https://example.com", Interval: 60, RetryInterval: 60, Timeout: 10, Active: true,
		Config: MonitorConfig{Headers: map[string]string{"Authorization": "Bearer secret"}}, ChannelIDs: []int64{ch.ID}}
	if err := s.CreateMonitor(m); err != nil {
		t.Fatal(err)
	}
	got, err := s.Monitor(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Headers["Authorization"] != "Bearer secret" || len(got.ChannelIDs) != 1 || got.Status != "pending" {
		t.Fatalf("round trip lost data: %+v", got)
	}

	now := time.Now().UnixMilli()
	for i, st := range []string{"up", "up", "down", "degraded"} {
		if err := s.InsertHeartbeat(&Heartbeat{MonitorID: m.ID, TS: now - int64(i)*1000, Status: st, Latency: 100}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RefreshRollups(now - 3600_000); err != nil {
		t.Fatal(err)
	}
	up, err := s.Uptime(now - 24*3600_000)
	if err != nil {
		t.Fatal(err)
	}
	if up[m.ID] != 0.75 {
		t.Fatalf("uptime = %v, want 0.75", up[m.ID])
	}

	if err := s.OpenIncident(m.ID, now, "boom"); err != nil {
		t.Fatal(err)
	}
	s.OpenIncident(m.ID, now+1, "again") // must not open a second one
	started, err := s.ResolveIncident(m.ID, now+5000)
	if err != nil || started != now {
		t.Fatalf("resolve: %v %v", started, err)
	}
	if in, _ := s.Incidents(m.ID, 10); len(in) != 1 {
		t.Fatalf("want 1 incident, got %d", len(in))
	}
}

func TestHostRollups(t *testing.T) {
	s := open(t)
	h := &Host{Name: "vps-1", Thresholds: DefaultThresholds()}
	if err := s.CreateHost(h, "hk_test"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.HostByToken("hk_test"); err != nil || got.ID != h.ID {
		t.Fatalf("token lookup: %v %v", got, err)
	}
	base := time.Now().Add(-10*time.Minute).UnixMilli() / 60_000 * 60_000
	for i, cpu := range []float64{10, 30, 50, 90} {
		m := &proto.Metrics{TS: base + int64(i)*5000, CPU: cpu, NetRx: cpu * 100}
		if err := s.InsertMetrics(h.ID, m); err != nil {
			t.Fatal(err)
		}
		s.InsertMetrics(h.ID, m) // replayed frame: ignored
	}
	if err := s.RefreshHostRollups(base); err != nil {
		t.Fatal(err)
	}
	pts, err := s.HostSeries(h.ID, base, "1m")
	if err != nil || len(pts) != 1 {
		t.Fatalf("1m points: %v %v", len(pts), err)
	}
	if pts[0].CPU != 45 || *pts[0].CPUMax != 90 || *pts[0].NetRxMax != 9000 {
		t.Fatalf("1m rollup wrong: cpu=%v max=%v", pts[0].CPU, *pts[0].CPUMax)
	}
	hp, err := s.HostSeries(h.ID, base/3_600_000*3_600_000, "1h")
	if err != nil || len(hp) != 1 || hp[0].CPU != 45 {
		t.Fatalf("1h rollup: %+v %v", hp, err)
	}
	latest, _ := s.LatestMetrics()
	if latest[h.ID].CPU != 90 {
		t.Fatalf("latest = %v", latest[h.ID].CPU)
	}
}

func TestFleetAggregates(t *testing.T) {
	s := open(t)
	m := &Monitor{Name: "api", Type: "http", Target: "https://x.test", Interval: 60, RetryInterval: 60, Timeout: 10, Active: true}
	if err := s.CreateMonitor(m); err != nil {
		t.Fatal(err)
	}
	hour := time.Now().Add(-2*time.Hour).UnixMilli() / 3_600_000 * 3_600_000
	for i, st := range []string{"up", "up", "down", "degraded"} {
		s.InsertHeartbeat(&Heartbeat{MonitorID: m.ID, TS: hour + int64(i)*60_000, Status: st, Latency: 100 * (i + 1)})
	}
	s.RefreshRollups(hour)
	b, err := s.FleetLatency(hour, 3_600_000)
	if err != nil || len(b) != 1 || b[0].Down != 1 || b[0].Up != 3 {
		t.Fatalf("latency buckets: %+v %v", b, err)
	}
	if b[0].Latency != (100+200+400)/3 {
		t.Fatalf("mean latency should skip failed checks, got %d", b[0].Latency)
	}
	up, down, _ := s.FleetUptime(hour, time.Now().UnixMilli())
	if up != 3 || down != 1 {
		t.Fatalf("uptime counts %d/%d", up, down)
	}

	h := &Host{Name: "vps", Thresholds: DefaultThresholds()}
	s.CreateHost(h, "hk_t")
	s.OpenIncident(m.ID, hour, "refused")
	s.OpenHostEvent(h.ID, "cpu", "", hour+1000, 97, "load 3")
	old := hour - 48*3_600_000
	s.OpenHostEvent(h.ID, "offline", "", old, 0, "")
	s.ResolveHostEvent(h.ID, "offline", "", old+1000) // resolved before the window
	feed, err := s.Feed(hour - 3_600_000)
	if err != nil || len(feed) != 2 || feed[0].Kind != "cpu" || feed[1].Source != "monitor" {
		t.Fatalf("feed: %+v %v", feed, err)
	}
}

func TestMaintenanceOccurrence(t *testing.T) {
	base := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC) // 02:00–03:00
	w := &Maintenance{StartsAt: base.UnixMilli(), EndsAt: base.Add(time.Hour).UnixMilli()}
	at := func(d time.Duration) time.Time { return base.Add(d) }
	if w.ActiveAt(at(-time.Minute)) || !w.ActiveAt(at(30*time.Minute)) || w.ActiveAt(at(time.Hour)) {
		t.Fatal("one-off window bounds wrong")
	}
	if _, _, ok := w.Occurrence(at(2 * time.Hour)); ok {
		t.Fatal("a finished one-off window has no next occurrence")
	}
	w.Repeat = "daily"
	if !w.ActiveAt(at(3*24*time.Hour+10*time.Minute)) || w.ActiveAt(at(3*24*time.Hour+90*time.Minute)) {
		t.Fatal("daily repeat wrong")
	}
	s, _, ok := w.Occurrence(at(3*24*time.Hour + 2*time.Hour))
	if !ok || !s.Equal(at(4*24*time.Hour)) {
		t.Fatalf("next daily occurrence = %v", s)
	}
	w.Repeat = "weekly"
	if w.ActiveAt(at(24*time.Hour+10*time.Minute)) || !w.ActiveAt(at(14*24*time.Hour+10*time.Minute)) {
		t.Fatal("weekly repeat wrong")
	}
	w.MonitorIDs, w.HostIDs = []int64{3}, []int64{7}
	if !w.CoversMonitor(3) || w.CoversMonitor(4) || !w.CoversHost(7) {
		t.Fatal("coverage wrong")
	}
	w.Everything = true
	if !w.CoversMonitor(99) || !w.CoversHost(99) {
		t.Fatal("everything should cover all")
	}
}

func TestStatusPagesAndDailyUptime(t *testing.T) {
	s := open(t)
	p := &StatusPage{Slug: "acme", Title: "Acme status", MonitorIDs: []int64{1}}
	if err := s.SaveStatusPage(p); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveStatusPage(&StatusPage{Slug: "acme", Title: "dup"}); err != ErrSlugTaken {
		t.Fatalf("duplicate slug: %v", err)
	}
	m := &Monitor{Name: "api", Type: "http", Target: "https://x.test", Interval: 60, RetryInterval: 60, Timeout: 10, Active: true}
	s.CreateMonitor(m)
	day := time.Now().Add(-48*time.Hour).UnixMilli() / 86_400_000 * 86_400_000
	for i, st := range []string{"up", "up", "up", "down"} {
		s.InsertHeartbeat(&Heartbeat{MonitorID: m.ID, TS: day + int64(i)*3_600_000, Status: st, Latency: 100})
	}
	s.RefreshRollups(day)
	days, err := s.DailyUptime([]int64{m.ID}, day)
	if err != nil || len(days[m.ID]) != 1 || days[m.ID][0].Up != 3 || days[m.ID][0].Down != 1 || days[m.ID][0].Day != day {
		t.Fatalf("daily uptime: %+v %v", days, err)
	}
}

func TestBackupAndSettings(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Load("", dir)
	s, err := Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSecretSetting("vapid", []byte("private")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.SecretSetting("vapid"); err != nil || string(v) != "private" {
		t.Fatalf("secret setting: %q %v", v, err)
	}
	if raw, _ := s.Setting("vapid"); string(raw) == "private" {
		t.Fatal("secret settings must be sealed at rest")
	}
	if err := s.Ping(); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{Name: "api", Type: "http", Target: "https://x.test", Interval: 60, RetryInterval: 60, Timeout: 10, Active: true}
	s.CreateMonitor(m)
	dst := filepath.Join(t.TempDir(), "b", "hook.db")
	if err := s.Backup(dst); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(dst); err == nil {
		t.Fatal("backup must not overwrite an existing file")
	}
	// The copy opens as a normal Hook database with the same key.
	copyStore, err := Open(filepath.Dir(dst), box)
	if err != nil {
		t.Fatal(err)
	}
	defer copyStore.Close()
	got, err := copyStore.Monitor(m.ID)
	if err != nil || got.Name != "api" {
		t.Fatalf("backup content: %+v %v", got, err)
	}
}

func TestKioskLinks(t *testing.T) {
	s := open(t)
	k, err := s.CreateKioskLink("Office TV", "tok-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.KioskByToken("tok-wrong"); err != ErrNotFound {
		t.Fatalf("wrong token: %v", err)
	}
	got, err := s.KioskByToken("tok-123")
	if err != nil || got.ID != k.ID || got.LastSeen == 0 {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if err := s.DeleteKioskLink(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KioskByToken("tok-123"); err != ErrNotFound {
		t.Fatalf("a revoked link must stop working: %v", err)
	}
}

func TestTrafficCountsEachFrameOnce(t *testing.T) {
	s := open(t)
	h := &Host{Name: "web", Thresholds: DefaultThresholds()}
	if err := s.CreateHost(h, "tok-web"); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC).UnixMilli()
	frame := &proto.Metrics{TS: day + 1000, NetRxBytes: 1000, NetTxBytes: 200, GPU: 40, GPUMem: 12.5}
	if err := s.InsertMetrics(h.ID, frame); err != nil {
		t.Fatal(err)
	}
	s.InsertMetrics(h.ID, frame) // replayed after a reconnect
	s.InsertMetrics(h.ID, &proto.Metrics{TS: day + 6000, NetRxBytes: 500, NetTxBytes: 100})
	days, err := s.Traffic(h.ID, day)
	if err != nil || len(days) != 1 || days[0].Rx != 1500 || days[0].Tx != 300 {
		t.Fatalf("traffic = %+v, %v", days, err)
	}
	pts, _ := s.HostSeries(h.ID, day, "raw")
	if len(pts) != 2 || pts[0].GPU != 40 || pts[0].GPUMem != 12.5 {
		t.Fatalf("gpu not stored: %+v", pts)
	}
}

func TestCycleStart(t *testing.T) {
	at := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	if got := CycleStart(at, 15); !got.Equal(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("before reset day: %v", got)
	}
	if got := CycleStart(at, 0); !got.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("default reset day: %v", got)
	}
}
