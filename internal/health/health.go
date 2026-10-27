// Package health answers "is the hub itself working?" and runs the dead-man
// switch: while healthy, the hub pings an external URL you choose, so that
// service alerts you when the pings stop (the hub can't report its own death).
package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"hook/internal/store"
)

// Check is one self-check result.
type Check struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type Report struct {
	OK      bool             `json:"ok"`
	Checks  map[string]Check `json:"checks"`
	Uptime  int64            `json:"uptime"` // seconds since start
	Checked int64            `json:"checked"`
}

// Deadman is the outbound heartbeat's last outcome.
type Deadman struct {
	Configured bool   `json:"configured"`
	URLHost    string `json:"urlHost,omitempty"` // host only: the URL itself is a secret
	Every      int    `json:"every"`             // seconds
	LastPing   int64  `json:"lastPing"`
	LastOK     bool   `json:"lastOk"`
	LastError  string `json:"lastError,omitempty"`
	SentFail   bool   `json:"sentFail"` // the last ping reported the hub as unhealthy
}

type Monitor struct {
	st        *store.Store
	dataDir   string
	lastBeat  func() time.Time // when the scheduler last finished a check
	monitors  func() int       // active monitors
	started   time.Time
	url, fail string
	every     time.Duration
	client    *http.Client
	log       *slog.Logger

	mu      sync.Mutex
	last    Report
	deadman Deadman
}

type Options struct {
	DataDir  string
	URL      string // pinged while healthy
	FailURL  string // pinged while unhealthy (optional, e.g. healthchecks.io …/fail)
	Every    time.Duration
	LastBeat func() time.Time
	Monitors func() int
}

func New(st *store.Store, o Options, log *slog.Logger) *Monitor {
	if o.Every < 10*time.Second {
		o.Every = time.Minute
	}
	m := &Monitor{st: st, dataDir: o.DataDir, lastBeat: o.LastBeat, monitors: o.Monitors, started: time.Now(),
		url: o.URL, fail: o.FailURL, every: o.Every, client: &http.Client{Timeout: 10 * time.Second}, log: log}
	m.deadman = Deadman{Configured: o.URL != "", URLHost: host(o.URL), Every: int(o.Every.Seconds())}
	return m
}

func host(u string) string {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil || u == "" {
		return ""
	}
	return req.URL.Host
}

// Check runs the self-checks now.
func (m *Monitor) Check() Report {
	now := time.Now()
	r := Report{OK: true, Checks: map[string]Check{}, Uptime: int64(now.Sub(m.started).Seconds()), Checked: now.UnixMilli()}
	add := func(name string, c Check) {
		r.Checks[name] = c
		r.OK = r.OK && c.OK
	}

	if err := m.st.Ping(); err != nil {
		add("database", Check{Detail: "can't write: " + err.Error()})
	} else {
		add("database", Check{OK: true, Detail: "reads and writes"})
	}

	// The scheduler stalls silently if, say, the process deadlocks: if there are
	// monitors but nothing has finished for 10 minutes, something is wrong.
	n := m.monitors()
	last := m.lastBeat()
	switch {
	case n == 0:
		add("scheduler", Check{OK: true, Detail: "no active monitors"})
	case now.Sub(m.started) < 10*time.Minute && last.IsZero():
		add("scheduler", Check{OK: true, Detail: "starting"})
	case now.Sub(last) > 10*time.Minute:
		add("scheduler", Check{Detail: fmt.Sprintf("no check finished in %s", now.Sub(last).Round(time.Second))})
	default:
		add("scheduler", Check{OK: true, Detail: fmt.Sprintf("%d monitors, last check %s ago", n, now.Sub(last).Round(time.Second))})
	}

	// SQLite on a full disk fails in ugly ways; warn well before that.
	if u, err := disk.Usage(m.dataDir); err == nil {
		free := u.Free
		ok := free > 200<<20 && u.UsedPercent < 98
		add("disk", Check{OK: ok, Detail: fmt.Sprintf("%.1f GB free on the data disk (%.0f%% used)", float64(free)/(1<<30), u.UsedPercent)})
	}

	m.mu.Lock()
	m.last = r
	m.mu.Unlock()
	return r
}

func (m *Monitor) Last() (Report, Deadman) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last, m.deadman
}

// Run checks every interval and pings the dead-man URL. When unhealthy it
// pings the fail URL if there is one, and otherwise stays silent so the
// external service notices the missing ping.
func (m *Monitor) Run(ctx context.Context) {
	tick := time.NewTicker(m.every)
	defer tick.Stop()
	for {
		m.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// pass runs the self-checks once and sends the matching ping.
func (m *Monitor) pass(ctx context.Context) {
	r := m.Check()
	if !r.OK {
		m.log.Error("hub unhealthy", "checks", r.Checks)
	}
	if m.url == "" {
		return
	}
	target := m.url
	if !r.OK {
		target = m.fail
	}
	if target == "" {
		return // stay silent: the external service alerts on the missing ping
	}
	err := m.ping(ctx, target)
	m.mu.Lock()
	m.deadman.LastPing, m.deadman.LastOK, m.deadman.SentFail = time.Now().UnixMilli(), err == nil, !r.OK
	m.deadman.LastError = ""
	if err != nil {
		m.deadman.LastError = err.Error()
	}
	m.mu.Unlock()
	if err != nil {
		m.log.Warn("dead-man ping failed", "host", host(target), "err", err)
	}
}

func (m *Monitor) ping(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Hook-Hub/deadman")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
