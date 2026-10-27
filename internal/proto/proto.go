// Package proto defines the messages exchanged between the VPS agent and the
// hub over a WebSocket. The agent only ever reports; the hub only ever sends
// collection settings. There is no message that runs anything on the server.
package proto

import "hook/internal/probe"

// Message types.
const (
	TypeHello    = "hello"    // agent → hub, once per connection
	TypeMetrics  = "metrics"  // agent → hub, every Interval seconds
	TypeSnapshot = "snapshot" // agent → hub, every SnapshotInterval seconds
	TypeConfig   = "config"   // hub → agent

	// Multi-location confirmation. Agents only answer when the server's owner
	// enabled it locally (HOOK_PROBES=1), and only for public addresses.
	TypeProbe       = "probe"       // hub → agent
	TypeProbeResult = "probeResult" // agent → hub

	// Bandwidth test against a fixed public endpoint chosen by the agent, not
	// the hub. Agents refuse when HOOK_SPEEDTEST=0 or a test ran recently.
	TypeSpeedTest   = "speedTest"   // hub → agent
	TypeSpeedResult = "speedResult" // agent → hub
)

type SpeedTestRequest struct {
	ID string `json:"id"`
}

type SpeedResult struct {
	ID        string  `json:"id"`
	DownBps   float64 `json:"downBps"` // bits per second
	UpBps     float64 `json:"upBps"`
	LatencyMs float64 `json:"latencyMs"`
	JitterMs  float64 `json:"jitterMs"`
	Server    string  `json:"server"` // e.g. "Cloudflare FRA"
	Bytes     int64   `json:"bytes"`  // transferred in total, both directions
	Error     string  `json:"error,omitempty"`
}

type ProbeRequest struct {
	ID   string     `json:"id"`
	Spec probe.Spec `json:"spec"`
}

type ProbeResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Latency int    `json:"latency"` // ms
	Code    int    `json:"code,omitempty"`
	Msg     string `json:"msg"`
	Skipped bool   `json:"skipped,omitempty"` // declined (disabled, busy, private target): not a result
}

type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type HostInfo struct {
	AgentVersion   string `json:"agentVersion"`
	Hostname       string `json:"hostname"`
	OS             string `json:"os"`       // linux
	Platform       string `json:"platform"` // ubuntu 24.04
	Kernel         string `json:"kernel"`
	Arch           string `json:"arch"`
	Virtualization string `json:"virtualization,omitempty"`
	CPUModel       string `json:"cpuModel"`
	Cores          int    `json:"cores"`
	MemTotal       uint64 `json:"memTotal"`
	SwapTotal      uint64 `json:"swapTotal"`
	BootTime       int64  `json:"bootTime"`              // unix seconds
	Probes         bool   `json:"probes"`                // will run confirmation checks for the hub
	NoSpeedTest    bool   `json:"noSpeedTest,omitempty"` // HOOK_SPEEDTEST=0 on this server
}

// Metrics is the lightweight frame. Rates are per second; percentages 0-100.
type Metrics struct {
	TS        int64   `json:"ts"` // unix ms
	CPU       float64 `json:"cpu"`
	IOWait    float64 `json:"iowait"`
	Steal     float64 `json:"steal"`
	Load1     float64 `json:"load1"`
	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	MemUsed   float64 `json:"memUsed"` // bytes, excluding cache/buffers
	MemTotal  float64 `json:"memTotal"`
	SwapUsed  float64 `json:"swapUsed"`
	SwapTotal float64 `json:"swapTotal"`
	DiskRead  float64 `json:"diskRead"` // bytes/s
	DiskWrite float64 `json:"diskWrite"`
	NetRx     float64 `json:"netRx"` // bytes/s
	NetTx     float64 `json:"netTx"`
	TCP       float64 `json:"tcp"`     // established TCP sockets
	Procs     float64 `json:"procs"`   // process count
	DiskPct   float64 `json:"diskPct"` // fullest real mount, %
	GPU       float64 `json:"gpu"`     // busiest GPU's utilisation, %; 0 without a GPU
	GPUMem    float64 `json:"gpuMem"`  // fullest GPU's memory, %

	// Bytes moved on real interfaces since the previous frame, for traffic
	// totals. Older agents leave these at 0 and the hub uses the rates.
	NetRxBytes uint64 `json:"netRxBytes,omitempty"`
	NetTxBytes uint64 `json:"netTxBytes,omitempty"`
}

