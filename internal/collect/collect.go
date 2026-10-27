// Package collect gathers host metrics for the agent with gopsutil. It keeps
// the previous counters so it can report rates and CPU percentages.
package collect

import (
	"context"
	"math"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/sensors"

	"hook/internal/proto"
)

type Collector struct {
	version string

	// metrics frame state
	prevCPU  *cpu.TimesStat
	prevDisk counter
	prevNet  counter

	// snapshot state
	prevCores  []cpu.TimesStat
	prevIf     map[string]net.IOCountersStat
	prevIfAt   time.Time
	procs      map[int32]*process.Process
	procsAt    time.Time
	lastTopCPU []proto.Proc
	docker     *dockerClient
	dockerCPU  map[string]cpuSample
	dockerIO   map[string]ioSample
	dockerDF   dfCache
	bootTime   time.Time
	gpu        gpuReader

	mu      sync.Mutex
	diskPct float64 // fullest mount, refreshed by Snapshot
}

type counter struct {
	at      time.Time
	in, out uint64
}

func New(version string) *Collector {
	return &Collector{version: version, procs: map[int32]*process.Process{}}
}

func (c *Collector) Info(ctx context.Context) proto.HostInfo {
	in := proto.HostInfo{AgentVersion: c.version, Arch: runtime.GOARCH, OS: runtime.GOOS}
	if h, err := host.InfoWithContext(ctx); err == nil {
		in.Hostname, in.Kernel, in.BootTime = h.Hostname, h.KernelVersion, int64(h.BootTime)
		in.Platform = strings.TrimSpace(h.Platform + " " + h.PlatformVersion)
		if h.VirtualizationRole == "guest" {
			in.Virtualization = h.VirtualizationSystem
		}
	}
	if ci, err := cpu.InfoWithContext(ctx); err == nil && len(ci) > 0 {
		in.CPUModel = strings.TrimSpace(ci[0].ModelName)
	}
	in.Cores, _ = cpu.CountsWithContext(ctx, true)
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		in.MemTotal = vm.Total
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		in.SwapTotal = sw.Total
	}
	return in
}

// busy splits CPU time into used / iowait / steal shares of the delta.
func busy(a, b cpu.TimesStat) (used, iowait, steal float64) {
	total := func(t cpu.TimesStat) float64 {
		return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
	}
	dt := total(b) - total(a)
	if dt <= 0 {
		return 0, 0, 0
	}
	idle := b.Idle - a.Idle
	io := b.Iowait - a.Iowait
	st := b.Steal - a.Steal
	clamp := func(v float64) float64 { return math.Max(0, math.Min(100, v)) }
	return clamp((dt - idle - io - st) / dt * 100), clamp(io / dt * 100), clamp(st / dt * 100)
}

func rate(prev *counter, in, out uint64, now time.Time) (float64, float64) {
	defer func() { *prev = counter{at: now, in: in, out: out} }()
	if prev.at.IsZero() || in < prev.in || out < prev.out { // first sample or counter reset
		return 0, 0
	}
	s := now.Sub(prev.at).Seconds()
	if s <= 0 {
		return 0, 0
	}
	return float64(in-prev.in) / s, float64(out-prev.out) / s
}

// Whole disks only: partitions, device-mapper and md arrays would double count.
var partRe = regexp.MustCompile(`^((sd|vd|xvd|hd)[a-z]+\d+|nvme\d+n\d+p\d+|mmcblk\d+p\d+)$`)

func wholeDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd", "nbd"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return !partRe.MatchString(name)
}

