package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"hook/internal/store"
)

func (s *Server) listKioskLinks(w http.ResponseWriter, r *http.Request, se *session) {
	ks, err := s.store.KioskLinks()
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ks)
}

// createKioskLink returns the token once; only its hash is stored.
func (s *Server) createKioskLink(w http.ResponseWriter, r *http.Request, se *session) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 60 {
		writeErr(w, http.StatusBadRequest, "give the screen a name (up to 60 characters)")
		return
	}
	token := newToken()
	k, err := s.store.CreateKioskLink(name, token)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": k.ID, "token": token})
}

func (s *Server) deleteKioskLink(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteKioskLink(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "kiosk link not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	ok(w)
}

type kioskMonitor struct {
	Name     string   `json:"name"`
	Group    string   `json:"group"`
	Status   string   `json:"status"`
	Since    int64    `json:"since"`
	Latency  *int     `json:"latency,omitempty"`
	Uptime24 *float64 `json:"uptime24h,omitempty"`
	Beats    []string `json:"beats"` // last 30 results, oldest first
}

type kioskServer struct {
	Name      string   `json:"name"`
	Group     string   `json:"group"`
	Status    string   `json:"status"` // new | online | offline
	Since     int64    `json:"since"`
	CPU       *float64 `json:"cpu,omitempty"`
	Mem       *float64 `json:"mem,omitempty"`
	Disk      *float64 `json:"disk,omitempty"`
	Open      []string `json:"open"` // open alert kinds: cpu, disk, container…
	Connected bool     `json:"connected"`
}

type kioskBoard struct {
	Name      string         `json:"name"`
	Overall   string         `json:"overall"` // up | degraded | down
	UpdatedAt int64          `json:"updatedAt"`
	Monitors  []kioskMonitor `json:"monitors"`
	Servers   []kioskServer  `json:"servers"`
}

// The board is the same for every screen, so screens share one build of it.
var kioskCache struct {
	sync.Mutex
	at    time.Time
	board kioskBoard
}

// kiosk serves a wall display without a session. Like status pages it carries
// display data only: names, states and gauges; never targets, IPs or errors.
func (s *Server) kiosk(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	link, err := s.store.KioskByToken(r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "this kiosk link doesn't exist or was revoked")
		return
	}
	if err != nil { // a busy database isn't a revoked link
		writeErr(w, http.StatusServiceUnavailable, "the hub is busy, try again shortly")
		return
	}
	kioskCache.Lock()
	defer kioskCache.Unlock()
	if time.Since(kioskCache.at) > 5*time.Second {
		b, err := s.buildKiosk()
		if err != nil {
			s.internal(w, err)
			return
		}
		kioskCache.board, kioskCache.at = b, time.Now()
	}
	b := kioskCache.board
	b.Name = link.Name
	body, err := json.Marshal(b)
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(body)
}

func (s *Server) buildKiosk() (kioskBoard, error) {
	now := time.Now()
	b := kioskBoard{Overall: "up", UpdatedAt: now.UnixMilli(), Monitors: []kioskMonitor{}, Servers: []kioskServer{}}
	worse := func(st string) {
		if st == "down" || (st == "degraded" && b.Overall == "up") {
			b.Overall = st
		}
	}

	ms, err := s.store.Monitors()
	if err != nil {
		return b, err
	}
	since := now.Add(-24 * time.Hour).UnixMilli()
	up, err := s.store.Uptime(since)
	if err != nil {
		return b, err
	}
	lat, _ := s.store.AvgLatency(since)
	for _, m := range ms {
		km := kioskMonitor{Name: m.Name, Group: m.Group, Status: m.Status, Since: m.StatusSince, Beats: []string{}}
		if !m.Active {
			km.Status = "paused"
		}
		if u, ok := up[m.ID]; ok {
			km.Uptime24 = &u
		}
		if l, ok := lat[m.ID]; ok {
			km.Latency = &l
		}
		beats, err := s.store.RecentBeats(m.ID, 30)
		if err != nil {
			return b, err
		}
		for _, hb := range beats {
			km.Beats = append(km.Beats, hb.Status)
		}
		switch km.Status {
		case "down":
			worse("down")
		case "pending", "degraded":
			worse("degraded")
		}
		b.Monitors = append(b.Monitors, km)
	}

	hosts, err := s.store.Hosts()
	if err != nil {
		return b, err
	}
	open, err := s.store.OpenHostEvents()
	if err != nil {
		return b, err
	}
	for _, h := range hosts {
		ks := kioskServer{Name: h.Name, Group: h.Group, Status: h.Status, Since: h.StatusSince, Open: []string{}}
		if m, live := s.fleet.Live(h.ID); m != nil {
			ks.Connected = live
			cpu, disk := m.CPU, m.DiskPct
			ks.CPU, ks.Disk = &cpu, &disk
			if m.MemTotal >= 1<<20 {
				mem := min(100, max(0, m.MemUsed/m.MemTotal*100))
				ks.Mem = &mem
			}
		}
		for _, e := range open {
			if e.HostID == h.ID && e.Kind != "offline" {
				ks.Open = append(ks.Open, e.Kind)
			}
		}
		switch {
		case h.Status == "offline":
			worse("down")
		case len(ks.Open) > 0:
			worse("degraded")
		}
		b.Servers = append(b.Servers, ks)
	}
	return b, nil
}
