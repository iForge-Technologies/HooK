// Package fleet accepts agent connections, stores what they report, and turns
// it into online/offline status and threshold alerts.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"hook/internal/alert"
	"hook/internal/events"
	"hook/internal/proto"
	"hook/internal/store"
)

const (
	Interval         = 5                // seconds between agent frames
	SnapshotInterval = 30               // seconds between snapshots
	offlineAfter     = 60 * time.Second // silence before a server counts as offline
	startupGrace     = 90 * time.Second // let agents reconnect after a hub restart
	recoverHold      = time.Minute      // a breach must stay clear this long to resolve
	hysteresis       = 5.0              // and drop this many points below the limit
)

var metricKinds = []string{"cpu", "mem", "disk"}

type Fleet struct {
	store   *store.Store
	alerts  *alert.Dispatcher
	events  *events.Broker
	log     *slog.Logger
	started time.Time
	now     func() time.Time // swappable in tests

	mu      sync.Mutex
	conns   map[int64]*agentConn
	live    map[int64]*live
	pending map[string]pendingProbe // probe ID → waiting Probe call
	speeds  map[string]chan proto.SpeedResult
}

type agentConn struct {
	ws     *websocket.Conn
	cancel context.CancelFunc
	wmu    sync.Mutex // config can be sent from the handler and from SetHost
}

func (c *agentConn) send(typ string, data any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c.ws, proto.Envelope{Type: typ, Data: data})
}

func agentConfig(h *store.Host) proto.Config {
	return proto.Config{Interval: Interval, SnapshotInterval: SnapshotInterval, Units: h.Thresholds.Units}
}

type live struct {
	host        *store.Host
	metrics     *proto.Metrics
	lastSeen    time.Time
	persistedAt time.Time
	breach      map[string]*breach
	running     map[string]bool // containers by name, from the last snapshot
	restarts    map[string]int
}

type breach struct {
	since   time.Time // first sample at/above the limit
	okSince time.Time // first sample clearly below, while alerted
	alerted bool
	peak    float64
}

func New(st *store.Store, al *alert.Dispatcher, ev *events.Broker, log *slog.Logger) *Fleet {
	return &Fleet{store: st, alerts: al, events: ev, log: log, started: time.Now(), now: time.Now,
		conns: map[int64]*agentConn{}, live: map[int64]*live{}, pending: map[string]pendingProbe{},
		speeds: map[string]chan proto.SpeedResult{}}
}

// Start restores state from the database: hosts, last metrics and open alerts.
func (f *Fleet) Start() error {
	hosts, err := f.store.Hosts()
	if err != nil {
		return err
	}
	latest, err := f.store.LatestMetrics()
	if err != nil {
		return err
	}
	open, err := f.store.OpenHostEvents()
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range hosts {
		l := f.state(h)
		if m, ok := latest[h.ID]; ok {
			l.metrics = &m
		}
		l.lastSeen = time.UnixMilli(h.LastSeen)
	}
	for _, e := range open {
		if l, ok := f.live[e.HostID]; ok && e.Subject == "" && slices.Contains(metricKinds, e.Kind) {
			l.breach[e.Kind] = &breach{since: time.UnixMilli(e.StartedAt), alerted: true, peak: e.Peak}
		}
	}
	return nil
}

// state returns (creating) the runtime record for a host. Caller holds f.mu.
func (f *Fleet) state(h *store.Host) *live {
	l, ok := f.live[h.ID]
	if !ok {
		l = &live{breach: map[string]*breach{}}
		f.live[h.ID] = l
	}
	l.host = h
	return l
}

// SetHost refreshes cached settings after an edit and sends the connected
// agent its new unit list.
func (f *Fleet) SetHost(h *store.Host) {
	f.mu.Lock()
	f.state(h)
	c := f.conns[h.ID]
	f.mu.Unlock()
	if c != nil {
		if err := c.send(proto.TypeConfig, agentConfig(h)); err != nil {
			f.log.Warn("send agent config", "server", h.Name, "err", err)
		}
	}
}

// Remove forgets a deleted host and drops its connection.
func (f *Fleet) Remove(id int64) {
	f.Disconnect(id, "server deleted")
	f.mu.Lock()
	delete(f.live, id)
	f.mu.Unlock()
}

