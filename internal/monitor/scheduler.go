// Package monitor runs one goroutine per active monitor, turns check results
// into status transitions, and raises incidents and alerts.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hook/internal/alert"
	"hook/internal/checks"
	"hook/internal/events"
	"hook/internal/fleet"
	"hook/internal/maint"
	"hook/internal/platform"
	"hook/internal/probe"
	"hook/internal/store"
)

// Days-left thresholds for certificate alerts, largest first.
var certThresholds = []int{14, 7, 3, 1}

// Prober runs a network check from other locations (the fleet's agents).
type Prober interface {
	Probe(ctx context.Context, spec probe.Spec, n int) []fleet.Outcome
}

type Scheduler struct {
	store  *store.Store
	alerts *alert.Dispatcher
	events *events.Broker
	log    *slog.Logger
	prober Prober          // nil: single-location checks only
	cal    *maint.Calendar // nil: no maintenance windows

	ctx      context.Context
	mu       sync.Mutex
	workers  map[int64]*worker
	lastBeat atomic.Int64 // unix ms of the last finished check, for the health check
}

// LastBeat is when any monitor last finished a check.
func (s *Scheduler) LastBeat() time.Time {
	if ms := s.lastBeat.Load(); ms > 0 {
		return time.UnixMilli(ms)
	}
	return time.Time{}
}

// Active counts running monitors.
func (s *Scheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workers)
}

type worker struct {
	cancel context.CancelFunc
	kick   chan struct{}
	push   chan checks.Result // push monitors: results reported by the job
}

func New(s *store.Store, a *alert.Dispatcher, e *events.Broker, p Prober, cal *maint.Calendar, log *slog.Logger) *Scheduler {
	return &Scheduler{store: s, alerts: a, events: e, prober: p, cal: cal, log: log, workers: map[int64]*worker{}}
}

func (s *Scheduler) maintenance(id int64, t time.Time) *store.Maintenance {
	if s.cal == nil {
		return nil
	}
	return s.cal.Monitor(id, t)
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.ctx = ctx
	ms, err := s.store.Monitors()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range ms {
		if m.Active {
			s.spawn(m)
		}
	}
	s.log.Info("scheduler started", "monitors", len(s.workers))
	return nil
}

// Reload restarts a monitor's worker from its stored settings (or stops it if
// it was paused or deleted).
func (s *Scheduler) Reload(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop(id)
	m, err := s.store.Monitor(id)
	if err != nil || !m.Active {
		return
	}
	s.spawn(m)
}

func (s *Scheduler) Remove(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop(id)
}

// Push delivers a result reported to a push monitor's URL. It returns false
// when the monitor is paused or isn't a push monitor.
func (s *Scheduler) Push(id int64, res checks.Result) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	if !ok || w.push == nil {
		return false
	}
	select {
	case w.push <- res:
	default: // a burst of pushes: one is plenty
	}
	return true
}

// CheckNow runs a check immediately; false when the monitor isn't running.
func (s *Scheduler) CheckNow(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	if !ok {
		return false
	}
	select {
	case w.kick <- struct{}{}:
	default:
	}
	return true
}

func (s *Scheduler) stop(id int64) {
	if w, ok := s.workers[id]; ok {
		w.cancel()
		delete(s.workers, id)
	}
}

func (s *Scheduler) spawn(m *store.Monitor) {
	ctx, cancel := context.WithCancel(s.ctx)
	w := &worker{cancel: cancel, kick: make(chan struct{}, 1)}
	s.workers[m.ID] = w
	if m.Type == "push" {
		w.push = make(chan checks.Result, 1)
		go s.runPush(ctx, m, w)
		return
	}
	go s.run(ctx, m, w)
}

// runPush waits for the job to report. Silence for longer than the interval
// (plus 10% slack) counts as a failed check, repeated every interval.
func (s *Scheduler) runPush(ctx context.Context, m *store.Monitor, w *worker) {
	period := time.Duration(m.Interval) * time.Second
	grace := period + max(period/10, 10*time.Second)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	st := &state{status: m.Status, since: m.StatusSince}
	for {
		select {
		case <-ctx.Done():
			return
		case res := <-w.push:
			s.process(m, st, res)
			timer.Reset(grace)
		case <-timer.C:
			s.process(m, st, checks.Result{Msg: "No push received in " + alert.HumanDuration(grace)})
			timer.Reset(period)
		}
	}
}

