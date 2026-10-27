package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"hook/internal/alert"
	"hook/internal/auth"
	"hook/internal/config"
	"hook/internal/events"
	"hook/internal/fleet"
	"hook/internal/maint"
	"hook/internal/monitor"
	"hook/internal/secret"
	"hook/internal/store"
)

// Regression tests for the 2026-10-27 security audit, through the real API.

type testHub struct {
	st     *store.Store
	ev     *events.Broker
	h      http.Handler
	userID int64
}

func newHub(t *testing.T) *testHub {
	t.Helper()
	dir := t.TempDir()
	box, _ := secret.Load("", dir)
	st, err := store.Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := events.NewBroker()
	al := alert.NewDispatcher(st, "", log)
	cal := maint.New(st, log)
	fl := fleet.New(st, al, ev, log)
	sched := monitor.New(st, al, ev, fl, cal, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("correct-horse-1")
	u, err := st.CreateUser("admin", hash)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(config.Config{}, st, sched, fl, cal, nil, nil, al, ev, fstest.MapFS{"index.html": {Data: []byte("x")}}, log)
	return &testHub{st: st, ev: ev, h: srv.Handler(), userID: u.ID}
}

// session creates a signed-in session and returns its token.
func (th *testHub) session(t *testing.T, token string) string {
	t.Helper()
	if err := th.st.CreateSession(token, th.userID, false, time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	return token
}

func (th *testHub) do(method, path, token, body, ip string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	}
	if ip != "" {
		r.RemoteAddr = ip + ":1234"
	}
	w := httptest.NewRecorder()
	th.h.ServeHTTP(w, r)
	return w
}

// TOTP guesses were limited per source IP only; rotating addresses gave
// unlimited tries on one pending sign-in.
func TestTOTPAttemptsArePerSignIn(t *testing.T) {
	th := newHub(t)
	th.st.SetTOTP(th.userID, auth.NewTOTPSecret(), true)
	if err := th.st.CreateSession("pending", th.userID, true, time.Minute, "", ""); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxMFAFails; i++ {
		w := th.do("POST", "/api/auth/totp", "pending", `{"code":"12345x"}`, "198.51.100."+string(rune('0'+i)))
		want := "Invalid code"
		if i == maxMFAFails {
			want = "Sign-in expired"
		}
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("attempt %d from a fresh address: %d %s", i, w.Code, w.Body)
		}
	}
	if _, err := th.st.GetSession("pending"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the pending sign-in survived repeated wrong codes")
	}
}

// Logins counted failures only after hashing, so a burst all passed.
func TestLoginCountsAttemptsUpFront(t *testing.T) {
	th := newHub(t)
	got429 := false
	for range 12 {
		w := th.do("POST", "/api/auth/login", "", `{"username":"admin","password":"wrong-password"}`, "203.0.113.9")
		got429 = got429 || w.Code == http.StatusTooManyRequests
	}
	if !got429 {
		t.Fatal("12 failed logins from one address were never limited")
	}
}

// A push token could drive unlimited alert/incident cycles.
func TestPushIsRateLimited(t *testing.T) {
	th := newHub(t)
	tok := th.session(t, "s")
	w := th.do("POST", "/api/monitors", tok, `{"name":"job","type":"push","interval":3600}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create push monitor: %d %s", w.Code, w.Body)
	}
	var created struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &created)
	m, _ := th.st.Monitor(created.ID)
	if w := th.do("GET", "/api/push/"+m.Config.PushToken+"?status=down", "", "", ""); w.Code != http.StatusOK {
		t.Fatalf("first push: %d %s", w.Code, w.Body)
	}
	if w := th.do("GET", "/api/push/"+m.Config.PushToken+"?status=up", "", "", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second push within 10 s: %d, want 429", w.Code)
	}
}

// Sending a masked header back with a new URL re-sent the stored secret there.
func TestMaskedHeaderStaysWithItsURL(t *testing.T) {
	th := newHub(t)
	tok := th.session(t, "s")
	w := th.do("POST", "/api/monitors", tok, `{"name":"api","type":"http","target":"https://api.example.com/","config":{"headers":{"Authorization":"Bearer real"}}}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	body := `{"name":"api","type":"http","target":"https://attacker.example/","config":{"headers":{"Authorization":"` + mask + `"}}}`
	if w := th.do("PUT", "/api/monitors/1", tok, body, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("masked header to a new URL: %d %s", w.Code, w.Body)
	}
	body = `{"name":"api renamed","type":"http","target":"https://api.example.com/","config":{"headers":{"Authorization":"` + mask + `"}}}`
	if w := th.do("PUT", "/api/monitors/1", tok, body, ""); w.Code != http.StatusOK {
		t.Fatalf("masked header, same URL: %d %s", w.Code, w.Body)
	}
	if m, _ := th.st.Monitor(1); m.Config.Headers["Authorization"] != "Bearer real" {
		t.Fatal("kept header lost")
	}
}

func TestChannelSecretStaysWithItsDestination(t *testing.T) {
	old := &store.Channel{Type: "ntfy", Config: map[string]string{"server": "https://ntfy.sh", "topic": "t", "token": "tk_real"}}
	moved := map[string]string{"server": "https://attacker.example", "topic": "t", "token": mask}
	if err := unmask(moved, "ntfy", old); err == nil {
		t.Fatal("stored token followed a new server")
	}
	same := map[string]string{"server": "https://ntfy.sh", "topic": "other", "token": mask}
	if err := unmask(same, "ntfy", old); err != nil || same["token"] != "tk_real" {
		t.Fatalf("same server: %v %q", err, same["token"])
	}
}

// Open event streams kept running after their session was revoked.
func TestEventStreamEndsWithItsSession(t *testing.T) {
	th := newHub(t)
	srv := httptest.NewServer(th.h)
	defer srv.Close()
	tok := th.session(t, "stolen")
	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	rd.ReadString('\n') // "retry: …"

	if w := th.do("POST", "/api/auth/logout", tok, `{}`, ""); w.Code != http.StatusOK {
		t.Fatalf("logout: %d", w.Code)
	}
	th.ev.Publish(events.Event{Type: "beat", Data: map[string]any{"secret": "after-logout"}})

	done := make(chan string, 1)
	go func() {
		rest, _ := io.ReadAll(rd)
		done <- string(rest)
	}()
	select {
	case rest := <-done:
		if strings.Contains(rest, "after-logout") {
			t.Fatal("a revoked session received an event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after its session was revoked")
	}
}

// Database monitors sent the password over unverified connections by default.
func TestDatabaseMonitorNeedsVerifiedTLS(t *testing.T) {
	th := newHub(t)
	tok := th.session(t, "s")
	body := `{"name":"db","type":"postgres","config":{"dsn":"postgres://app:pw@db.example.com/x?sslmode=require"}}`
	if w := th.do("POST", "/api/monitors", tok, body, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unverified TLS accepted: %d %s", w.Code, w.Body)
	}
	body = `{"name":"db","type":"postgres","config":{"dsn":"postgres://app:pw@db.example.com/x?sslmode=require","ignoreTls":true}}`
	if w := th.do("POST", "/api/monitors", tok, body, ""); w.Code != http.StatusCreated {
		t.Fatalf("explicit opt-in refused: %d %s", w.Code, w.Body)
	}
}

// writeJSON committed 200 before encoding, so an unencodable value became an
// empty 200 (this blanked the fleet list).
func TestWriteJSONFailsClosed(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]float64{"cpu": math.Inf(1)})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", w.Code)
	}
}