// Disconnect closes a host's agent connection, telling the agent why.
func (f *Fleet) Disconnect(id int64, reason string) {
	f.mu.Lock()
	c := f.conns[id]
	delete(f.conns, id)
	f.mu.Unlock()
	if c != nil {
		c.cancel()
		c.ws.Close(websocket.StatusPolicyViolation, reason)
	}
}

// Live returns the latest frame and whether an agent is connected right now.
func (f *Fleet) Live(id int64) (*proto.Metrics, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, connected := f.conns[id]
	if l, ok := f.live[id]; ok && l.metrics != nil {
		m := *l.metrics
		return &m, connected
	}
	return nil, connected
}

type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// HandleWS authenticates an agent by its bearer token and reads its stream.
func (f *Fleet) HandleWS(w http.ResponseWriter, r *http.Request, remoteIP string) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || !strings.HasPrefix(token, "hk_") {
		http.Error(w, "missing agent token", http.StatusUnauthorized)
		return
	}
	host, err := f.store.HostByToken(token)
	if err != nil {
		f.log.Warn("agent rejected: unknown token", "ip", remoteIP)
		http.Error(w, "unknown agent token", http.StatusUnauthorized)
		return
	}
	// The server's ReadTimeout would otherwise cut the hijacked connection.
	rc := http.NewResponseController(w)
	rc.SetReadDeadline(time.Time{})
	rc.SetWriteDeadline(time.Time{})
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &agentConn{ws: ws, cancel: cancel}
	f.mu.Lock()
	old := f.conns[host.ID]
	f.conns[host.ID] = c
	f.state(host).lastSeen = time.Now() // a fresh connection is a sign of life
	f.mu.Unlock()
	if old != nil { // a reinstalled or duplicate agent replaces the old stream
		old.cancel()
		old.ws.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
	defer func() {
		f.mu.Lock()
		if f.conns[host.ID] == c {
			delete(f.conns, host.ID)
		}
		f.mu.Unlock()
		ws.CloseNow()
		f.events.Publish(events.Event{Type: "hosts"})
	}()

	c.send(proto.TypeConfig, agentConfig(host))
	f.log.Info("agent connected", "server", host.Name, "ip", remoteIP)

	for {
		var env envelope
		if err := wsjson.Read(ctx, ws, &env); err != nil {
			if !errors.Is(err, context.Canceled) {
				f.log.Info("agent disconnected", "server", host.Name, "reason", closeReason(err))
			}
			return
		}
		if err := f.handle(host.ID, env, remoteIP); err != nil {
			f.log.Warn("bad agent message", "server", host.Name, "type", env.Type, "err", err)
		}
	}
}

func closeReason(err error) string {
	if s := websocket.CloseStatus(err); s != -1 {
		return s.String()
	}
	return err.Error()
}

