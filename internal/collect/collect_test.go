package collect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseUnits(t *testing.T) {
	boot := time.Unix(1_700_000_000, 0)
	out := "Id=nginx.service\nLoadState=loaded\nActiveState=active\nSubState=running\nNRestarts=2\nStateChangeTimestampMonotonic=5000000\n\n" +
		"Id=ghost.service\nLoadState=not-found\nActiveState=inactive\nSubState=dead\nNRestarts=0\nStateChangeTimestampMonotonic=0\n"
	us := parseUnits(out, boot)
	if len(us) != 2 {
		t.Fatalf("want 2 units, got %+v", us)
	}
	if us[0].Name != "nginx.service" || us[0].Active != "active" || us[0].Restarts != 2 || us[0].Since != boot.Add(5*time.Second).UnixMilli() {
		t.Fatalf("nginx: %+v", us[0])
	}
	if us[1].Active != "not-found" {
		t.Fatalf("missing unit should read not-found: %+v", us[1])
	}
	for _, bad := range []string{"--help", "-x", "a;rm -rf /", "$(id)", ""} {
		if UnitName.MatchString(bad) {
			t.Errorf("unit name %q should be rejected", bad)
		}
	}
	for _, good := range []string{"nginx", "postgresql@16-main.service", "dev-disk-by\x2duuid.device"} {
		if !UnitName.MatchString(good) {
			t.Errorf("unit name %q should be accepted", good)
		}
	}
}

// A fake Docker Engine API reached through DOCKER_HOST=tcp://… (the socket-proxy setup).
func TestContainers(t *testing.T) {
	total := uint64(1_000_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/containers/json":
			enc.Encode([]any{
				map[string]any{"Id": "bbbbbbbbbbbbbbbb", "Names": []string{"/old-job"}, "Image": "busybox", "State": "exited", "Status": "Exited (1) 2 days ago"},
				map[string]any{"Id": "aaaaaaaaaaaaaaaa", "Names": []string{"/web"}, "Image": "nginx:1.27", "State": "running", "Status": "Up 3 hours (unhealthy)"},
			})
		case strings.HasSuffix(r.URL.Path, "/json"):
			enc.Encode(map[string]any{"RestartCount": 3, "State": map[string]any{"Health": map[string]any{"Status": "unhealthy"}}})
		case strings.HasSuffix(r.URL.Path, "/stats"):
			total += 500_000_000 // half a core-second per 2 s of system time on 1 cpu → 25%
			enc.Encode(map[string]any{
				"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": total}, "system_cpu_usage": total * 4, "online_cpus": 1},
				"memory_stats": map[string]any{"usage": 300 << 20, "limit": 1 << 30, "stats": map[string]any{"inactive_file": 100 << 20}},
			})
		}
	}))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))

	c := New("test")
	d := c.containers(context.Background())
	if !d.Available || len(d.Containers) != 2 {
		t.Fatalf("docker: %+v", d)
	}
	web := d.Containers[0]
	if web.Name != "web" || web.Health != "unhealthy" || web.Restarts != 3 || web.Mem != 200<<20 || web.ID != "aaaaaaaaaaaa" {
		t.Fatalf("running containers should sort first with stats: %+v", web)
	}
	if web.CPU != 0 {
		t.Fatal("first sample has no CPU delta yet")
	}
	d = c.containers(context.Background())
	if d.Containers[0].CPU <= 0 {
		t.Fatalf("second sample should have CPU %%: %+v", d.Containers[0])
	}
	if d.Containers[1].Mem != 0 {
		t.Fatal("stopped containers have no stats")
	}
}
