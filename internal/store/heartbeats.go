package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"hook/internal/probe"
)

// Timings is the HTTP request waterfall (defined with the probe that measures it).
type Timings = probe.Timings

type Heartbeat struct {
	MonitorID int64    `json:"monitorId"`
	TS        int64    `json:"ts"`
	Status    string   `json:"status"`
	Latency   int      `json:"latency"`
	Code      int      `json:"code,omitempty"`
	Msg       string   `json:"msg,omitempty"`
	Timings   *Timings `json:"timings,omitempty"`
}

func (s *Store) InsertHeartbeat(h *Heartbeat) error {
	var timings any
	if h.Timings != nil {
		b, _ := json.Marshal(h.Timings)
		timings = string(b)
	}
	_, err := s.db.Exec(`INSERT INTO heartbeats(monitor_id, ts, status, latency, code, msg, timings) VALUES(?,?,?,?,?,?,?)`,
		h.MonitorID, h.TS, h.Status, h.Latency, h.Code, h.Msg, timings)
	return err
}

func scanBeats(rows *sql.Rows) ([]Heartbeat, error) {
	defer rows.Close()
	out := []Heartbeat{}
	for rows.Next() {
		var h Heartbeat
		var timings sql.NullString
		if err := rows.Scan(&h.MonitorID, &h.TS, &h.Status, &h.Latency, &h.Code, &h.Msg, &timings); err != nil {
			return nil, err
		}
		if timings.Valid {
			h.Timings = &Timings{}
			json.Unmarshal([]byte(timings.String), h.Timings)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RecentBeats returns the newest n beats, oldest first.
func (s *Store) RecentBeats(monitorID int64, n int) ([]Heartbeat, error) {
	rows, err := s.db.Query(`SELECT * FROM (SELECT monitor_id, ts, status, latency, code, msg, timings FROM heartbeats WHERE monitor_id = ? ORDER BY ts DESC LIMIT ?) ORDER BY ts`, monitorID, n)
	if err != nil {
		return nil, err
	}
	return scanBeats(rows)
}

// BeatsSince returns raw beats from sinceMs, oldest first.
func (s *Store) BeatsSince(monitorID, sinceMs int64) ([]Heartbeat, error) {
	rows, err := s.db.Query(`SELECT monitor_id, ts, status, latency, code, msg, timings FROM heartbeats WHERE monitor_id = ? AND ts >= ? ORDER BY ts`, monitorID, sinceMs)
	if err != nil {
		return nil, err
	}
	return scanBeats(rows)
}

// ---- hourly rollups ----

type Rollup struct {
	Hour     int64 `json:"ts"`
	Up       int   `json:"up"`
	Down     int   `json:"down"`
	Degraded int   `json:"degraded"`
	AvgLat   int   `json:"latency"`
	MaxLat   int   `json:"maxLatency"`
}

// RefreshRollups recomputes hourly buckets for every hour touching sinceMs onward.
func (s *Store) RefreshRollups(sinceMs int64) error {
	since := sinceMs / 3600000 * 3600000
	_, err := s.db.Exec(`
INSERT INTO rollup_1h(monitor_id, hour, up, down, degraded, lat_sum, lat_n, lat_max)
SELECT monitor_id, (ts / 3600000) * 3600000 AS h,
       sum(status IN ('up','degraded')),
       sum(status = 'down'),
       sum(status = 'degraded'),
       coalesce(sum(CASE WHEN status != 'down' THEN latency END), 0),
       sum(status != 'down'),
       coalesce(max(CASE WHEN status != 'down' THEN latency END), 0)
FROM heartbeats WHERE ts >= ? GROUP BY monitor_id, h
ON CONFLICT(monitor_id, hour) DO UPDATE SET
  up = excluded.up, down = excluded.down, degraded = excluded.degraded,
  lat_sum = excluded.lat_sum, lat_n = excluded.lat_n, lat_max = excluded.lat_max`, since)
	return err
}

func (s *Store) Rollups(monitorID, sinceMs int64) ([]Rollup, error) {
	rows, err := s.db.Query(`SELECT hour, up, down, degraded, CASE WHEN lat_n > 0 THEN lat_sum / lat_n ELSE 0 END, lat_max
		FROM rollup_1h WHERE monitor_id = ? AND hour >= ? ORDER BY hour`, monitorID, sinceMs/3600000*3600000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rollup{}
	for rows.Next() {
		var r Rollup
		if err := rows.Scan(&r.Hour, &r.Up, &r.Down, &r.Degraded, &r.AvgLat, &r.MaxLat); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Uptime returns up/(up+down) per monitor since sinceMs, at hour granularity.
// Monitors with no confirmed beats are absent from the map.
func (s *Store) Uptime(sinceMs int64) (map[int64]float64, error) {
	rows, err := s.db.Query(`SELECT monitor_id, sum(up), sum(down) FROM rollup_1h WHERE hour >= ? GROUP BY monitor_id`, sinceMs/3600000*3600000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var id, up, down int64
		if err := rows.Scan(&id, &up, &down); err != nil {
			return nil, err
		}
		if up+down > 0 {
			out[id] = float64(up) / float64(up+down)
		}
	}
	return out, rows.Err()
}

// AvgLatency returns the mean non-down latency per monitor since sinceMs.
func (s *Store) AvgLatency(sinceMs int64) (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT monitor_id, sum(lat_sum) / sum(lat_n) FROM rollup_1h WHERE hour >= ? GROUP BY monitor_id HAVING sum(lat_n) > 0`, sinceMs/3600000*3600000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var v int
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// Prune deletes raw beats and rollups older than the given cut-offs.
func (s *Store) Prune(rawBeforeMs, rollupBeforeMs int64) error {
	if _, err := s.db.Exec(`DELETE FROM heartbeats WHERE ts < ?`, rawBeforeMs); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM rollup_1h WHERE hour < ?`, rollupBeforeMs)
	return err
}

// ---- incidents ----

type Incident struct {
	ID         int64  `json:"id"`
	MonitorID  int64  `json:"monitorId"`
	StartedAt  int64  `json:"startedAt"`
	ResolvedAt *int64 `json:"resolvedAt"`
	Cause      string `json:"cause"`
}

// OpenIncident starts an incident unless one is already open.
func (s *Store) OpenIncident(monitorID, at int64, cause string) error {
	_, err := s.db.Exec(`INSERT INTO incidents(monitor_id, started_at, cause)
		SELECT ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM incidents WHERE monitor_id = ? AND resolved_at IS NULL)`, monitorID, at, cause, monitorID)
	return err
}

// ResolveIncident closes the open incident and returns when it started (0 if none).
func (s *Store) ResolveIncident(monitorID, at int64) (int64, error) {
	var started int64
	err := s.db.QueryRow(`UPDATE incidents SET resolved_at = ? WHERE monitor_id = ? AND resolved_at IS NULL RETURNING started_at`, at, monitorID).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return started, err
}

// CloseOpenIncidents resolves incidents of a monitor that was paused or edited.
func (s *Store) CloseOpenIncidents(monitorID int64) error {
	_, err := s.db.Exec(`UPDATE incidents SET resolved_at = ? WHERE monitor_id = ? AND resolved_at IS NULL`, nowMs(), monitorID)
	return err
}

func (s *Store) Incidents(monitorID int64, limit int) ([]Incident, error) {
	rows, err := s.db.Query(`SELECT id, monitor_id, started_at, resolved_at, cause FROM incidents WHERE monitor_id = ? ORDER BY started_at DESC LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var in Incident
		var res sql.NullInt64
		if err := rows.Scan(&in.ID, &in.MonitorID, &in.StartedAt, &res, &in.Cause); err != nil {
			return nil, err
		}
		if res.Valid {
			in.ResolvedAt = &res.Int64
		}
		out = append(out, in)
	}
	return out, rows.Err()
}
