package collect

import (
	"context"
	"encoding/csv"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"hook/internal/proto"
)

// GPUs are read with nvidia-smi (NVIDIA) or from sysfs (AMD, Intel). Both are
// read-only. Detection is repeated every 10 minutes so a driver installed
// later is picked up without restarting the agent.

type gpuReader struct {
	mu         sync.Mutex
	detectedAt time.Time
	smi        string    // path to nvidia-smi, "" if absent
	cards      []drmCard // AMD and Intel cards found in sysfs
	note       string
	last       *proto.GPUs
	lastAt     time.Time
}

type drmCard struct {
	dir    string // /sys/class/drm/card0
	vendor string
	index  int
}

var cardRe = regexp.MustCompile(`^card(\d+)$`)

// PCI vendor IDs of real GPUs. Virtual and BMC display adapters (virtio,
// QXL, VMware, Cirrus, ASPEED, Matrox) are deliberately absent: a VPS with
// only those has no GPU.
var gpuVendors = map[string]string{"0x10de": "nvidia", "0x1002": "amd", "0x8086": "intel"}

func (g *gpuReader) detect() {
	g.detectedAt = time.Now()
	g.smi, g.cards, g.note = "", nil, ""
	if p, err := exec.LookPath("nvidia-smi"); err == nil {
		g.smi = p
	}
	entries, _ := os.ReadDir("/sys/class/drm")
	nvidiaInSysfs := false
	for _, e := range entries {
		m := cardRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		dir := filepath.Join("/sys/class/drm", e.Name())
		vendor := gpuVendors[readTrim(filepath.Join(dir, "device", "vendor"))]
		switch vendor {
		case "nvidia":
			nvidiaInSysfs = true // read through nvidia-smi instead
		case "amd", "intel":
			i, _ := strconv.Atoi(m[1])
			g.cards = append(g.cards, drmCard{dir: dir, vendor: vendor, index: i})
		}
	}
	if !nvidiaInSysfs {
		if _, err := os.Stat("/proc/driver/nvidia/gpus"); err == nil {
			nvidiaInSysfs = true
		}
	}
	if nvidiaInSysfs && g.smi == "" {
		g.note = "An NVIDIA GPU is installed, but nvidia-smi wasn't found. Install the NVIDIA driver to see its usage."
	}
}

// gpus returns the current readings. Readings are cached for 9 s so the 5 s
// metrics frame and the snapshot share one nvidia-smi run.
func (c *Collector) gpus(ctx context.Context, withProcs bool) *proto.GPUs {
	g := &c.gpu
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.detectedAt) > 10*time.Minute {
		g.detect()
	}
	if g.smi == "" && len(g.cards) == 0 {
		return &proto.GPUs{Note: g.note, Devices: []proto.GPU{}, Procs: []proto.GPUProc{}}
	}
	if g.last != nil && time.Since(g.lastAt) < 9*time.Second && (!withProcs || g.last.Procs != nil) {
		return g.last
	}
	out := &proto.GPUs{Note: g.note, Devices: []proto.GPU{}}
	if g.smi != "" {
		devs, uuids, err := nvidiaDevices(ctx, g.smi)
		if err != nil {
			out.Note = "nvidia-smi failed: " + err.Error()
		}
		out.Devices = append(out.Devices, devs...)
		if withProcs && len(devs) > 0 {
			out.Procs = nvidiaProcs(ctx, g.smi, uuids)
		}
	}
	for _, card := range g.cards {
		out.Devices = append(out.Devices, sysfsGPU(card))
	}
	if out.Procs == nil && withProcs {
		out.Procs = []proto.GPUProc{}
	}
	out.Present = len(out.Devices) > 0
	g.last, g.lastAt = out, time.Now()
	return out
}

// hasGPU is cheap: it never runs anything when detection found nothing.
func (c *Collector) hasGPU() bool {
	g := &c.gpu
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.detectedAt) > 10*time.Minute {
		g.detect()
	}
	return g.smi != "" || len(g.cards) > 0
}

// gpuSummary is the busiest GPU's utilisation and the fullest GPU's memory.
func gpuSummary(gs *proto.GPUs) (util, mem float64) {
	for _, d := range gs.Devices {
		util = math.Max(util, d.Util)
		if d.MemTotal > 0 {
			mem = math.Max(mem, float64(d.MemUsed)/float64(d.MemTotal)*100)
		}
	}
	return round1(util), round1(mem)
}