// Virtual interfaces mirror traffic already counted on the real NIC.
func realIface(name string) bool {
	if name == "lo" || strings.HasPrefix(strings.ToLower(name), "loopback") {
		return false
	}
	for _, p := range []string{"veth", "docker", "br-", "virbr", "cali", "cni", "flannel", "kube", "lxc", "vnet", "isatap", "Teredo"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

func (c *Collector) Metrics(ctx context.Context) proto.Metrics {
	now := time.Now()
	m := proto.Metrics{TS: now.UnixMilli()}

	if t, err := cpu.TimesWithContext(ctx, false); err == nil && len(t) > 0 {
		if c.prevCPU != nil {
			m.CPU, m.IOWait, m.Steal = busy(*c.prevCPU, t[0])
		}
		c.prevCPU = &t[0]
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		m.Load1, m.Load5, m.Load15 = l.Load1, l.Load5, l.Load15
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemTotal = float64(vm.Total)
		m.MemUsed = float64(vm.Total - vm.Available) // what apps hold, not page cache
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		m.SwapUsed, m.SwapTotal = float64(sw.Used), float64(sw.Total)
	}
	if io, err := disk.IOCountersWithContext(ctx); err == nil {
		var r, w uint64
		for name, d := range io {
			if wholeDisk(name) {
				r += d.ReadBytes
				w += d.WriteBytes
			}
		}
		m.DiskRead, m.DiskWrite = rate(&c.prevDisk, r, w, now)
	}
	if nics, err := net.IOCountersWithContext(ctx, true); err == nil {
		var rx, tx uint64
		for _, n := range nics {
			if realIface(n.Name) {
				rx += n.BytesRecv
				tx += n.BytesSent
			}
		}
		if p := c.prevNet; !p.at.IsZero() && rx >= p.in && tx >= p.out {
			m.NetRxBytes, m.NetTxBytes = rx-p.in, tx-p.out
		}
		m.NetRx, m.NetTx = rate(&c.prevNet, rx, tx, now)
	}
	if pc, err := net.ProtoCountersWithContext(ctx, []string{"tcp"}); err == nil && len(pc) > 0 {
		m.TCP = float64(pc[0].Stats["CurrEstab"])
	}
	if misc, err := load.MiscWithContext(ctx); err == nil && misc.ProcsTotal > 0 {
		m.Procs = float64(misc.ProcsTotal)
	} else if pids, err := process.PidsWithContext(ctx); err == nil {
		m.Procs = float64(len(pids))
	}
	if c.hasGPU() {
		m.GPU, m.GPUMem = gpuSummary(c.gpus(ctx, false))
	}
	c.mu.Lock()
	m.DiskPct = c.diskPct
	c.mu.Unlock()
	m.CPU, m.IOWait, m.Steal = round1(m.CPU), round1(m.IOWait), round1(m.Steal)
	m.DiskRead, m.DiskWrite, m.NetRx, m.NetTx = math.Round(m.DiskRead), math.Round(m.DiskWrite), math.Round(m.NetRx), math.Round(m.NetTx)
	return m
}

var pseudoFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "bpf": true, "autofs": true, "fusectl": true, "configfs": true,
	"hugetlbfs": true, "nsfs": true, "ramfs": true, "efivarfs": true, "binfmt_misc": true,
	"rpc_pipefs": true, "fuse.lxcfs": true, "iso9660": true, "nfsd": true, "fuse.snapfuse": true,
}

func (c *Collector) mounts(ctx context.Context) []proto.Mount {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	slices.SortFunc(parts, func(a, b disk.PartitionStat) int { return len(a.Mountpoint) - len(b.Mountpoint) })
	var out []proto.Mount
	for _, p := range parts {
		if pseudoFS[p.Fstype] || strings.HasPrefix(p.Mountpoint, "/snap/") || strings.HasPrefix(p.Mountpoint, "/var/lib/docker/") || seen[p.Device] {
			continue
		}
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		seen[p.Device] = true // bind mounts repeat the same device
		out = append(out, proto.Mount{Path: p.Mountpoint, Device: p.Device, FSType: p.Fstype, Total: u.Total, Used: u.Used,
			UsedPct: round1(u.UsedPercent), InodesPct: round1(u.InodesUsedPercent)})
		if len(out) >= 20 {
			break
		}
	}
	return out
}

// Snapshot gathers the heavier, slower-changing state. watch lists the
// systemd units the hub asked about.
func (c *Collector) Snapshot(ctx context.Context, watch []string) proto.Snapshot {
	now := time.Now()
	s := proto.Snapshot{TS: now.UnixMilli()}
	s.Uptime, _ = host.UptimeWithContext(ctx)
	if c.bootTime.IsZero() {
		if bt, err := host.BootTimeWithContext(ctx); err == nil {
			c.bootTime = time.Unix(int64(bt), 0)
		}
	}
	s.Docker = c.containers(ctx)
	s.GPU = c.gpus(ctx, true)
	s.Units = units(ctx, watch, c.bootTime)
	s.FailedUnits = failedUnits(ctx)

	if t, err := cpu.TimesWithContext(ctx, true); err == nil {
		if len(c.prevCores) == len(t) {
			for i := range t {
				u, _, _ := busy(c.prevCores[i], t[i])
				s.Cores = append(s.Cores, round1(u))
			}
		}
		c.prevCores = t
	}

	s.Mounts = c.mounts(ctx)
	worst := 0.0
	for _, m := range s.Mounts {
		worst = math.Max(worst, m.UsedPct)
	}
	c.mu.Lock()
	c.diskPct = worst
	c.mu.Unlock()

	if nics, err := net.IOCountersWithContext(ctx, true); err == nil {
		dt := now.Sub(c.prevIfAt).Seconds()
		next := map[string]net.IOCountersStat{}
		details := map[string]net.InterfaceStat{}
		if ifs, err := net.InterfacesWithContext(ctx); err == nil {
			for _, i := range ifs {
				details[i.Name] = i
			}
		}
		for _, n := range nics {
			next[n.Name] = n
			if !realIface(n.Name) || (n.BytesRecv == 0 && n.BytesSent == 0) {
				continue
			}
			f := proto.Iface{Name: n.Name, RxErr: n.Errin, TxErr: n.Errout, Drops: n.Dropin + n.Dropout,
				RxTotal: n.BytesRecv, TxTotal: n.BytesSent, Addrs: []string{}}
			if d, ok := details[n.Name]; ok {
				f.MTU, f.Up = d.MTU, slices.Contains(d.Flags, "up")
				for _, a := range d.Addrs {
					if !strings.HasPrefix(a.Addr, "fe80:") { // link-local says nothing useful
						f.Addrs = append(f.Addrs, a.Addr)
					}
				}
			}
			if v, ok := readNum("/sys/class/net/" + n.Name + "/speed"); ok && v > 0 {
				f.Speed = int(v)
			}
			if p, ok := c.prevIf[n.Name]; ok && dt > 0 && n.BytesRecv >= p.BytesRecv && n.BytesSent >= p.BytesSent {
				f.Rx, f.Tx = float64(n.BytesRecv-p.BytesRecv)/dt, float64(n.BytesSent-p.BytesSent)/dt
			}
			s.Ifaces = append(s.Ifaces, f)
		}
		c.prevIf, c.prevIfAt = next, now
	}

	s.TopCPU, s.TopMem = c.topProcs(ctx, 8)
	s.Temps = temps(ctx)
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		s.RebootRequired = true
	}
	return s
}

