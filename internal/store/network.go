package store

import (
	"time"

	"hook/internal/proto"
)

type TrafficDay struct {
	Day int64   `json:"day"` // UTC midnight, unix ms
	Rx  float64 `json:"rx"`  // bytes
	Tx  float64 `json:"tx"`
}

// Traffic returns daily totals from sinceMs (inclusive), oldest first.
func (s *Store) Traffic(hostID, sinceMs int64) ([]TrafficDay, error) {
	rows, err := s.db.Query(`SELECT day, rx, tx FROM host_traffic WHERE host_id = ? AND day >= ? ORDER BY day`, hostID, sinceMs/86_400_000*86_400_000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficDay{}
	for rows.Next() {
		var d TrafficDay
		if err := rows.Scan(&d.Day, &d.Rx, &d.Tx); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CycleStart is the start (UTC midnight) of the billing cycle containing t,
// for a provider whose counter resets on resetDay of each month.
func CycleStart(t time.Time, resetDay int) time.Time {
	resetDay = max(1, min(28, resetDay))
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), resetDay, 0, 0, 0, 0, time.UTC)
	if start.After(t) {
		start = start.AddDate(0, -1, 0)
	}
	return start
}

type SpeedTest struct {
	ID        int64   `json:"id"`
	TS        int64   `json:"ts"`
	DownBps   float64 `json:"downBps"`
	UpBps     float64 `json:"upBps"`
	LatencyMs float64 `json:"latencyMs"`
	JitterMs  float64 `json:"jitterMs"`
	Server    string  `json:"server"`
	Bytes     int64   `json:"bytes"`
	Error     string  `json:"error,omitempty"`
	Scheduled bool    `json:"scheduled"`
}

func (s *Store) SaveSpeedTest(hostID, ts int64, r proto.SpeedResult, scheduled bool) (*SpeedTest, error) {
	t := &SpeedTest{TS: ts, DownBps: r.DownBps, UpBps: r.UpBps, LatencyMs: r.LatencyMs, JitterMs: r.JitterMs,
		Server: r.Server, Bytes: r.Bytes, Error: r.Error, Scheduled: scheduled}
	res, err := s.db.Exec(`INSERT INTO speed_tests(host_id, ts, down_bps, up_bps, latency_ms, jitter_ms, server, bytes, error, scheduled)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, hostID, ts, t.DownBps, t.UpBps, t.LatencyMs, t.JitterMs, t.Server, t.Bytes, t.Error, scheduled)
	if err != nil {
		return nil, err
	}
	t.ID, _ = res.LastInsertId()
	return t, nil
}

// SpeedTests returns a host's newest tests first.
func (s *Store) SpeedTests(hostID int64, limit int) ([]SpeedTest, error) {
	rows, err := s.db.Query(`SELECT id, ts, down_bps, up_bps, latency_ms, jitter_ms, server, bytes, error, scheduled
		FROM speed_tests WHERE host_id = ? ORDER BY ts DESC LIMIT ?`, hostID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpeedTest{}
	for rows.Next() {
		var t SpeedTest
		if err := rows.Scan(&t.ID, &t.TS, &t.DownBps, &t.UpBps, &t.LatencyMs, &t.JitterMs, &t.Server, &t.Bytes, &t.Error, &t.Scheduled); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LastSpeedTests maps each host to the time of its newest test.
func (s *Store) LastSpeedTests() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT host_id, max(ts) FROM speed_tests GROUP BY host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, err
		}
		out[id] = ts
	}
	return out, rows.Err()
}
