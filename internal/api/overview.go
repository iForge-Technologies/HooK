package api

import (
	"cmp"
	"net/http"
	"slices"
	"time"
)

type memProc struct {
	HostID int64  `json:"hostId"`
	Host   string `json:"host"`
	Name   string `json:"name"`
	RSS    uint64 `json:"rss"`
}

// overview serves the history behind the bento home page. Live counts come
// from the monitors and servers lists, which the event stream keeps current.
func (s *Server) overview(w http.ResponseWriter, r *http.Request, se *session) {
	ranges := map[string]struct {
		d      time.Duration
		bucket time.Duration // ~24–60 points per chart
	}{
		"24h": {24 * time.Hour, time.Hour},
		"7d":  {7 * 24 * time.Hour, 3 * time.Hour},
		"30d": {30 * 24 * time.Hour, 12 * time.Hour},
	}
	rng := r.URL.Query().Get("range")
	sel, known := ranges[rng]
	if !known {
		rng, sel = "24h", ranges["24h"]
	}
	now := time.Now()
	from := now.Add(-sel.d)
	bucket := sel.bucket.Milliseconds()

	up, down, err := s.store.FleetUptime(from.UnixMilli(), now.Add(time.Hour).UnixMilli())
	if err != nil {
		s.internal(w, err)
		return
	}
	prevUp, prevDown, err := s.store.FleetUptime(from.Add(-sel.d).UnixMilli(), from.UnixMilli())
	if err != nil {
		s.internal(w, err)
		return
	}
	latency, err := s.store.FleetLatency(from.UnixMilli(), bucket)
	if err != nil {
		s.internal(w, err)
		return
	}
	feed, err := s.store.Feed(from.UnixMilli())
	if err != nil {
		s.internal(w, err)
		return
	}
	// Minute rollups only cover 14 days; past 24h the hourly ones are plenty.
	network, err := s.store.FleetNetwork(from.UnixMilli(), bucket/2, rng == "24h")
	if err != nil {
		s.internal(w, err)
		return
	}

	hosts, err := s.store.Hosts()
	if err != nil {
		s.internal(w, err)
		return
	}
	snaps, err := s.store.Snapshots()
	if err != nil {
		s.internal(w, err)
		return
	}
	procs := []memProc{}
	for _, h := range hosts {
		if snap := snaps[h.ID]; snap != nil && h.Status == "online" {
			for _, p := range snap.TopMem {
				procs = append(procs, memProc{HostID: h.ID, Host: h.Name, Name: p.Name, RSS: p.RSS})
			}
		}
	}
	slices.SortFunc(procs, func(a, b memProc) int { return cmp.Compare(b.RSS, a.RSS) })
	procs = procs[:min(len(procs), 40)]

	ratio := func(u, d int64) *float64 {
		if u+d == 0 {
			return nil
		}
		v := float64(u) / float64(u+d)
		return &v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":      rng,
		"from":       from.UnixMilli(),
		"to":         now.UnixMilli(),
		"bucket":     bucket,
		"uptime":     ratio(up, down),
		"uptimePrev": ratio(prevUp, prevDown),
		"checks":     map[string]int64{"up": up, "down": down},
		"latency":    latency,
		"feed":       feed,
		"network":    network,
		"memProcs":   procs,
	})
}