const smiFields = "index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit,fan.speed,clocks.sm,driver_version"

func nvidiaDevices(ctx context.Context, smi string) ([]proto.GPU, map[string]int, error) {
	rows, err := runCSV(ctx, smi, "--query-gpu="+smiFields, "--format=csv,noheader,nounits")
	if err != nil {
		return nil, nil, err
	}
	uuids := map[string]int{}
	var out []proto.GPU
	for _, r := range rows {
		if len(r) < 12 {
			continue
		}
		idx, _ := strconv.Atoi(r[0])
		uuids[r[1]] = idx
		util := num(r[3])
		if math.IsNaN(util) {
			util = -1
		}
		out = append(out, proto.GPU{
			Index: idx, Vendor: "nvidia", Name: r[2], Util: util,
			MemUsed: mib(r[4]), MemTotal: mib(r[5]),
			Temp: zero(num(r[6])), Power: zero(num(r[7])), PowerLimit: zero(num(r[8])),
			Fan: zero(num(r[9])), ClockMHz: zero(num(r[10])), Driver: r[11],
		})
	}
	return out, uuids, nil
}

func nvidiaProcs(ctx context.Context, smi string, uuids map[string]int) []proto.GPUProc {
	rows, err := runCSV(ctx, smi, "--query-compute-apps=pid,process_name,used_memory,gpu_uuid", "--format=csv,noheader,nounits")
	out := []proto.GPUProc{}
	if err != nil {
		return out
	}
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(r[0])
		out = append(out, proto.GPUProc{PID: int32(pid), Name: filepath.Base(r[1]), Mem: mib(r[2]), GPU: uuids[r[3]]})
	}
	slices.SortFunc(out, func(a, b proto.GPUProc) int { return cmpDesc(float64(a.Mem), float64(b.Mem)) })
	return out[:min(10, len(out))]
}

func runCSV(ctx context.Context, bin string, args ...string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(strings.NewReader(string(b)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

// sysfsGPU reads an AMD or Intel card. amdgpu exposes busy %, VRAM, power
// and temperature; i915 exposes little more than the clock without root.
func sysfsGPU(card drmCard) proto.GPU {
	dev := filepath.Join(card.dir, "device")
	g := proto.GPU{Index: card.index, Vendor: card.vendor, Util: -1}
	g.Name = readTrim(filepath.Join(dev, "product_name"))
	if g.Name == "" {
		id := readTrim(filepath.Join(dev, "device"))
		g.Name = map[string]string{"amd": "AMD Radeon", "intel": "Intel Graphics"}[card.vendor] + " (" + id + ")"
	}
	if v, ok := readNum(filepath.Join(dev, "gpu_busy_percent")); ok {
		g.Util = v
	}
	if v, ok := readNum(filepath.Join(dev, "mem_info_vram_used")); ok {
		g.MemUsed = uint64(v)
	}
	if v, ok := readNum(filepath.Join(dev, "mem_info_vram_total")); ok {
		g.MemTotal = uint64(v)
	}
	if v, ok := readNum(filepath.Join(card.dir, "gt_cur_freq_mhz")); ok {
		g.ClockMHz = v
	}
	if mons, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*")); len(mons) > 0 {
		h := mons[0]
		if v, ok := readNum(filepath.Join(h, "temp1_input")); ok {
			g.Temp = round1(v / 1000)
		}
		for _, f := range []string{"power1_average", "power1_input"} {
			if v, ok := readNum(filepath.Join(h, f)); ok {
				g.Power = round1(v / 1e6)
				break
			}
		}
		if v, ok := readNum(filepath.Join(h, "power1_cap")); ok {
			g.PowerLimit = round1(v / 1e6)
		}
		pwm, ok1 := readNum(filepath.Join(h, "pwm1"))
		pmax, ok2 := readNum(filepath.Join(h, "pwm1_max"))
		if ok1 && ok2 && pmax > 0 {
			g.Fan = round1(pwm / pmax * 100)
		}
	}
	return g
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readNum(path string) (float64, bool) {
	v, err := strconv.ParseFloat(readTrim(path), 64)
	return v, err == nil
}

// num parses nvidia-smi's numbers; "[N/A]" and "[Not Supported]" become NaN.
func num(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func zero(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return round1(v)
}

func mib(s string) uint64 {
	v := num(s)
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	return uint64(v * 1024 * 1024)
}
