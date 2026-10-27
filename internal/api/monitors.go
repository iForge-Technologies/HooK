package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"hook/internal/alert"
	"hook/internal/checks"
	"hook/internal/events"
	"hook/internal/store"
)

// mask replaces secret values sent to the browser; sending it back means "keep".
const mask = "••••••••"

type monitorView struct {
	*store.Monitor
	Beats      []store.Heartbeat `json:"beats"`
	Uptime24h  *float64          `json:"uptime24h"`
	AvgLatency *int              `json:"avgLatency24h"`
}

func masked(m *store.Monitor) *store.Monitor {
	c := *m
	if c.Config.DSN != "" {
		c.Config.DSN = mask
	}
	if len(m.Config.Headers) > 0 {
		c.Config.Headers = map[string]string{}
		for k := range m.Config.Headers {
			c.Config.Headers[k] = mask
		}
	}
	return &c
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request, se *session) {
	groups, err := s.store.Groups()
	if err != nil {
		s.internal(w, err)
		return
	}
	types := make([]string, 0, len(alert.Types))
	for k := range alert.Types {
		types = append(types, k)
	}
	slices.Sort(types)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "publicUrl": s.cfg.PublicURL, "groups": groups, "locations": s.fleet.Locations(),
		"checkTypes": checks.Types, "dnsRecords": checks.DNSRecords, "channelTypes": types,
	})
}

func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request, se *session) {
	ms, err := s.store.Monitors()
	if err != nil {
		s.internal(w, err)
		return
	}
	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	up, err := s.store.Uptime(since)
	if err != nil {
		s.internal(w, err)
		return
	}
	lat, err := s.store.AvgLatency(since)
	if err != nil {
		s.internal(w, err)
		return
	}
	out := make([]monitorView, 0, len(ms))
	for _, m := range ms {
		beats, err := s.store.RecentBeats(m.ID, 60)
		if err != nil {
			s.internal(w, err)
			return
		}
		for i := range beats {
			beats[i].Timings = nil // the list only needs status and latency
		}
		v := monitorView{Monitor: masked(m), Beats: beats}
		if u, ok := up[m.ID]; ok {
			v.Uptime24h = &u
		}
		if l, ok := lat[m.ID]; ok {
			v.AvgLatency = &l
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	m, err := s.store.Monitor(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	uptime := map[string]*float64{}
	for label, d := range map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour} {
		up, err := s.store.Uptime(time.Now().Add(-d).UnixMilli())
		if err != nil {
			s.internal(w, err)
			return
		}
		if u, ok := up[id]; ok {
			uptime[label] = &u
		} else {
			uptime[label] = nil
		}
	}
	lat, err := s.store.AvgLatency(time.Now().Add(-24 * time.Hour).UnixMilli())
	if err != nil {
		s.internal(w, err)
		return
	}
	beats, err := s.store.RecentBeats(id, 100)
	if err != nil {
		s.internal(w, err)
		return
	}
	incidents, err := s.store.Incidents(id, 20)
	if err != nil {
		s.internal(w, err)
		return
	}
	var avg *int
	if l, ok := lat[id]; ok {
		avg = &l
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"monitor": masked(m), "uptime": uptime, "avgLatency24h": avg, "beats": beats, "incidents": incidents,
	})
}

type seriesPoint struct {
	TS      int64  `json:"ts"`
	Latency int    `json:"latency"`
	Max     int    `json:"max,omitempty"`
	Status  string `json:"status"`
	Msg     string `json:"msg,omitempty"`
	Up      int    `json:"up,omitempty"`
	Down    int    `json:"down,omitempty"`
}

// monitorSeries returns response times: raw beats up to 24h, hourly rollups beyond.
func (s *Server) monitorSeries(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ranges := map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour}
	rng := r.URL.Query().Get("range")
	d, known := ranges[rng]
	if !known {
		rng, d = "24h", 24*time.Hour
	}
	since := time.Now().Add(-d).UnixMilli()
	points := []seriesPoint{}
	resolution := "raw"
	if d <= 24*time.Hour {
		beats, err := s.store.BeatsSince(id, since)
		if err != nil {
			s.internal(w, err)
			return
		}
		for _, b := range beats {
			p := seriesPoint{TS: b.TS, Latency: b.Latency, Status: b.Status}
			if b.Status != "up" {
				p.Msg = b.Msg
			}
			points = append(points, p)
		}
	} else {
		resolution = "1h"
		rolls, err := s.store.Rollups(id, since)
		if err != nil {
			s.internal(w, err)
			return
		}
		for _, ro := range rolls {
			st := "up"
			if ro.Down > 0 {
				st = "down"
			} else if ro.Degraded > 0 {
				st = "degraded"
			}
			points = append(points, seriesPoint{TS: ro.Hour, Latency: ro.AvgLat, Max: ro.MaxLat, Status: st, Up: ro.Up, Down: ro.Down})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rng, "resolution": resolution, "points": points})
}

