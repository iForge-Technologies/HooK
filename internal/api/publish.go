package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"hook/internal/events"
	"hook/internal/store"
)

// ---- maintenance windows ----

func (s *Server) listMaintenance(w http.ResponseWriter, r *http.Request, se *session) {
	ws, err := s.store.MaintenanceWindows()
	if err != nil {
		s.internal(w, err)
		return
	}
	now := time.Now()
	type view struct {
		*store.Maintenance
		Active    bool  `json:"active"`
		NextStart int64 `json:"nextStart"` // current or next occurrence; 0 when finished
		NextEnd   int64 `json:"nextEnd"`
	}
	out := make([]view, len(ws))
	for i, m := range ws {
		v := view{Maintenance: m, Active: m.ActiveAt(now)}
		if st, en, ok := m.Occurrence(now); ok {
			v.NextStart, v.NextEnd = st.UnixMilli(), en.UnixMilli()
		}
		out[i] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveMaintenance(w http.ResponseWriter, r *http.Request, se *session) {
	var m store.Maintenance
	if err := readJSON(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID = 0
	if r.PathValue("id") != "" {
		id, valid := pathID(r)
		if !valid {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		m.ID = id
	}
	m.Title = strings.TrimSpace(m.Title)
	d := time.Duration(m.EndsAt-m.StartsAt) * time.Millisecond
	switch {
	case m.Title == "" || len(m.Title) > 100:
		writeErr(w, http.StatusBadRequest, "title is required (max 100 characters)")
		return
	case d < time.Minute:
		writeErr(w, http.StatusBadRequest, "the window must end at least a minute after it starts")
		return
	case m.Repeat != "" && m.Repeat != "daily" && m.Repeat != "weekly":
		writeErr(w, http.StatusBadRequest, "repeat must be daily, weekly or empty")
		return
	case m.Repeat == "daily" && d >= 24*time.Hour, m.Repeat == "weekly" && d >= 7*24*time.Hour:
		writeErr(w, http.StatusBadRequest, "a repeating window must be shorter than its repeat period")
		return
	case m.Repeat == "" && d > 31*24*time.Hour:
		writeErr(w, http.StatusBadRequest, "a one-off window can last at most 31 days")
		return
	}
	if m.MonitorIDs == nil {
		m.MonitorIDs = []int64{}
	}
	if m.HostIDs == nil {
		m.HostIDs = []int64{}
	}
	if !m.Everything && len(m.MonitorIDs) == 0 && len(m.HostIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "choose what the window covers")
		return
	}
	if err := s.store.SaveMaintenance(&m); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "window not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.cal.Reload()
	s.forgetPublic()
	s.events.Publish(events.Event{Type: "monitors"})
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteMaintenance(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteMaintenance(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "window not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.cal.Reload()
	s.forgetPublic()
	ok(w)
}

// ---- status pages (admin) ----

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

func (s *Server) listStatusPages(w http.ResponseWriter, r *http.Request, se *session) {
	ps, err := s.store.StatusPages()
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) saveStatusPage(w http.ResponseWriter, r *http.Request, se *session) {
	var p store.StatusPage
	if err := readJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p.ID = 0
	if r.PathValue("id") != "" {
		id, valid := pathID(r)
		if !valid {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		p.ID = id
	}
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	p.Title, p.Description = strings.TrimSpace(p.Title), strings.TrimSpace(p.Description)
	switch {
	case !slugRe.MatchString(p.Slug):
		writeErr(w, http.StatusBadRequest, "the address must be 2–40 lowercase letters, digits or dashes")
		return
	case p.Title == "" || len(p.Title) > 80:
		writeErr(w, http.StatusBadRequest, "title is required (max 80 characters)")
		return
	case len(p.Description) > 500:
		writeErr(w, http.StatusBadRequest, "description is too long (max 500 characters)")
		return
	}
	ms, err := s.store.Monitors()
	if err != nil {
		s.internal(w, err)
		return
	}
	var ids []int64
	for _, id := range p.MonitorIDs {
		if slices.ContainsFunc(ms, func(m *store.Monitor) bool { return m.ID == id }) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, "choose at least one monitor to show")
		return
	}
	p.MonitorIDs = ids
	if err := s.store.SaveStatusPage(&p); errors.Is(err, store.ErrSlugTaken) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	} else if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.forgetPublic()
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteStatusPage(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteStatusPage(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	s.forgetPublic()
	ok(w)
}

// ---- public status page ----

var publicCache = struct {
	sync.Mutex
	m map[string]cachedPage
}{m: map[string]cachedPage{}}

type cachedPage struct {
	at   time.Time
	body []byte
}

func (s *Server) forgetPublic() {
	publicCache.Lock()
	clear(publicCache.m)
	publicCache.Unlock()
}

type publicDay struct {
	Day    int64    `json:"day"`
	Uptime *float64 `json:"uptime"` // nil: no data that day
}

type publicMonitor struct {
	Name     string      `json:"name"`
	Group    string      `json:"group"`
	Status   string      `json:"status"` // up | degraded | down | maintenance | paused
	Uptime90 *float64    `json:"uptime90"`
	Days     []publicDay `json:"days"` // 90 UTC days, oldest first
	Latency  *int        `json:"latency,omitempty"`
}

type publicIncident struct {
	Name       string `json:"name"`
	StartedAt  int64  `json:"startedAt"`
	ResolvedAt *int64 `json:"resolvedAt"`
}

type publicWindow struct {
	Title    string `json:"title"`
	StartsAt int64  `json:"startsAt"`
	EndsAt   int64  `json:"endsAt"`
	Active   bool   `json:"active"`
}

// publicStatus serves a status page without authentication. It exposes only
// display data: monitor names, groups, states and history. Never targets,
// IDs, error messages or anything else internal.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	publicCache.Lock()
	c, hit := publicCache.m[slug]
	publicCache.Unlock()
	if hit && time.Since(c.at) < 30*time.Second {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Write(c.body)
		return
	}
	page, err := s.store.StatusPageBySlug(slug)
	if err != nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	all, err := s.store.Monitors()
	if err != nil {
		s.internal(w, err)
		return
	}
	byID := map[int64]*store.Monitor{}
	for _, m := range all {
		byID[m.ID] = m
	}
	now := time.Now()
	today := now.UnixMilli() / 86_400_000 * 86_400_000
	from := today - 89*86_400_000
	daily, err := s.store.DailyUptime(page.MonitorIDs, from)
	if err != nil {
		s.internal(w, err)
		return
	}
	lat, _ := s.store.AvgLatency(now.Add(-24 * time.Hour).UnixMilli())

	out := struct {
		Title       string           `json:"title"`
		Description string           `json:"description"`
		Overall     string           `json:"overall"`
		UpdatedAt   int64            `json:"updatedAt"`
		Monitors    []publicMonitor  `json:"monitors"`
		Incidents   []publicIncident `json:"incidents"`
		Maintenance []publicWindow   `json:"maintenance"`
	}{Title: page.Title, Description: page.Description, Overall: "up", UpdatedAt: now.UnixMilli(),
		Monitors: []publicMonitor{}, Incidents: []publicIncident{}, Maintenance: []publicWindow{}}

	rank := map[string]int{"up": 0, "maintenance": 1, "degraded": 2, "down": 3}
	for _, id := range page.MonitorIDs {
		m := byID[id]
		if m == nil {
			continue
		}
		pm := publicMonitor{Name: m.Name, Group: m.Group, Status: m.Status}
		switch {
		case !m.Active:
			pm.Status = "paused"
		case m.Status == "pending":
			pm.Status = "degraded" // a failure being confirmed
		}
		if rank[pm.Status] > rank[out.Overall] {
			out.Overall = pm.Status
		}
		days := map[int64]store.DayBucket{}
		var up, down int
		for _, d := range daily[id] {
			days[d.Day] = d
			up, down = up+d.Up, down+d.Down
		}
		for d := from; d <= today; d += 86_400_000 {
			pd := publicDay{Day: d}
			if b, ok := days[d]; ok && b.Up+b.Down > 0 {
				v := float64(b.Up) / float64(b.Up+b.Down)
				pd.Uptime = &v
			}
			pm.Days = append(pm.Days, pd)
		}
		if up+down > 0 {
			v := float64(up) / float64(up+down)
			pm.Uptime90 = &v
		}
		if page.ShowLatency {
			if l, ok := lat[id]; ok {
				pm.Latency = &l
			}
		}
		out.Monitors = append(out.Monitors, pm)

		incs, _ := s.store.Incidents(id, 20)
		for _, in := range incs {
			if in.StartedAt >= now.AddDate(0, 0, -14).UnixMilli() || in.ResolvedAt == nil {
				out.Incidents = append(out.Incidents, publicIncident{Name: m.Name, StartedAt: in.StartedAt, ResolvedAt: in.ResolvedAt})
			}
		}
	}
	slices.SortFunc(out.Incidents, func(a, b publicIncident) int { return int((b.StartedAt - a.StartedAt) / 1000) })

	ws, _ := s.store.MaintenanceWindows()
	for _, wd := range ws {
		if !wd.Everything && !slices.ContainsFunc(page.MonitorIDs, wd.CoversMonitor) {
			continue
		}
		if st, en, ok := wd.Occurrence(now); ok && st.Before(now.AddDate(0, 0, 14)) {
			out.Maintenance = append(out.Maintenance, publicWindow{Title: wd.Title, StartsAt: st.UnixMilli(), EndsAt: en.UnixMilli(), Active: wd.ActiveAt(now)})
		}
	}

	body, _ := json.Marshal(out)
	publicCache.Lock()
	publicCache.m[slug] = cachedPage{at: now, body: body}
	publicCache.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Write(body)
}
