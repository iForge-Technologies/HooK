package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Regression (audit): a failed dead-man ping put the secret URL into the log
// and into Deadman.LastError, and the ping followed redirects.
func TestDeadmanErrorsHideURLAndDontFollowRedirects(t *testing.T) {
	st, dir := newStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(st, Options{DataDir: dir, URL: "http://127.0.0.1:1/ping/SECRET-UUID", Every: time.Hour,
		LastBeat: time.Now, Monitors: func() int { return 0 }}, log)
	m.pass(context.Background())
	if _, dm := m.Last(); dm.LastOK || dm.LastError == "" || strings.Contains(dm.LastError, "SECRET") {
		t.Fatalf("LastError = %q", dm.LastError)
	}

	internal := 0
	in := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internal++ }))
	defer in.Close()
	out := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, in.URL, http.StatusFound) }))
	defer out.Close()
	if err := m.ping(context.Background(), out.URL); err == nil || internal != 0 {
		t.Fatalf("redirect followed: err=%v internal hits=%d", err, internal)
	}
}