// ---- create / update ----

var methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		v = def
	}
	return max(lo, min(hi, v))
}

// normalize validates input and fills defaults in place.
func normalize(m *store.Monitor) error {
	m.Name = strings.TrimSpace(m.Name)
	m.Target = strings.TrimSpace(m.Target)
	m.Group = strings.TrimSpace(m.Group)
	if m.Name == "" || len(m.Name) > 100 {
		return errors.New("name is required (max 100 characters)")
	}
	if len(m.Group) > 50 {
		return errors.New("group is too long (max 50 characters)")
	}
	if !slices.Contains(checks.Types, m.Type) {
		return fmt.Errorf("unknown type %q", m.Type)
	}
	maxInterval := 86400
	if m.Type == "push" {
		maxInterval = 30 * 86400 // daily and weekly jobs
	}
	m.Interval = clamp(m.Interval, 20, maxInterval, 60)
	if m.Type == "vercel" || m.Type == "render" {
		m.Interval = max(m.Interval, 60) // be kind to platform API quotas
	}
	m.RetryInterval = clamp(m.RetryInterval, 20, 86400, m.Interval)
	m.Timeout = clamp(m.Timeout, 1, min(120, m.Interval), min(30, m.Interval))
	m.Retries = max(0, min(10, m.Retries))
	m.DegradedMs = max(0, min(120000, m.DegradedMs))

	c := &m.Config
	switch m.Type {
	case "http", "keyword":
		u, err := url.Parse(m.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("URL must start with http:// or https://")
		}
		c.Method = strings.ToUpper(strings.TrimSpace(c.Method))
		if c.Method == "" {
			c.Method = "GET"
		}
		if !slices.Contains(methods, c.Method) {
			return fmt.Errorf("unsupported method %s", c.Method)
		}
		if !checks.ValidStatusSpec(c.ExpectedStatus) {
			return errors.New("accepted status codes look like 200-299, 301 or 2xx")
		}
		if m.Type == "keyword" && strings.TrimSpace(c.Keyword) == "" {
			return errors.New("keyword is required")
		}
		if len(c.Body) > 64<<10 {
			return errors.New("request body is too large (max 64 KB)")
		}
		for k := range c.Headers {
			if strings.TrimSpace(k) == "" || strings.ContainsAny(k, " :\r\n") {
				return fmt.Errorf("invalid header name %q", k)
			}
		}
		if len(c.Headers) == 0 {
			c.Headers = nil // match what a stored config decodes to
		}
		c.DNSRecord, c.DNSResolver, c.DNSExpected = "", "", ""
		if m.Type == "http" {
			c.Keyword, c.KeywordInvert = "", false
		}
	case "tcp":
		host, port, err := net.SplitHostPort(m.Target)
		if p, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || p < 1 || p > 65535 {
			return errors.New("target must be host:port, e.g. db.example.com:5432")
		}
		m.Config = store.MonitorConfig{NoConfirm: c.NoConfirm}
	case "tls":
		host := m.Target
		if h, port, err := net.SplitHostPort(m.Target); err == nil {
			if p, perr := strconv.Atoi(port); perr != nil || p < 1 || p > 65535 {
				return errors.New("invalid port")
			}
			host = h
		}
		if host == "" || strings.Contains(host, "/") {
			return errors.New("target must be a hostname, optionally with :port")
		}
		m.Config = store.MonitorConfig{IgnoreTLS: c.IgnoreTLS, CertAlerts: c.CertAlerts, NoConfirm: c.NoConfirm}
	case "dns":
		if m.Target == "" || strings.ContainsAny(m.Target, "/: ") {
			return errors.New("target must be a hostname like example.com")
		}
		c.DNSRecord = strings.ToUpper(c.DNSRecord)
		if c.DNSRecord == "" {
			c.DNSRecord = "A"
		}
		if !slices.Contains(checks.DNSRecords, c.DNSRecord) {
			return fmt.Errorf("unsupported record type %s", c.DNSRecord)
		}
		m.Config = store.MonitorConfig{DNSRecord: c.DNSRecord, DNSResolver: strings.TrimSpace(c.DNSResolver), DNSExpected: strings.TrimSpace(c.DNSExpected), NoConfirm: c.NoConfirm}
	case "postgres", "mysql", "redis", "mongodb":
		dsn := strings.TrimSpace(c.DSN)
		if dsn == "" {
			return errors.New("connection string is required")
		}
		examples := map[string]string{
			"postgres": "postgres://user:pass@host:5432/db",
			"mysql":    "mysql://user:pass@host:3306/db",
			"redis":    "redis://:pass@host:6379/0",
			"mongodb":  "mongodb+srv://user:pass@cluster.example.net/",
		}
		valid := false
		switch m.Type {
		case "postgres":
			valid = strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") || strings.Contains(dsn, "host=")
		case "mysql":
			_, err := checks.MySQLDSN(dsn)
			valid = err == nil
		case "redis":
			valid = strings.HasPrefix(dsn, "redis://") || strings.HasPrefix(dsn, "rediss://")
		case "mongodb":
			valid = strings.HasPrefix(dsn, "mongodb://") || strings.HasPrefix(dsn, "mongodb+srv://")
		}
		if !valid {
			return fmt.Errorf("connection string should look like %s", examples[m.Type])
		}
		m.Target = checks.RedactDSN(m.Type, dsn)
		m.Config = store.MonitorConfig{DSN: dsn}
	case "push":
		m.Target = "push"
		m.Config = store.MonitorConfig{PushToken: c.PushToken}
	case "vercel", "render":
		if c.IntegrationID == 0 || strings.TrimSpace(c.ProjectID) == "" {
			return errors.New("choose an account and a project")
		}
		name := strings.TrimSpace(c.ProjectName)
		if name == "" {
			name = c.ProjectID
		}
		m.Target = name
		m.Config = store.MonitorConfig{IntegrationID: c.IntegrationID, ProjectID: strings.TrimSpace(c.ProjectID), ProjectName: name}
	}
	if m.ChannelIDs == nil {
		m.ChannelIDs = []int64{}
	}
	return nil
}

