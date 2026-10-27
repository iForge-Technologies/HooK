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

func TestDockerDetail(t *testing.T) {
	var reads, rx uint64 = 0, 0
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/containers/json":
			enc.Encode([]any{map[string]any{
				"Id": "cccccccccccccccc", "Names": []string{"/db"}, "Image": "postgres:17", "State": "running", "Status": "Up 1 hour", "Created": 1_790_000_000,
				"Ports": []any{
					map[string]any{"IP": "0.0.0.0", "PrivatePort": 5432, "PublicPort": 5432, "Type": "tcp"},
					map[string]any{"IP": "::", "PrivatePort": 5432, "PublicPort": 5432, "Type": "tcp"},
					map[string]any{"PrivatePort": 8008, "Type": "tcp"},
				},
				"Mounts":          []any{map[string]any{"Type": "volume", "Name": "pgdata", "Source": "/var/lib/docker/volumes/pgdata/_data", "Destination": "/var/lib/postgresql/data", "RW": true}},
				"NetworkSettings": map[string]any{"Networks": map[string]any{"backend": map[string]any{}}},
			}})
		case r.URL.Path == "/version":
			enc.Encode(map[string]any{"Version": "27.3.1"})
		case r.URL.Path == "/system/df":
			enc.Encode(map[string]any{
				"LayersSize": 900 << 20,
				"Images": []any{
					map[string]any{"Id": "sha256:1111111111111111aaaa", "RepoTags": []string{"postgres:17"}, "Size": 400 << 20, "SharedSize": 100 << 20, "Containers": 1},
					map[string]any{"Id": "sha256:2222222222222222bbbb", "RepoTags": []string{"<none>:<none>"}, "Size": 300 << 20, "SharedSize": 100 << 20, "Containers": 0},
				},
				"Containers": []any{map[string]any{"Id": "cccccccccccccccc", "SizeRw": 5 << 20, "State": "running"}},
				"Volumes": []any{
					map[string]any{"Name": "pgdata", "Driver": "local", "UsageData": map[string]any{"Size": 2 << 30, "RefCount": 1}},
					map[string]any{"Name": strings.Repeat("ab", 32), "Driver": "local", "UsageData": map[string]any{"Size": 10 << 20, "RefCount": 0}},
				},
				"BuildCache": []any{map[string]any{"Size": 50 << 20, "InUse": false, "Shared": false}},
			})
		case strings.HasSuffix(r.URL.Path, "/json"):
			enc.Encode(map[string]any{"RestartCount": 0, "State": map[string]any{"StartedAt": "2026-10-27T11:00:00Z"}})
		case strings.HasSuffix(r.URL.Path, "/stats"):
			reads++
			rx += 10_000_000 // 10 MB per 10 s → 1 MB/s
			enc.Encode(map[string]any{
				"read":         at.Add(time.Duration(reads) * 10 * time.Second),
				"networks":     map[string]any{"eth0": map[string]any{"rx_bytes": rx, "tx_bytes": rx / 2}},
				"blkio_stats":  map[string]any{"io_service_bytes_recursive": []any{map[string]any{"op": "read", "value": rx * 2}, map[string]any{"op": "write", "value": rx}}},
				"pids_stats":   map[string]any{"current": 12},
				"memory_stats": map[string]any{"usage": 100 << 20, "limit": 1 << 30},
			})
		}
	}))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))

	c := New("test")
	c.containers(context.Background()) // starts the background disk-usage read
	deadline := time.Now().Add(3 * time.Second)
	d := c.containers(context.Background())
	for d.Usage == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		d = c.containers(context.Background())
	}
	if d.Usage == nil {
		t.Fatalf("disk usage never arrived: %+v", d)
	}
	db := d.Containers[0]
	if strings.Join(db.Ports, ",") != "5432→5432/tcp,8008/tcp" {
		t.Fatalf("ports: %v", db.Ports)
	}
	if db.PIDs != 12 || db.NetRx != 1_000_000 || db.NetTx != 500_000 || db.BlockRead != 2_000_000 || db.StartedAt == 0 {
		t.Fatalf("rates: %+v", db)
	}
	if db.SizeRw != 5<<20 || len(db.Mounts) != 1 || db.Mounts[0].Name != "pgdata" || db.Networks[0] != "backend" {
		t.Fatalf("detail: %+v", db)
	}
	if d.Version != "27.3.1" || len(d.Images) != 2 || !d.Images[1].Dangling || d.Images[0].ID != "1111111111111111"[:12] {
		t.Fatalf("images: %+v", d.Images)
	}
	if len(d.Volumes) != 2 || d.Volumes[0].Name != "pgdata" || d.Volumes[0].UsedBy[0] != "db" || !d.Volumes[1].Anonymous {
		t.Fatalf("volumes: %+v", d.Volumes)
	}
	u := d.Usage
	// unused image's unique bytes (200 MB) + unused volume (10 MB) + idle build cache (50 MB)
	if u.Images != 900<<20 || u.Volumes != 2<<30+10<<20 || u.Reclaimable != 260<<20 {
		t.Fatalf("usage: %+v", u)
	}
}
