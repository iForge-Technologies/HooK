package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"hook/internal/proto"
)

// The Docker Engine API, read-only: list containers, inspect, one-shot stats.
// DOCKER_HOST may point at a read-only socket proxy (tcp://127.0.0.1:2375),
// which is safer than giving the agent the raw socket (that's root-equivalent).

type dockerClient struct {
	http *http.Client
	base string
	err  error // why Docker can't be used here
}

func newDocker() *dockerClient {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		if runtime.GOOS == "windows" {
			return &dockerClient{err: errors.New("Docker monitoring needs a Linux host or DOCKER_HOST=tcp://…")}
		}
		host = "unix:///var/run/docker.sock"
	}
	u, err := url.Parse(host)
	if err != nil {
		return &dockerClient{err: fmt.Errorf("bad DOCKER_HOST: %w", err)}
	}
	tr := &http.Transport{MaxIdleConns: 2, IdleConnTimeout: time.Minute}
	base := "http://docker"
	switch u.Scheme {
	case "unix":
		path := u.Path
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
	case "tcp", "http":
		base = "http://" + u.Host
	default:
		return &dockerClient{err: fmt.Errorf("unsupported DOCKER_HOST scheme %q", u.Scheme)}
	}
	return &dockerClient{http: &http.Client{Transport: tr, Timeout: 10 * time.Second}, base: base}
}

func (d *dockerClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("docker API %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

type cpuSample struct{ total, system uint64 }

type listedContainer struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

// containers reports up to 50 containers, running ones first.
func (c *Collector) containers(ctx context.Context) *proto.Docker {
	if c.docker == nil {
		c.docker = newDocker()
	}
	d := c.docker
	if d.err != nil {
		return &proto.Docker{Error: d.err.Error()}
	}
	var list []listedContainer
	if err := d.get(ctx, "/containers/json?all=1", &list); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "permission denied"):
			msg = "no permission to read the Docker socket (add hook-agent to the docker group, or set DOCKER_HOST to a read-only socket proxy)"
		case strings.Contains(msg, "no such file"), strings.Contains(msg, "connection refused"):
			msg = "Docker isn't running on this server"
		}
		return &proto.Docker{Error: msg}
	}
	slices.SortStableFunc(list, func(a, b listedContainer) int {
		ar, br := a.State == "running", b.State == "running"
		switch {
		case ar && !br:
			return -1
		case br && !ar:
			return 1
		}
		return strings.Compare(name(a.Names), name(b.Names))
	})
	list = list[:min(len(list), 50)]

	out := make([]proto.Container, len(list))
	seen := map[string]cpuSample{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, ct := range list {
		out[i] = proto.Container{ID: ct.ID[:min(12, len(ct.ID))], Name: name(ct.Names), Image: ct.Image, State: ct.State, Status: ct.Status}
		wg.Add(1)
		go func(i int, id string, running bool) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var ins struct {
				RestartCount int `json:"RestartCount"`
				State        struct {
					Health *struct {
						Status string `json:"Status"`
					} `json:"Health"`
				} `json:"State"`
			}
			if err := d.get(ctx, "/containers/"+id+"/json", &ins); err == nil {
				out[i].Restarts = ins.RestartCount
				if ins.State.Health != nil {
					out[i].Health = ins.State.Health.Status
				}
			}
			if !running {
				return
			}
			var st struct {
				CPU struct {
					Usage struct {
						Total uint64 `json:"total_usage"`
					} `json:"cpu_usage"`
					System uint64 `json:"system_cpu_usage"`
					Online int    `json:"online_cpus"`
				} `json:"cpu_stats"`
				Mem struct {
					Usage uint64            `json:"usage"`
					Limit uint64            `json:"limit"`
					Stats map[string]uint64 `json:"stats"`
				} `json:"memory_stats"`
			}
			if err := d.get(ctx, "/containers/"+id+"/stats?stream=false&one-shot=true", &st); err != nil {
				return
			}
			mem := st.Mem.Usage
			for _, k := range []string{"inactive_file", "total_inactive_file"} { // cgroup v2, v1
				if v, ok := st.Mem.Stats[k]; ok && v < mem {
					mem -= v
					break
				}
			}
			out[i].Mem, out[i].MemLimit = mem, st.Mem.Limit
			now := cpuSample{st.CPU.Usage.Total, st.CPU.System}
			mu.Lock()
			prev, had := c.dockerCPU[id]
			seen[id] = now
			mu.Unlock()
			if had && now.system > prev.system && now.total >= prev.total {
				cpus := float64(max(1, st.CPU.Online))
				out[i].CPU = round1(float64(now.total-prev.total) / float64(now.system-prev.system) * cpus * 100)
			}
		}(i, ct.ID, ct.State == "running")
	}
	wg.Wait()
	c.dockerCPU = seen
	return &proto.Docker{Available: true, Containers: out}
}

func name(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