// probe runs one check, resolving the platform account for deploy monitors
// and confirming network failures from other locations.
func (s *Scheduler) probe(ctx context.Context, m *store.Monitor) checks.Result {
	if m.Type != "vercel" && m.Type != "render" {
		return s.confirm(ctx, m, checks.Run(ctx, m))
	}
	acct, err := s.store.Integration(m.Config.IntegrationID)
	if err != nil {
		return checks.Result{Msg: "The connected " + m.Type + " account was removed"}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.Timeout)*time.Second)
	defer cancel()
	return platform.Check(ctx, acct, m.Config.ProjectID)
}

func (s *Scheduler) run(ctx context.Context, m *store.Monitor, w *worker) {
	// Spread first checks so a restart doesn't fire everything at once.
	spread := min(m.Interval, 10)
	timer := time.NewTimer(time.Duration(rand.Int64N(int64(spread)*int64(time.Second)) + int64(200*time.Millisecond)))
	defer timer.Stop()
	st := &state{status: m.Status, since: m.StatusSince}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-w.kick:
			timer.Stop()
		}
		res := s.probe(ctx, m)
		if ctx.Err() != nil {
			return // paused, edited or deleted mid-check: drop the result
		}
		timer.Reset(s.process(m, st, res))
	}
}

type state struct {
	status string
	since  int64
	fails  int
}

// process records a result and returns the delay until the next check.
func (s *Scheduler) process(m *store.Monitor, st *state, res checks.Result) time.Duration {
	now := time.Now()
	s.lastBeat.Store(now.UnixMilli())
	lat := int(res.Latency.Milliseconds())
	prev := st.status
	var next string
	msg := res.Msg
	if res.OK {
		st.fails = 0
		next = "up"
		if res.Partial || (m.DegradedMs > 0 && lat > m.DegradedMs) {
			next = "degraded"
		}
	} else {
		st.fails++
		if prev == "down" || st.fails > m.Retries {
			next = "down"
		} else {
			next = "pending"
		}
	}
	// In a maintenance window the check still runs (so you can watch it come
	// back), but the monitor shows "maintenance", nothing alerts, and the beat
	// counts neither up nor down for uptime. An outage that was open when the
	// window started is closed, since its recovery won't be seen as one.
	if w := s.maintenance(m.ID, now); w != nil {
		if prev != "maintenance" {
			s.store.CloseOpenIncidents(m.ID)
		}
		st.fails = 0
		next = "maintenance"
		msg = w.Title + " · " + msg
	}
	if next != prev {
		st.since = now.UnixMilli()
	}
	st.status = next

	hb := &store.Heartbeat{MonitorID: m.ID, TS: now.UnixMilli(), Status: next, Latency: lat, Code: res.Code, Msg: msg, Timings: res.Timings}
	if err := s.store.InsertHeartbeat(hb); err != nil {
		s.log.Error("save heartbeat", "monitor", m.ID, "err", err)
	}
	if err := s.store.SaveState(m.ID, store.MonitorState{Status: next, StatusSince: st.since, LastCheck: hb.TS, LastLatency: lat, LastMsg: msg}); err != nil {
		s.log.Error("save state", "monitor", m.ID, "err", err)
	}

	switch {
	case next == "down" && prev != "down":
		if err := s.store.OpenIncident(m.ID, hb.TS, msg); err != nil {
			s.log.Error("open incident", "monitor", m.ID, "err", err)
		}
		s.log.Warn("monitor down", "monitor", m.Name, "reason", msg)
		ev := alert.MonitorEvent(alert.KindDown, m, now)
		ev.Msg = msg
		s.alerts.Notify(ev)
	case prev == "down" && (next == "up" || next == "degraded"):
		started, err := s.store.ResolveIncident(m.ID, hb.TS)
		if err != nil {
			s.log.Error("resolve incident", "monitor", m.ID, "err", err)
		}
		var downtime time.Duration
		if started > 0 {
			downtime = now.Sub(time.UnixMilli(started))
		}
		s.log.Info("monitor recovered", "monitor", m.Name, "downtime", downtime)
		ev := alert.MonitorEvent(alert.KindUp, m, now)
		ev.Msg, ev.Downtime = msg, downtime
		s.alerts.Notify(ev)
	}

	if !res.CertExpires.IsZero() {
		s.handleCert(m, res, now)
	}

	s.events.Publish(events.Event{Type: "beat", Data: map[string]any{
		"monitorId": m.ID, "beat": hb, "status": next, "statusSince": st.since,
		"certExpires": m.CertExpires, "certIssuer": m.CertIssuer,
	}})

	if next == "pending" && m.RetryInterval > 0 {
		return time.Duration(m.RetryInterval) * time.Second
	}
	return time.Duration(m.Interval) * time.Second
}

