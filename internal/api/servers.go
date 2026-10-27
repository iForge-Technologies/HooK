package api

import (
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"hook/internal/collect"
	"hook/internal/events"
	"hook/internal/forecast"
	"hook/internal/proto"
	"hook/internal/store"
)

//go:embed install.sh
var installScript []byte

// Agent binaries served to install.sh; built by `make linux` into HOOK_AGENT_DIR.
var agentFiles = []string{"hook-agent-linux-amd64", "hook-agent-linux-arm64"}

func newAgentToken() string { return "hk_" + newToken() }

// baseURL is where agents should reach the hub: HOOK_PUBLIC_URL when set,
// otherwise the address this request came in on.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil || (s.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) installCommand(r *http.Request, token string) string {
	base := s.baseURL(r)
	return "curl -fsSL " + base + "/install.sh | sudo sh -s -- --url " + base + " --token " + token
}

type serverView struct {
	*store.Host
	Metrics   *proto.Metrics `json:"metrics"`
	Connected bool           `json:"connected"`
	Open      []string       `json:"openAlerts"` // kinds of unresolved events
	Spark     []float64      `json:"cpuSpark"`   // last hour, per minute
	DiskDays  *float64       `json:"diskDaysToFull,omitempty"`
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request, se *session) {
	hosts, err := s.store.Hosts()
	if err != nil {
		s.internal(w, err)
		return
	}
	open, err := s.store.OpenHostEvents()
	if err != nil {
		s.internal(w, err)
		return
	}
	since := time.Now().Add(-time.Hour).UnixMilli()
	out := make([]serverView, 0, len(hosts))
	for _, h := range hosts {
		v := serverView{Host: h, Open: []string{}, Spark: []float64{}}
		v.Metrics, v.Connected = s.fleet.Live(h.ID)
		for _, e := range open {
			if e.HostID == h.ID {
				v.Open = append(v.Open, e.Kind)
			}
		}
		pts, err := s.store.HostSeries(h.ID, since, "1m")
		if err != nil {
			s.internal(w, err)
			return
		}
		for _, p := range pts {
			v.Spark = append(v.Spark, p.CPU)
		}
		if week, err := s.store.HostSeries(h.ID, time.Now().Add(-7*24*time.Hour).UnixMilli(), "1h"); err == nil {
			if d := forecast.DiskFull(week); d.Confident {
				v.DiskDays = d.DaysToFull
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request, se *session) {
	h, found := s.loadHost(w, r)
	if !found {
		return
	}
	v := serverView{Host: h, Open: []string{}}
	v.Metrics, v.Connected = s.fleet.Live(h.ID)
	snap, err := s.store.Snapshot(h.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internal(w, err)
		return
	}
	evs, err := s.store.HostEvents(h.ID, 25)
	if err != nil {
		s.internal(w, err)
		return
	}
	for _, e := range evs {
		if e.ResolvedAt == nil {
			v.Open = append(v.Open, e.Kind)
		}
	}
	now := time.Now()
	week, _ := s.store.HostSeries(h.ID, now.Add(-7*24*time.Hour).UnixMilli(), "1h")
	month, _ := s.store.HostSeries(h.ID, now.Add(-30*24*time.Hour).UnixMilli(), "1h")
	writeJSON(w, http.StatusOK, map[string]any{"server": v, "snapshot": snap, "events": evs,
		"forecast": map[string]any{"disk": forecast.DiskFull(week), "sizing": forecast.RightSize(month)}})
}

func (s *Server) loadHost(w http.ResponseWriter, r *http.Request) (*store.Host, bool) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, false
	}
	h, err := s.store.Host(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "server not found")
		return nil, false
	}
	if err != nil {
		s.internal(w, err)
		return nil, false
	}
	return h, true
}

type serverInput struct {
	Name       string            `json:"name"`
	Group      string            `json:"group"`
	Thresholds *store.Thresholds `json:"thresholds"`
	ChannelIDs []int64           `json:"channelIds"`
}

