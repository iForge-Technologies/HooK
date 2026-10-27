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
)

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
	BootTime       int64  `json:"bootTime"` // unix seconds
	Probes         bool   `json:"probes"`   // will run confirmation checks for the hub
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
	Name  string  `json:"name"`
	Rx    float64 `json:"rx"` // bytes/s
	Tx    float64 `json:"tx"`
	RxErr uint64  `json:"rxErr"`
	TxErr uint64  `json:"txErr"`
	Drops uint64  `json:"drops"`
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
	Units          []Unit    `json:"units"`       // watched systemd units
	FailedUnits    []string  `json:"failedUnits"` // every unit systemd reports as failed
}

type Docker struct {
	Available  bool        `json:"available"`
	Error      string      `json:"error,omitempty"` // why it isn't available
	Containers []Container `json:"containers"`
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
