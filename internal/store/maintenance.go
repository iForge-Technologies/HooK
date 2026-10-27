package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Maintenance is a planned window: monitors show "maintenance" and alerts for
// the covered monitors and servers are muted. Repeats are fixed 24 h / 7 day
// periods from the first occurrence.
type Maintenance struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	StartsAt   int64   `json:"startsAt"`
	EndsAt     int64   `json:"endsAt"`
	Repeat     string  `json:"repeat"` // "" | daily | weekly
	Everything bool    `json:"everything"`
	MonitorIDs []int64 `json:"monitorIds"`
	HostIDs    []int64 `json:"hostIds"`
}

func (m *Maintenance) period() time.Duration {
	switch m.Repeat {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	}
	return 0
}

// Occurrence returns the window occurrence that is active at t, or else the
// next one. ok is false when there's none left.
func (m *Maintenance) Occurrence(t time.Time) (start, end time.Time, ok bool) {
	s, e := time.UnixMilli(m.StartsAt), time.UnixMilli(m.EndsAt)
	p := m.period()
	if p == 0 || t.Before(s) {
		return s, e, t.Before(e)
	}
	d := e.Sub(s)
	k := t.Sub(s) / p
	occ := s.Add(k * p)
	if t.Before(occ.Add(d)) {
		return occ, occ.Add(d), true
	}
	occ = occ.Add(p)
	return occ, occ.Add(d), true
}

// ActiveAt reports whether the window covers t.
func (m *Maintenance) ActiveAt(t time.Time) bool {
	s, e, ok := m.Occurrence(t)
	return ok && !t.Before(s) && t.Before(e)
}

func (m *Maintenance) CoversMonitor(id int64) bool {
	return m.Everything || slices.Contains(m.MonitorIDs, id)
}

func (m *Maintenance) CoversHost(id int64) bool {
	return m.Everything || slices.Contains(m.HostIDs, id)
}

const maintCols = `id, title, starts_at, ends_at, repeat, everything, monitor_ids, host_ids`

func scanMaintenance(r scanner) (*Maintenance, error) {
	var m Maintenance
	var mids, hids string
	err := r.Scan(&m.ID, &m.Title, &m.StartsAt, &m.EndsAt, &m.Repeat, &m.Everything, &mids, &hids)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.MonitorIDs, m.HostIDs = []int64{}, []int64{}
	json.Unmarshal([]byte(mids), &m.MonitorIDs)
	json.Unmarshal([]byte(hids), &m.HostIDs)
	return &m, nil
}

func (s *Store) MaintenanceWindows() ([]*Maintenance, error) {
	rows, err := s.db.Query(`SELECT ` + maintCols + ` FROM maintenance ORDER BY starts_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Maintenance{}
	for rows.Next() {
		m, err := scanMaintenance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SaveMaintenance(m *Maintenance) error {
	mids, _ := json.Marshal(m.MonitorIDs)
	hids, _ := json.Marshal(m.HostIDs)
	if m.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO maintenance(title, starts_at, ends_at, repeat, everything, monitor_ids, host_ids, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			m.Title, m.StartsAt, m.EndsAt, m.Repeat, boolInt(m.Everything), string(mids), string(hids), nowMs())
		if err != nil {
			return err
		}
		m.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.Exec(`UPDATE maintenance SET title=?, starts_at=?, ends_at=?, repeat=?, everything=?, monitor_ids=?, host_ids=? WHERE id=?`,
		m.Title, m.StartsAt, m.EndsAt, m.Repeat, boolInt(m.Everything), string(mids), string(hids), m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMaintenance(id int64) error {
	res, err := s.db.Exec(`DELETE FROM maintenance WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- status pages ----

type StatusPage struct {
	ID          int64   `json:"id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	MonitorIDs  []int64 `json:"monitorIds"`
	ShowLatency bool    `json:"showLatency"`
}

const pageCols = `id, slug, title, description, monitor_ids, show_latency`

func scanPage(r scanner) (*StatusPage, error) {
	var p StatusPage
	var ids string
	err := r.Scan(&p.ID, &p.Slug, &p.Title, &p.Description, &ids, &p.ShowLatency)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.MonitorIDs = []int64{}
	json.Unmarshal([]byte(ids), &p.MonitorIDs)
	return &p, nil
}

func (s *Store) StatusPages() ([]*StatusPage, error) {
	rows, err := s.db.Query(`SELECT ` + pageCols + ` FROM status_pages ORDER BY title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*StatusPage{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) StatusPageBySlug(slug string) (*StatusPage, error) {
	return scanPage(s.db.QueryRow(`SELECT `+pageCols+` FROM status_pages WHERE slug = ?`, slug))
}

var ErrSlugTaken = errors.New("that address is already used by another status page")

func (s *Store) SaveStatusPage(p *StatusPage) error {
	ids, _ := json.Marshal(p.MonitorIDs)
	var err error
	var res sql.Result
	if p.ID == 0 {
		res, err = s.db.Exec(`INSERT INTO status_pages(slug, title, description, monitor_ids, show_latency, created_at) VALUES(?,?,?,?,?,?)`,
			p.Slug, p.Title, p.Description, string(ids), boolInt(p.ShowLatency), nowMs())
		if err == nil {
			p.ID, _ = res.LastInsertId()
		}
	} else {
		res, err = s.db.Exec(`UPDATE status_pages SET slug=?, title=?, description=?, monitor_ids=?, show_latency=? WHERE id=?`,
			p.Slug, p.Title, p.Description, string(ids), boolInt(p.ShowLatency), p.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}
	}
	if err != nil && isUnique(err) {
		return ErrSlugTaken
	}
	return err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Store) DeleteStatusPage(id int64) error {
	res, err := s.db.Exec(`DELETE FROM status_pages WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DayBucket is one UTC day of a monitor's checks.
type DayBucket struct {
	Day  int64 `json:"day"` // unix ms, UTC midnight
	Up   int   `json:"up"`
	Down int   `json:"down"`
	Lat  int   `json:"latency"`
}

// DailyUptime folds hourly rollups into UTC days for the given monitors.
func (s *Store) DailyUptime(ids []int64, fromMs int64) (map[int64][]DayBucket, error) {
	out := map[int64][]DayBucket{}
	if len(ids) == 0 {
		return out, nil
	}
	in, _ := json.Marshal(ids)
	rows, err := s.db.Query(`SELECT monitor_id, (hour / 86400000) * 86400000 AS d, sum(up), sum(down),
		CASE WHEN sum(lat_n) > 0 THEN sum(lat_sum) / sum(lat_n) ELSE 0 END
		FROM rollup_1h WHERE hour >= ? AND monitor_id IN (SELECT value FROM json_each(?))
		GROUP BY monitor_id, d ORDER BY d`, fromMs, string(in))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var b DayBucket
		if err := rows.Scan(&id, &b.Day, &b.Up, &b.Down, &b.Lat); err != nil {
			return nil, err
		}
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}