func (in *serverInput) validate() error {
	in.Name, in.Group = strings.TrimSpace(in.Name), strings.TrimSpace(in.Group)
	if in.Name == "" || len(in.Name) > 60 {
		return errors.New("name is required (max 60 characters)")
	}
	if len(in.Group) > 50 {
		return errors.New("group is too long (max 50 characters)")
	}
	if t := in.Thresholds; t != nil {
		for _, v := range []float64{t.CPU, t.Mem, t.Disk} {
			if v < 0 || v > 100 {
				return errors.New("thresholds are percentages between 0 (off) and 100")
			}
		}
		t.Minutes = max(1, min(120, t.Minutes))
		units := []string{}
		for _, u := range t.Units {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if !collect.UnitName.MatchString(u) {
				return fmt.Errorf("%q isn't a valid systemd unit name", u)
			}
			if !strings.Contains(u, ".") {
				u += ".service"
			}
			if !slices.Contains(units, u) {
				units = append(units, u)
			}
		}
		if len(units) > 30 {
			return errors.New("watch at most 30 units per server")
		}
		t.Units = units
	}
	if in.ChannelIDs == nil {
		in.ChannelIDs = []int64{}
	}
	return nil
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request, se *session) {
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h := &store.Host{Name: in.Name, Group: in.Group, Thresholds: store.DefaultThresholds(), ChannelIDs: in.ChannelIDs}
	if in.Thresholds != nil {
		h.Thresholds = *in.Thresholds
	}
	token := newAgentToken()
	if err := s.store.CreateHost(h, token); err != nil {
		s.internal(w, err)
		return
	}
	s.fleet.SetHost(h)
	s.events.Publish(events.Event{Type: "hosts"})
	writeJSON(w, http.StatusCreated, map[string]any{"id": h.ID, "token": token, "install": s.installCommand(r, token)})
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request, se *session) {
	h, found := s.loadHost(w, r)
	if !found {
		return
	}
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.Name, h.Group, h.ChannelIDs = in.Name, in.Group, in.ChannelIDs
	if in.Thresholds != nil {
		h.Thresholds = *in.Thresholds
	}
	if err := s.store.UpdateHost(h); err != nil {
		s.internal(w, err)
		return
	}
	s.fleet.SetHost(h)
	s.events.Publish(events.Event{Type: "hosts"})
	ok(w)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteHost(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "server not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.fleet.Remove(id)
	s.events.Publish(events.Event{Type: "hosts"})
	ok(w)
}

// rotateToken issues a new agent token; the old one stops working at once.
func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request, se *session) {
	h, found := s.loadHost(w, r)
	if !found {
		return
	}
	token := newAgentToken()
	if err := s.store.SetHostToken(h.ID, token); err != nil {
		s.internal(w, err)
		return
	}
	s.fleet.Disconnect(h.ID, "token revoked")
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "install": s.installCommand(r, token)})
}

// serverSeries picks a resolution that keeps a chart around 360–1440 points.
func (s *Server) serverSeries(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ranges := map[string]struct {
		d   time.Duration
		res string
	}{
		"1h":  {time.Hour, "raw"},
		"6h":  {6 * time.Hour, "1m"},
		"24h": {24 * time.Hour, "1m"},
		"7d":  {7 * 24 * time.Hour, "1h"},
		"30d": {30 * 24 * time.Hour, "1h"},
	}
	rng := r.URL.Query().Get("range")
	sel, known := ranges[rng]
	if !known {
		rng, sel = "1h", ranges["1h"]
	}
	pts, err := s.store.HostSeries(id, time.Now().Add(-sel.d).UnixMilli(), sel.res)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rng, "resolution": sel.res, "points": pts})
}

func (s *Server) agentWS(w http.ResponseWriter, r *http.Request) {
	s.fleet.HandleWS(w, r, s.clientIP(r))
}

func (s *Server) serveInstall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(installScript)
}

func (s *Server) serveAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !slices.Contains(agentFiles, name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.cfg.AgentDir, name)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "agent binary not found on the hub: run `make linux` and copy bin/agents/* to "+s.cfg.AgentDir, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, path)
}
