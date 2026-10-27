package speedtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunRespectsBudget(t *testing.T) {
	var served, received atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f1a2b3c4d5e6f70-FRA")
		w.Header().Set("Server-Timing", "cfSpeedEdge;dur=0, cfSpeedWorker;dur=0")
		switch r.URL.Path {
		case "/__down":
			n, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
			buf := make([]byte, 64<<10)
			for n > 0 {
				k := min(n, int64(len(buf)))
				if _, err := w.Write(buf[:k]); err != nil {
					return
				}
				served.Add(k)
				n -= k
			}
		case "/__up":
			k, _ := io.Copy(io.Discard, r.Body)
			received.Add(k)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	o := Options{Base: srv.URL, Duration: 3 * time.Second, MaxDown: 3 << 20, MaxUp: 2 << 20, Streams: 3}
	r := Run(context.Background(), o)
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	if r.DownBps <= 0 || r.UpBps <= 0 || r.LatencyMs < 0 { // localhost latency rounds to 0
		t.Fatalf("result: %+v", r)
	}
	if r.Server != "Cloudflare FRA" {
		t.Fatalf("server = %q", r.Server)
	}
	if served.Load() != 3<<20 || received.Load() != 2<<20 {
		t.Fatalf("budget: served %d, received %d", served.Load(), received.Load())
	}
	if r.Bytes != 5<<20 {
		t.Fatalf("bytes = %d", r.Bytes)
	}
}

func TestRunReportsUnreachable(t *testing.T) {
	r := Run(context.Background(), Options{Base: "http://127.0.0.1:1", Duration: time.Second, MaxDown: 1, MaxUp: 1, Streams: 1})
	if r.Error == "" || r.DownBps != 0 {
		t.Fatalf("expected an error: %+v", r)
	}
}

func TestServerTime(t *testing.T) {
	h := http.Header{}
	h.Add("Server-Timing", "cfSpeedEdge;dur=10, cfSpeedWorker;dur=53")
	h.Add("Server-Timing", `cfL4;desc="?proto=TCP&rtt=53661"`)
	if got := serverTime(h); got != 63 {
		t.Fatalf("serverTime = %v", got)
	}
}