func (s *Scheduler) handleCert(m *store.Monitor, res checks.Result, now time.Time) {
	exp := res.CertExpires.UnixMilli()
	days := int(res.CertExpires.Sub(now).Hours() / 24)
	notified := m.CertNotified
	if days > certThresholds[0] {
		notified = 0 // renewed (or never close): re-arm alerts
	}
	if m.Config.CertAlerts {
		hit := 0
		for _, t := range certThresholds {
			if days <= t {
				hit = t
			}
		}
		if hit > 0 && (notified == 0 || hit < notified) {
			notified = hit
			m.CertExpires, m.CertIssuer = exp, res.CertIssuer
			ev := alert.MonitorEvent(alert.KindCert, m, now)
			ev.CertDays = days
			s.alerts.Notify(ev)
		}
	}
	if exp != m.CertExpires || res.CertIssuer != m.CertIssuer || notified != m.CertNotified {
		m.CertExpires, m.CertIssuer, m.CertNotified = exp, res.CertIssuer, notified
		if err := s.store.SaveCert(m.ID, exp, res.CertIssuer, notified); err != nil {
			s.log.Error("save cert", "monitor", m.ID, "err", err)
		}
	}
}

// Server metrics: raw 5 s frames for 48 h, minute rollups 14 days, hourly 2 years.
const hostRawRetention = 48 * time.Hour

// Maintain keeps rollups fresh and enforces retention.
func Maintain(ctx context.Context, st *store.Store, rawDays int, log *slog.Logger) {
	tick := time.NewTicker(20 * time.Second) // uptime % lags live status by at most this
	defer tick.Stop()
	// First pass rebuilds anything missed while the hub was down.
	since := time.Now().Add(-time.Duration(rawDays) * 24 * time.Hour)
	var lastPrune time.Time
	for {
		if err := st.RefreshRollups(since.UnixMilli()); err != nil {
			log.Error("refresh rollups", "err", err)
		}
		hostSince := since
		if floor := time.Now().Add(-hostRawRetention); hostSince.Before(floor) {
			hostSince = floor
		}
		if err := st.RefreshHostRollups(hostSince.UnixMilli()); err != nil {
			log.Error("refresh host rollups", "err", err)
		}
		since = time.Now().Add(-2 * time.Hour)
		if time.Since(lastPrune) > time.Hour {
			raw := time.Now().Add(-time.Duration(rawDays) * 24 * time.Hour).UnixMilli()
			roll := time.Now().AddDate(-2, 0, 0).UnixMilli()
			if err := st.Prune(raw, roll); err != nil {
				log.Error("prune", "err", err)
			}
			now := time.Now()
			if err := st.PruneHosts(now.Add(-hostRawRetention).UnixMilli(), now.AddDate(0, 0, -14).UnixMilli(), roll); err != nil {
				log.Error("prune hosts", "err", err)
			}
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// confirm asks up to three other locations to re-check a failed network
// monitor. Down needs at least two locations to agree: if every remote location
// reaches the target, the problem is between the hub and the target, so the
// check counts as "slow" (up, but flagged) rather than paging anyone. Monitors
// with custom headers aren't confirmed remotely, since headers (often auth)
// never leave the hub.
func (s *Scheduler) confirm(ctx context.Context, m *store.Monitor, res checks.Result) checks.Result {
	if res.OK || s.prober == nil || !checks.IsNetwork(m.Type) || m.Config.NoConfirm || len(m.Config.Headers) > 0 {
		return res
	}
	outs := s.prober.Probe(ctx, checks.SpecFor(m), 3)
	if len(outs) == 0 {
		return res
	}
	var ok, failed []string
	var best time.Duration
	for _, o := range outs {
		if o.OK {
			ok = append(ok, fmt.Sprintf("%s (%d ms)", o.Location, o.Latency.Milliseconds()))
			if best == 0 || o.Latency < best {
				best = o.Latency
			}
		} else {
			failed = append(failed, o.Location)
		}
	}
	if len(failed) == 0 {
		return checks.Result{OK: true, Partial: true, Latency: best, Code: res.Code,
			Msg: fmt.Sprintf("Failed from the hub (%s) but reachable from %s", res.Msg, strings.Join(ok, ", "))}
	}
	res.Msg = fmt.Sprintf("%s · confirmed from %s", res.Msg, strings.Join(failed, ", "))
	if len(ok) > 0 {
		res.Msg += "; reachable from " + strings.Join(ok, ", ")
	}
	return checks.Result(probe.Clip(res))
}
