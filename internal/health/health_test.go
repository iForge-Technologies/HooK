package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hook/internal/secret"
	"hook/internal/store"
)

func newStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	box, _ := secret.Load("", dir)
	st, err := store.Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

func TestDeadman(t *testing.T) {
	st, dir := newStore(t)
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
	}))
	defer srv.Close()

	beat := time.Now()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(st, Options{DataDir: dir, URL: srv.URL + "/ping", FailURL: srv.URL + "/ping/fail", Every: time.Hour,
		LastBeat: func() time.Time { return beat }, Monitors: func() int { return 3 }}, log)

	run := func() { m.pass(context.Background()) }
	run()
	if r, dm := m.Last(); !r.OK || !dm.LastOK || dm.SentFail || dm.URLHost == "" {
		t.Fatalf("healthy pass: %+v %+v", r, dm)
	}

	// A stalled scheduler: unhealthy, so the fail URL is pinged instead.
	m.started = time.Now().Add(-time.Hour)
	beat = time.Now().Add(-20 * time.Minute)
	run()
	r, dm := m.Last()
	if r.OK || r.Checks["scheduler"].OK || !dm.SentFail {
		t.Fatalf("stalled scheduler should be unhealthy: %+v %+v", r, dm)
	}

	// Without a fail URL an unhealthy hub stays silent, so the service notices.
	m.fail = ""
	run()
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 || hits[0] != "/ping" || hits[1] != "/ping/fail" {
		t.Fatalf("pings = %v", hits)
	}
}

func TestNoMonitorsIsHealthy(t *testing.T) {
	st, dir := newStore(t)
	m := New(st, Options{DataDir: dir, LastBeat: func() time.Time { return time.Time{} }, Monitors: func() int { return 0 }},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if r := m.Check(); !r.OK || r.Checks["disk"].Detail == "" {
		t.Fatalf("fresh hub: %+v", r)
	}
}
