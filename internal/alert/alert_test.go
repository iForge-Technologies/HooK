package alert

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression tests for the 2026-10-27 security audit.

// Alert deliveries followed a destination's redirect to the hub's network.
func TestDeliveryDoesNotFollowRedirects(t *testing.T) {
	internal := 0
	in := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internal++ }))
	defer in.Close()
	out := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, in.URL, http.StatusTemporaryRedirect)
	}))
	defer out.Close()
	d := NewDispatcher(nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := d.postJSON(context.Background(), out.URL, map[string]string{"a": "b"}, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") || internal != 0 {
		t.Fatalf("err=%v internal hits=%d; want a failed delivery and no hit", err, internal)
	}
}

// Failed deliveries logged the whole secret webhook URL.
func TestDeliveryErrorHidesURL(t *testing.T) {
	d := NewDispatcher(nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := d.postJSON(context.Background(), "http://127.0.0.1:1/services/T000/B000/SECRETPART?token=SECRETQ", map[string]string{}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error leaks the URL: %v", err)
	}
}
