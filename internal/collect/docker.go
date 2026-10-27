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
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"hook/internal/proto"
)

// The Docker Engine API, read-only: list containers, inspect, one-shot stats,
// images, volumes and disk usage. DOCKER_HOST may point at a read-only socket
// proxy (tcp://127.0.0.1:2375), which is safer than giving the agent the raw
// socket (that's root-equivalent).

type dockerClient struct {
	http    *http.Client
	slow    *http.Client // no overall timeout: /system/df can take a while
	base    string
	err     error // why Docker can't be used here
	version string
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
	return &dockerClient{http: &http.Client{Transport: tr, Timeout: 10 * time.Second}, slow: &http.Client{Transport: tr}, base: base}
}

func (d *dockerClient) get(ctx context.Context, path string, out any) error {
	return d.do(ctx, d.http, path, out)
}

func (d *dockerClient) do(ctx context.Context, client *http.Client, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("docker API %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

type cpuSample struct{ total, system uint64 }

// ioSample holds a container's cumulative network and block I/O counters.
type ioSample struct {
	at             time.Time
	rx, tx, br, bw uint64
}

type listedContainer struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Created int64    `json:"Created"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

type containerStats struct {
	Read time.Time `json:"read"`
	CPU  struct {
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
	Networks map[string]struct {
		Rx uint64 `json:"rx_bytes"`
		Tx uint64 `json:"tx_bytes"`
	} `json:"networks"`
	Blkio struct {
		IO []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PIDs struct {
		Current int `json:"current"`
	} `json:"pids_stats"`
}

// containers reports up to 50 containers (running ones first), plus images,
// volumes and disk usage from the last background /system/df.
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
	if d.version == "" {
		var v struct {
			Version string `json:"Version"`
		}
		if d.get(ctx, "/version", &v) == nil {
			d.version = v.Version
		}
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
	all := list
	list = list[:min(len(list), 50)]

	df := c.dockerDF.get(d)
	out := make([]proto.Container, len(list))
	seenCPU := map[string]cpuSample{}
	seenIO := map[string]ioSample{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, ct := range list {
		out[i] = describe(ct)
		if df != nil {
			if sz, ok := df.sizeRw[ct.ID]; ok {
				out[i].SizeRw = sz
			}
		}
		wg.Add(1)
		go func(i int, id string, running bool) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var ins struct {
				RestartCount int `json:"RestartCount"`
				State        struct {
					StartedAt string `json:"StartedAt"`
					Health    *struct {
						Status string `json:"Status"`
					} `json:"Health"`
				} `json:"State"`
			}
			if err := d.get(ctx, "/containers/"+id+"/json", &ins); err == nil {
				out[i].Restarts = ins.RestartCount
				if ins.State.Health != nil {
					out[i].Health = ins.State.Health.Status
				}
				if t, err := time.Parse(time.RFC3339Nano, ins.State.StartedAt); err == nil && running && t.Year() > 1 {
					out[i].StartedAt = t.UnixMilli()
				}
			}
			if !running {
				return
			}
			var st containerStats
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
			out[i].PIDs = st.PIDs.Current

			at := st.Read
			if at.IsZero() || at.Year() < 2000 {
				at = time.Now()
			}
			io := ioSample{at: at}
			for _, n := range st.Networks {
				io.rx += n.Rx
				io.tx += n.Tx
			}
			for _, b := range st.Blkio.IO {
				switch strings.ToLower(b.Op) {
				case "read":
					io.br += b.Value
				case "write":
					io.bw += b.Value
				}
			}
			now := cpuSample{st.CPU.Usage.Total, st.CPU.System}
			mu.Lock()
			prev, had := c.dockerCPU[id]
			prevIO, hadIO := c.dockerIO[id]
			seenCPU[id], seenIO[id] = now, io
			mu.Unlock()
			if had && now.system > prev.system && now.total >= prev.total {
				cpus := float64(max(1, st.CPU.Online))
				out[i].CPU = round1(float64(now.total-prev.total) / float64(now.system-prev.system) * cpus * 100)
			}
			if dt := io.at.Sub(prevIO.at).Seconds(); hadIO && dt > 0 {
				per := func(a, b uint64) float64 {
					if a < b { // counters reset (container restarted)
						return 0
					}
					return float64(a-b) / dt
				}
				out[i].NetRx, out[i].NetTx = per(io.rx, prevIO.rx), per(io.tx, prevIO.tx)
				out[i].BlockRead, out[i].BlockWrite = per(io.br, prevIO.br), per(io.bw, prevIO.bw)
			}
		}(i, ct.ID, ct.State == "running")
	}
	wg.Wait()
	c.dockerCPU, c.dockerIO = seenCPU, seenIO

	res := &proto.Docker{Available: true, Version: d.version, Containers: out, Images: []proto.Image{}, Volumes: []proto.Volume{}}
	if df != nil {
		res.Images, res.Volumes, res.Usage = df.images, volumesWithUsers(df.volumes, all), df.usage
	}
	res.DetailError = c.dockerDF.lastErr()
	return res
}

// describe maps the list entry; stats and inspect fill in the rest.
func describe(ct listedContainer) proto.Container {
	out := proto.Container{ID: ct.ID[:min(12, len(ct.ID))], Name: name(ct.Names), Image: ct.Image, State: ct.State, Status: ct.Status,
		Created: ct.Created * 1000, Ports: []string{}, Networks: []string{}, Mounts: []proto.ContainerMount{}, SizeRw: -1}
	seen := map[string]bool{}
	for _, p := range ct.Ports {
		var s string
		switch {
		case p.PublicPort == 0:
			s = fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		case p.IP == "" || p.IP == "0.0.0.0" || p.IP == "::":
			s = fmt.Sprintf("%d→%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		default:
			s = fmt.Sprintf("%s:%d→%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type)
		}
		if !seen[s] { // Docker lists the IPv4 and IPv6 binding separately
			seen[s] = true
			out.Ports = append(out.Ports, s)
		}
	}
	for n := range ct.NetworkSettings.Networks {
		out.Networks = append(out.Networks, n)
	}
	slices.Sort(out.Networks)
	for _, m := range ct.Mounts {
		cm := proto.ContainerMount{Type: m.Type, Name: m.Name, Target: m.Destination, RW: m.RW}
		if m.Type != "volume" {
			cm.Name = m.Source
		}
		out.Mounts = append(out.Mounts, cm)
	}
	return out
}

func name(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// ---- images, volumes and disk usage ----

type dfResult struct {
	images  []proto.Image
	volumes []proto.Volume
	usage   *proto.DockerDisk
	sizeRw  map[string]int64 // full container ID → writable layer bytes
}

type dfCache struct {
	mu      sync.Mutex
	at      time.Time
	running bool
	data    *dfResult
	err     string
}

const dfEvery = 5 * time.Minute

// get returns the last result and starts a refresh in the background when
// it's stale, so a slow /system/df never delays a snapshot.
func (c *dfCache) get(d *dockerClient) *dfResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running && time.Since(c.at) > dfEvery {
		c.running = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			data, err := diskUsage(ctx, d)
			c.mu.Lock()
			defer c.mu.Unlock()
			c.running, c.at = false, time.Now()
			if data != nil {
				c.data = data
			}
			c.err = ""
			if err != nil {
				c.err = err.Error()
			}
		}()
	}
	return c.data
}

func (c *dfCache) lastErr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

type sysDF struct {
	LayersSize int64 `json:"LayersSize"`
	Images     []struct {
		ID         string   `json:"Id"`
		RepoTags   []string `json:"RepoTags"`
		Size       int64    `json:"Size"`
		SharedSize int64    `json:"SharedSize"`
		Created    int64    `json:"Created"`
		Containers int      `json:"Containers"`
	} `json:"Images"`
	Containers []struct {
		ID     string `json:"Id"`
		SizeRw int64  `json:"SizeRw"`
		State  string `json:"State"`
	} `json:"Containers"`
	Volumes    []dfVolume `json:"Volumes"`
	BuildCache []struct {
		Size   int64 `json:"Size"`
		InUse  bool  `json:"InUse"`
		Shared bool  `json:"Shared"`
	} `json:"BuildCache"`
}

type dfVolume struct {
	Name      string            `json:"Name"`
	Driver    string            `json:"Driver"`
	CreatedAt string            `json:"CreatedAt"`
	Labels    map[string]string `json:"Labels"`
	UsageData *struct {
		Size     int64 `json:"Size"`
		RefCount int   `json:"RefCount"`
	} `json:"UsageData"` // only from /system/df
}

var anonVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

// diskUsage reads /system/df. A socket proxy may refuse it (it needs
// SYSTEM=1); images and volumes are then listed without sizes or usage.
func diskUsage(ctx context.Context, d *dockerClient) (*dfResult, error) {
	var df sysDF
	err := d.do(ctx, d.slow, "/system/df", &df)
	if err != nil {
		// Fallback: plain listings, which need IMAGES=1 / VOLUMES=1 on a proxy.
		var errs []string
		if e := d.get(ctx, "/images/json", &df.Images); e != nil {
			errs = append(errs, "images: "+e.Error())
		}
		var vols struct {
			Volumes []dfVolume `json:"Volumes"`
		}
		if e := d.get(ctx, "/volumes", &vols); e != nil {
			errs = append(errs, "volumes: "+e.Error())
		}
		df.Volumes = vols.Volumes
		msg := "disk usage unavailable (" + err.Error() + ")"
		if len(errs) > 0 {
			msg += "; " + strings.Join(errs, "; ")
		}
		err = errors.New(msg)
	}

	r := &dfResult{images: []proto.Image{}, volumes: []proto.Volume{}, sizeRw: map[string]int64{}}
	var usage proto.DockerDisk
	usage.At = time.Now().UnixMilli()
	for _, im := range df.Images {
		tags := slices.DeleteFunc(slices.Clone(im.RepoTags), func(t string) bool { return t == "<none>:<none>" })
		if tags == nil {
			tags = []string{}
		}
		id := strings.TrimPrefix(im.ID, "sha256:")
		r.images = append(r.images, proto.Image{ID: id[:min(12, len(id))], Tags: tags, Size: im.Size, Created: im.Created * 1000,
			Containers: im.Containers, Dangling: len(tags) == 0})
		if im.Containers == 0 {
			usage.Reclaimable += max(0, im.Size-max(0, im.SharedSize))
		}
		if df.LayersSize == 0 {
			usage.Images += im.Size
		}
	}
	if df.LayersSize > 0 {
		usage.Images = df.LayersSize
	}
	for _, ct := range df.Containers {
		r.sizeRw[ct.ID] = ct.SizeRw
		usage.Containers += max(0, ct.SizeRw)
		if ct.State != "running" {
			usage.Reclaimable += max(0, ct.SizeRw)
		}
	}
	usage.Volumes = -1
	for _, v := range df.Volumes {
		pv := proto.Volume{Name: v.Name, Driver: v.Driver, Size: -1, UsedBy: []string{},
			Anonymous: anonVolume.MatchString(v.Name) || v.Labels["com.docker.volume.anonymous"] != ""}
		if t, e := time.Parse(time.RFC3339, v.CreatedAt); e == nil {
			pv.Created = t.UnixMilli()
		}
		if v.UsageData != nil && v.UsageData.Size >= 0 {
			pv.Size = v.UsageData.Size
			if usage.Volumes < 0 {
				usage.Volumes = 0
			}
			usage.Volumes += pv.Size
			if v.UsageData.RefCount == 0 {
				usage.Reclaimable += pv.Size
			}
		}
		r.volumes = append(r.volumes, pv)
	}
	for _, b := range df.BuildCache {
		if !b.Shared {
			usage.BuildCache += b.Size
			if !b.InUse {
				usage.Reclaimable += b.Size
			}
		}
	}
	slices.SortFunc(r.images, func(a, b proto.Image) int { return cmpDesc(float64(a.Size), float64(b.Size)) })
	slices.SortFunc(r.volumes, func(a, b proto.Volume) int { return cmpDesc(float64(a.Size), float64(b.Size)) })
	r.images, r.volumes = r.images[:min(100, len(r.images))], r.volumes[:min(100, len(r.volumes))]
	if len(df.Containers) > 0 || df.LayersSize > 0 || len(df.Images) > 0 {
		r.usage = &usage
	}
	if err != nil && len(r.images) == 0 && len(r.volumes) == 0 {
		return nil, err
	}
	return r, err
}

// volumesWithUsers fills in which containers mount each volume, from the
// live container list (fresher than the 5-minute disk usage).
func volumesWithUsers(vols []proto.Volume, cs []listedContainer) []proto.Volume {
	users := map[string][]string{}
	for _, ct := range cs {
		for _, m := range ct.Mounts {
			if m.Type == "volume" && m.Name != "" {
				users[m.Name] = append(users[m.Name], name(ct.Names))
			}
		}
	}
	out := make([]proto.Volume, len(vols))
	for i, v := range vols {
		v.UsedBy = users[v.Name]
		if v.UsedBy == nil {
			v.UsedBy = []string{}
		}
		out[i] = v
	}
	return out
}