type Mount struct {
	Path      string  `json:"path"`
	Device    string  `json:"device"`
	FSType    string  `json:"fstype"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	UsedPct   float64 `json:"usedPct"`
	InodesPct float64 `json:"inodesPct"`
}

type Iface struct {
	Name    string   `json:"name"`
	Rx      float64  `json:"rx"` // bytes/s
	Tx      float64  `json:"tx"`
	RxErr   uint64   `json:"rxErr"`
	TxErr   uint64   `json:"txErr"`
	Drops   uint64   `json:"drops"`
	RxTotal uint64   `json:"rxTotal"` // bytes since boot
	TxTotal uint64   `json:"txTotal"`
	Up      bool     `json:"up"`
	Speed   int      `json:"speed"` // link speed in Mbit/s; 0 if the driver doesn't say (common on VMs)
	MTU     int      `json:"mtu"`
	Addrs   []string `json:"addrs"`
}

// Proc carries no command line: arguments often contain secrets.
type Proc struct {
	PID  int32   `json:"pid"`
	Name string  `json:"name"`
	User string  `json:"user"`
	CPU  float64 `json:"cpu"` // % of one core
	RSS  uint64  `json:"rss"`
}

type Temp struct {
	Sensor string  `json:"sensor"`
	C      float64 `json:"c"`
}

type Snapshot struct {
	TS             int64     `json:"ts"`
	Uptime         uint64    `json:"uptime"` // seconds
	Cores          []float64 `json:"cores"`  // per-core usage %
	Mounts         []Mount   `json:"mounts"`
	Ifaces         []Iface   `json:"ifaces"`
	TopCPU         []Proc    `json:"topCpu"`
	TopMem         []Proc    `json:"topMem"`
	Temps          []Temp    `json:"temps"`
	RebootRequired bool      `json:"rebootRequired"`
	Docker         *Docker   `json:"docker,omitempty"`
	GPU            *GPUs     `json:"gpu,omitempty"`
	Units          []Unit    `json:"units"`       // watched systemd units
	FailedUnits    []string  `json:"failedUnits"` // every unit systemd reports as failed
}

// GPUs is always sent by current agents; Present=false means none was found.
type GPUs struct {
	Present bool      `json:"present"`
	Note    string    `json:"note,omitempty"` // e.g. "NVIDIA GPU found but nvidia-smi isn't installed"
	Devices []GPU     `json:"devices"`
	Procs   []GPUProc `json:"procs"`
}

type GPU struct {
	Index      int     `json:"index"`
	Vendor     string  `json:"vendor"` // nvidia | amd | intel
	Name       string  `json:"name"`
	Util       float64 `json:"util"`            // %, -1 if the driver doesn't report it
	MemUsed    uint64  `json:"memUsed"`         // bytes
	MemTotal   uint64  `json:"memTotal"`        // bytes; 0 for shared-memory GPUs
	Temp       float64 `json:"temp,omitempty"`  // °C
	Power      float64 `json:"power,omitempty"` // W
	PowerLimit float64 `json:"powerLimit,omitempty"`
	Fan        float64 `json:"fan,omitempty"` // %
	ClockMHz   float64 `json:"clockMhz,omitempty"`
	Driver     string  `json:"driver,omitempty"`
}

// GPUProc carries no command line, like Proc.
type GPUProc struct {
	PID  int32  `json:"pid"`
	Name string `json:"name"`
	GPU  int    `json:"gpu"` // device index
	Mem  uint64 `json:"mem"` // bytes
}

type Docker struct {
	Available  bool        `json:"available"`
	Error      string      `json:"error,omitempty"` // why it isn't available
	Version    string      `json:"version,omitempty"`
	Containers []Container `json:"containers"`
	Images     []Image     `json:"images"`
	Volumes    []Volume    `json:"volumes"`
	Usage      *DockerDisk `json:"usage,omitempty"` // from /system/df, refreshed every few minutes
	// Why images, volumes or usage are missing (e.g. a socket proxy without IMAGES=1).
	DetailError string `json:"detailError,omitempty"`
}

type DockerDisk struct {
	At          int64 `json:"at"` // unix ms of the measurement
	Images      int64 `json:"images"`
	Containers  int64 `json:"containers"` // writable layers
	Volumes     int64 `json:"volumes"`    // -1 when the engine didn't compute it
	BuildCache  int64 `json:"buildCache"`
	Reclaimable int64 `json:"reclaimable"` // unused images, stopped containers, unused volumes, idle build cache
}

type Image struct {
	ID         string   `json:"id"` // short
	Tags       []string `json:"tags"`
	Size       int64    `json:"size"`
	Created    int64    `json:"created"`    // unix ms
	Containers int      `json:"containers"` // using it, -1 if unknown
	Dangling   bool     `json:"dangling"`
}

type Volume struct {
	Name      string   `json:"name"`
	Driver    string   `json:"driver"`
	Size      int64    `json:"size"` // -1 if unknown
	Created   int64    `json:"created,omitempty"`
	UsedBy    []string `json:"usedBy"` // container names
	Anonymous bool     `json:"anonymous"`
}

type Container struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Image    string  `json:"image"`
	State    string  `json:"state"`            // running | exited | restarting | paused | dead | created
	Status   string  `json:"status"`           // e.g. "Up 3 hours (healthy)"
	Health   string  `json:"health,omitempty"` // healthy | unhealthy | starting
	Restarts int     `json:"restarts"`
	CPU      float64 `json:"cpu"` // % of one core
	Mem      uint64  `json:"mem"` // bytes, excluding page cache
	MemLimit uint64  `json:"memLimit"`

	Created    int64            `json:"created"`             // unix ms
	StartedAt  int64            `json:"startedAt,omitempty"` // unix ms, for running containers
	Ports      []string         `json:"ports"`               // "0.0.0.0:8080→80/tcp"
	Networks   []string         `json:"networks"`
	Mounts     []ContainerMount `json:"mounts"`
	NetRx      float64          `json:"netRx"` // bytes/s
	NetTx      float64          `json:"netTx"`
	BlockRead  float64          `json:"blockRead"` // bytes/s
	BlockWrite float64          `json:"blockWrite"`
	PIDs       int              `json:"pids"`
	SizeRw     int64            `json:"sizeRw"` // writable layer, -1 if unknown
}

// ContainerMount is one of a container's mounts.
type ContainerMount struct {
	Type   string `json:"type"`   // volume | bind | tmpfs
	Name   string `json:"name"`   // volume name, or the host path for binds
	Target string `json:"target"` // path inside the container
	RW     bool   `json:"rw"`
}

type Unit struct {
	Name     string `json:"name"`
	Active   string `json:"active"` // active | inactive | failed | activating | not-found …
	Sub      string `json:"sub"`    // running | exited | dead …
	Restarts int    `json:"restarts"`
	Since    int64  `json:"since"` // unix ms of the last state change
}

type Config struct {
	Interval         int      `json:"interval"`         // seconds between metrics frames
	SnapshotInterval int      `json:"snapshotInterval"` // seconds between snapshots
	Units            []string `json:"units"`            // systemd units to report on (read-only)
}