// topProcs keeps Process objects between snapshots so CPU % covers the whole
// interval, and only resolves names and users for the winners. A process seen
// for the first time, or an interval under 10 s, has no meaningful CPU % yet
// (it would be a lifetime average or noise), so it's left out of the CPU list.
func (c *Collector) topProcs(ctx context.Context, n int) (byCPU, byMem []proto.Proc) {
	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, nil
	}
	now := time.Now()
	// An extra snapshot soon after the last one (e.g. the hub changed the
	// watched units) mustn't sample CPU: that would also reset every
	// process's baseline. Keep the baseline and repeat the last list.
	tooSoon := !c.procsAt.IsZero() && now.Sub(c.procsAt) < 10*time.Second
	measured := !c.procsAt.IsZero() && !tooSoon
	if !tooSoon {
		c.procsAt = now
	}
	type row struct {
		p     *process.Process
		cpu   float64
		rss   uint64
		known bool
	}
	alive := map[int32]*process.Process{}
	rows := make([]row, 0, len(ps))
	for _, p := range ps {
		old, known := c.procs[p.Pid]
		if known {
			p = old
		}
		alive[p.Pid] = p
		var pct float64
		if !tooSoon || !known {
			pct, _ = p.PercentWithContext(ctx, 0)
		}
		var rss uint64
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil {
			rss = mi.RSS
		}
		rows = append(rows, row{p, pct, rss, known})
	}
	c.procs = alive
	resolve := func(r row) proto.Proc {
		name, _ := r.p.NameWithContext(ctx)
		user, _ := r.p.UsernameWithContext(ctx)
		return proto.Proc{PID: r.p.Pid, Name: name, User: user, CPU: round1(r.cpu), RSS: r.rss}
	}
	switch {
	case tooSoon:
		byCPU = c.lastTopCPU
	case measured:
		ranked := slices.DeleteFunc(slices.Clone(rows), func(r row) bool { return !r.known || r.cpu <= 0 })
		slices.SortFunc(ranked, func(a, b row) int { return cmpDesc(a.cpu, b.cpu) })
		for _, r := range ranked[:min(n, len(ranked))] {
			byCPU = append(byCPU, resolve(r))
		}
		c.lastTopCPU = byCPU
	}
	slices.SortFunc(rows, func(a, b row) int { return cmpDesc(float64(a.rss), float64(b.rss)) })
	for _, r := range rows[:min(n, len(rows))] {
		byMem = append(byMem, resolve(r))
	}
	return byCPU, byMem
}

func temps(ctx context.Context) []proto.Temp {
	ts, _ := sensors.TemperaturesWithContext(ctx) // partial results come with an error
	best := map[string]float64{}
	for _, t := range ts {
		if t.Temperature <= 0 || t.Temperature > 150 {
			continue
		}
		key := strings.TrimSuffix(strings.Split(t.SensorKey, "_")[0], "input")
		best[key] = math.Max(best[key], t.Temperature)
	}
	out := make([]proto.Temp, 0, len(best))
	for k, v := range best {
		out = append(out, proto.Temp{Sensor: k, C: round1(v)})
	}
	slices.SortFunc(out, func(a, b proto.Temp) int { return cmpDesc(a.C, b.C) })
	return out[:min(8, len(out))]
}

func cmpDesc(a, b float64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