func (s *Server) createMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	var m store.Monitor
	if err := readJSON(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.Active = true
	m.Config.PushToken = ""
	if err := normalize(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.checkIntegration(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if m.Type == "push" {
		m.Config.PushToken = newToken()
	}
	if err := s.store.CreateMonitor(&m); err != nil {
		s.internal(w, err)
		return
	}
	if m.Type == "push" {
		if err := s.store.SetPushToken(m.ID, m.Config.PushToken); err != nil {
			s.internal(w, err)
			return
		}
	}
	s.sched.Reload(m.ID)
	s.events.Publish(events.Event{Type: "monitors"})
	writeJSON(w, http.StatusCreated, map[string]int64{"id": m.ID})
}

func (s *Server) updateMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	old, err := s.store.Monitor(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	var m store.Monitor
	if err := readJSON(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID, m.Active = id, old.Active
	for k, v := range m.Config.Headers {
		if v == mask {
			m.Config.Headers[k] = old.Config.Headers[k]
		}
	}
	if m.Config.DSN == mask {
		m.Config.DSN = old.Config.DSN
	}
	// The push token can only be issued by the hub.
	m.Config.PushToken = old.Config.PushToken
	if m.Type == "push" && m.Config.PushToken == "" {
		m.Config.PushToken = newToken()
	}
	if err := normalize(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.checkIntegration(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// A different check makes the old status meaningless.
	reset := m.Type != old.Type || m.Target != old.Target || !reflect.DeepEqual(m.Config, old.Config)
	if err := s.store.UpdateMonitor(&m, reset); err != nil {
		s.internal(w, err)
		return
	}
	if reset {
		s.store.CloseOpenIncidents(id)
	}
	if m.Type == "push" {
		s.store.SetPushToken(id, m.Config.PushToken)
	}
	s.sched.Reload(id)
	s.events.Publish(events.Event{Type: "monitors"})
	ok(w)
}

func (s *Server) deleteMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.sched.Remove(id)
	if err := s.store.DeleteMonitor(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.events.Publish(events.Event{Type: "monitors"})
	ok(w)
}

func (s *Server) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !active {
		s.sched.Remove(id) // stop first so an in-flight check can't overwrite "paused"
	}
	if err := s.store.SetMonitorActive(id, active); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.store.CloseOpenIncidents(id)
	if active {
		s.sched.Reload(id)
	}
	s.events.Publish(events.Event{Type: "monitors"})
	ok(w)
}

func (s *Server) pauseMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	s.setActive(w, r, false)
}

func (s *Server) resumeMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	s.setActive(w, r, true)
}

func (s *Server) checkMonitor(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if m, err := s.store.Monitor(id); err == nil && m.Type == "push" {
		writeErr(w, http.StatusBadRequest, "Push monitors are checked when your job calls its URL")
		return
	}
	if !valid || !s.sched.CheckNow(id) {
		writeErr(w, http.StatusConflict, "monitor is paused or missing")
		return
	}
	ok(w)
}