func (f *Fleet) handle(id int64, env envelope, remoteIP string) error {
	now := f.now()
	// Any message proves the agent is alive. This must happen before a hello
	// flips the host online, or the watchdog could see "online but silent"
	// in the gap before the first metrics frame and mark it offline again.
	f.mu.Lock()
	if l := f.live[id]; l != nil {
		l.lastSeen = now
	}
	f.mu.Unlock()
	switch env.Type {
	case proto.TypeHello:
		var info proto.HostInfo
		if err := json.Unmarshal(env.Data, &info); err != nil {
			return err
		}
		if err := f.store.SaveHostInfo(id, info, remoteIP); err != nil {
			return err
		}
		f.mu.Lock()
		if l := f.live[id]; l != nil {
			l.host.Info, l.host.RemoteIP = info, remoteIP
		}
		f.mu.Unlock()
		f.markOnline(id, now)

	case proto.TypeMetrics:
		var m proto.Metrics
		if err := json.Unmarshal(env.Data, &m); err != nil {
			return err
		}
		ts := time.UnixMilli(m.TS)
		if ts.After(now.Add(time.Minute)) || ts.Before(now.Add(-2*time.Hour)) {
			m.TS, ts = now.UnixMilli(), now // agent clock is off; trust ours
		}
		if m.NetRxBytes == 0 && m.NetTxBytes == 0 { // older agent: estimate from the rates
			m.NetRxBytes, m.NetTxBytes = uint64(m.NetRx*Interval), uint64(m.NetTx*Interval)
		}
		if err := f.store.InsertMetrics(id, &m); err != nil {
			return err
		}
		f.mu.Lock()
		l := f.live[id]
		fresh := now.Sub(ts) < 2*time.Minute // replayed backlog doesn't alert
		if l != nil && (l.metrics == nil || m.TS >= l.metrics.TS) {
			l.metrics = &m
		}
		persist := false
		if l != nil {
			if now.Sub(l.persistedAt) > 30*time.Second {
				l.persistedAt, persist = now, true
			}
		}
		f.mu.Unlock()
		if persist {
			f.store.TouchHost(id, now.UnixMilli())
		}
		f.markOnline(id, now)
		if fresh && l != nil {
			f.evaluate(id, &m, ts)
			f.events.Publish(events.Event{Type: "host", Data: map[string]any{"hostId": id, "metrics": m}})
		}

	case proto.TypeProbeResult:
		var r proto.ProbeResult
		if err := json.Unmarshal(env.Data, &r); err != nil {
			return err
		}
		f.deliver(r)

	case proto.TypeSpeedResult:
		var r proto.SpeedResult
		if err := json.Unmarshal(env.Data, &r); err != nil {
			return err
		}
		f.deliverSpeed(r)

	case proto.TypeSnapshot:
		var s proto.Snapshot
		if err := json.Unmarshal(env.Data, &s); err != nil {
			return err
		}
		if err := f.store.SaveSnapshot(id, &s); err != nil {
			return err
		}
		f.inspect(id, &s, now)
		f.events.Publish(events.Event{Type: "hostSnapshot", Data: map[string]any{"hostId": id, "snapshot": s}})
	}
	return nil
}

// markOnline flips a host to online, closing an offline incident if there was one.
func (f *Fleet) markOnline(id int64, now time.Time) {
	f.mu.Lock()
	l := f.live[id]
	if l == nil || l.host.Status == "online" {
		f.mu.Unlock()
		return
	}
	h := *l.host
	l.host.Status, l.host.StatusSince = "online", now.UnixMilli()
	f.mu.Unlock()

	if err := f.store.SetHostStatus(id, "online", now.UnixMilli(), now.UnixMilli()); err != nil {
		f.log.Error("set host status", "err", err)
	}
	if h.Status == "offline" {
		started, _ := f.store.ResolveHostEvent(id, "offline", "", now.UnixMilli())
		if started > 0 && h.Thresholds.Offline {
			ev := alert.ServerEvent(alert.KindOnline, &h, now)
			ev.Downtime = now.Sub(time.UnixMilli(started))
			f.alerts.Notify(ev)
		}
		f.log.Info("server back online", "server", h.Name)
	}
	f.events.Publish(events.Event{Type: "hosts"})
}

// Run is the watchdog: servers that stop reporting go offline.
func (f *Fleet) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Since(f.started) < startupGrace {
			continue
		}
		f.sweep(f.now())
	}
}

// sweep marks online servers that have been silent too long as offline.
func (f *Fleet) sweep(now time.Time) {
	var gone []store.Host
	f.mu.Lock()
	for _, l := range f.live {
		if l.host.Status == "online" && now.Sub(l.lastSeen) > offlineAfter {
			l.host.Status, l.host.StatusSince = "offline", l.lastSeen.UnixMilli()
			gone = append(gone, *l.host)
		}
	}
	f.mu.Unlock()
	for _, h := range gone {
		f.Disconnect(h.ID, "no data received") // a silent socket is likely half-open
		f.store.SetHostStatus(h.ID, "offline", h.StatusSince, h.StatusSince)
		f.store.OpenHostEvent(h.ID, "offline", "", h.StatusSince, 0, "agent stopped reporting")
		f.log.Warn("server offline", "server", h.Name)
		if h.Thresholds.Offline {
			f.alerts.Notify(alert.ServerEvent(alert.KindOffline, &h, time.UnixMilli(h.StatusSince)))
		}
		f.events.Publish(events.Event{Type: "hosts"})
	}
}

func values(m *proto.Metrics) map[string]float64 {
	mem := 0.0
	if m.MemTotal > 0 {
		mem = m.MemUsed / m.MemTotal * 100
	}
	return map[string]float64{"cpu": m.CPU, "mem": mem, "disk": m.DiskPct}
}

// evaluate applies sustained-threshold rules to one fresh frame.
func (f *Fleet) evaluate(id int64, m *proto.Metrics, ts time.Time) {
	type fire struct {
		kind  string
		ev    alert.Event
		since time.Time
	}
	var out []fire
	vals := values(m)

	f.mu.Lock()
	l := f.live[id]
	if l == nil {
		f.mu.Unlock()
		return
	}
	th := l.host.Thresholds
	limits := map[string]float64{"cpu": th.CPU, "mem": th.Mem, "disk": th.Disk}
	sustain := time.Duration(max(1, th.Minutes)) * time.Minute
	h := *l.host
	for _, k := range metricKinds {
		v, lim := vals[k], limits[k]
		b := l.breach[k]
		if b == nil {
			b = &breach{}
			l.breach[k] = b
		}
		switch {
		case lim <= 0: // check turned off: close any open alert and forget the breach
			if b.alerted {
				out = append(out, fire{kind: "resolve", ev: recovered(&h, k, v, b, ts)})
			}
			*b = breach{}
		case v >= lim:
			b.okSince = time.Time{}
			if b.since.IsZero() {
				b.since = ts
			}
			b.peak = math.Max(b.peak, v)
			if !b.alerted && ts.Sub(b.since) >= sustain {
				b.alerted = true
				ev := alert.ServerEvent(alert.KindThreshold, &h, ts)
				ev.Metric, ev.Value, ev.Peak, ev.Limit, ev.Minutes = k, v, b.peak, lim, th.Minutes
				ev.Msg = detail(k, m)
				out = append(out, fire{kind: "open", ev: ev, since: b.since})
			} else if b.alerted {
				out = append(out, fire{kind: "peak", ev: alert.Event{Metric: k, Peak: b.peak}})
			}
		case b.alerted:
			if v < lim-hysteresis {
				if b.okSince.IsZero() {
					b.okSince = ts
				}
				if ts.Sub(b.okSince) >= recoverHold {
					out = append(out, fire{kind: "resolve", ev: recovered(&h, k, v, b, ts)})
					*b = breach{}
				}
			} else {
				b.okSince = time.Time{}
			}
		default:
			*b = breach{} // dipped below before alerting: start over
		}
	}
	f.mu.Unlock()

	for _, o := range out {
		switch o.kind {
		case "open":
			f.store.OpenHostEvent(id, o.ev.Metric, "", o.since.UnixMilli(), o.ev.Peak, o.ev.Msg)
			f.log.Warn("threshold breached", "server", h.Name, "metric", o.ev.Metric, "value", o.ev.Value)
			f.alerts.Notify(o.ev)
		case "peak":
			f.store.BumpHostEventPeak(id, o.ev.Metric, o.ev.Peak)
		case "resolve":
			f.store.ResolveHostEvent(id, o.ev.Metric, "", o.ev.At.UnixMilli())
			f.log.Info("threshold recovered", "server", h.Name, "metric", o.ev.Metric)
			f.alerts.Notify(o.ev)
		}
	}
	if len(out) > 0 {
		f.events.Publish(events.Event{Type: "hosts"})
	}
}

func recovered(h *store.Host, kind string, v float64, b *breach, ts time.Time) alert.Event {
	ev := alert.ServerEvent(alert.KindRecovered, h, ts)
	ev.Metric, ev.Value, ev.Peak, ev.Downtime = kind, v, b.peak, ts.Sub(b.since)
	return ev
}

// detail adds context to a threshold alert.
func detail(kind string, m *proto.Metrics) string {
	switch kind {
	case "cpu":
		if m.Steal >= 10 {
			return fmt.Sprintf("%.0f%% stolen by the hypervisor", m.Steal)
		}
		if m.IOWait >= 10 {
			return fmt.Sprintf("%.0f%% waiting on disk", m.IOWait)
		}
		return fmt.Sprintf("load %.2f", m.Load1)
	case "mem":
		if m.SwapTotal > 0 && m.SwapUsed/m.SwapTotal > 0.5 {
			return fmt.Sprintf("swap %.0f%% used", m.SwapUsed/m.SwapTotal*100)
		}
	}
	return ""
}
