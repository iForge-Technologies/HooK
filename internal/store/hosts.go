package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"hook/internal/proto"
)

// Thresholds raise an alert when a value stays at or above the limit for
// Minutes. A zero limit turns that check off.
type Thresholds struct {
	CPU        float64  `json:"cpu"`
	Mem        float64  `json:"mem"`
	Disk       float64  `json:"disk"`
	Minutes    int      `json:"minutes"`
	Offline    bool     `json:"offline"`    // alert when the agent stops reporting
	Containers bool     `json:"containers"` // alert when a running container stops, restarts or turns unhealthy
	Services   bool     `json:"services"`   // alert when a watched systemd unit isn't active
	Units      []string `json:"units"`      // systemd units to watch, e.g. nginx.service
}

func DefaultThresholds() Thresholds {
	return Thresholds{CPU: 90, Mem: 90, Disk: 85, Minutes: 5, Offline: true, Containers: true, Services: true, Units: []string{}}
}

type Host struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Group       string         `json:"group"`
	Info        proto.HostInfo `json:"info"`
	Thresholds  Thresholds     `json:"thresholds"`
	ChannelIDs  []int64        `json:"channelIds"`
	Status      string         `json:"status"` // new | online | offline
	StatusSince int64          `json:"statusSince"`
	LastSeen    int64          `json:"lastSeen"`
	RemoteIP    string         `json:"remoteIp"`
	CreatedAt   int64          `json:"createdAt"`
}

const hostCols = `id, name, grp, info, thresholds, status, status_since, last_seen, remote_ip, created_at`

func scanHost(r scanner) (*Host, error) {
	var h Host
	var info, th string
	err := r.Scan(&h.ID, &h.Name, &h.Group, &info, &th, &h.Status, &h.StatusSince, &h.LastSeen, &h.RemoteIP, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(info), &h.Info)
	h.Thresholds = DefaultThresholds()
	json.Unmarshal([]byte(th), &h.Thresholds)
	h.ChannelIDs = []int64{}
	return &h, nil
}

func (s *Store) hostChannelIDs() (map[int64][]int64, error) {
	rows, err := s.db.Query(`SELECT host_id, channel_id FROM host_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var h, c int64
		if err := rows.Scan(&h, &c); err != nil {
			return nil, err
		}
		out[h] = append(out[h], c)
	}
	return out, rows.Err()
}

func (s *Store) Hosts() ([]*Host, error) {
	rows, err := s.db.Query(`SELECT ` + hostCols + ` FROM hosts ORDER BY grp COLLATE NOCASE, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids, err := s.hostChannelIDs()
	if err != nil {
		return nil, err
	}
	for _, h := range out {
		if v := ids[h.ID]; v != nil {
			h.ChannelIDs = v
		}
	}
	return out, nil
}

func (s *Store) Host(id int64) (*Host, error) {
	h, err := scanHost(s.db.QueryRow(`SELECT `+hostCols+` FROM hosts WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	ids, err := s.hostChannelIDs()
	if err != nil {
		return nil, err
	}
	if v := ids[id]; v != nil {
		h.ChannelIDs = v
	}
	return h, nil
}

func (s *Store) HostByToken(token string) (*Host, error) {
	return scanHost(s.db.QueryRow(`SELECT `+hostCols+` FROM hosts WHERE token_hash = ?`, hashToken(token)))
}

func (s *Store) CreateHost(h *Host, token string) error {
	th, _ := json.Marshal(h.Thresholds)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowMs()
	res, err := tx.Exec(`INSERT INTO hosts(name, grp, token_hash, thresholds, status, status_since, created_at) VALUES(?,?,?,?, 'new', ?, ?)`,
		h.Name, h.Group, hashToken(token), string(th), now, now)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	h.Status, h.StatusSince, h.CreatedAt = "new", now, now
	if err := setHostChannels(tx, h.ID, h.ChannelIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateHost(h *Host) error {
	th, _ := json.Marshal(h.Thresholds)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE hosts SET name = ?, grp = ?, thresholds = ? WHERE id = ?`, h.Name, h.Group, string(th), h.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := setHostChannels(tx, h.ID, h.ChannelIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func setHostChannels(tx *sql.Tx, hostID int64, ids []int64) error {
	if _, err := tx.Exec(`DELETE FROM host_channels WHERE host_id = ?`, hostID); err != nil {
		return err
	}
	for _, c := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO host_channels(host_id, channel_id) SELECT ?, id FROM channels WHERE id = ?`, hostID, c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetHostToken(id int64, token string) error {
	res, err := s.db.Exec(`UPDATE hosts SET token_hash = ? WHERE id = ?`, hashToken(token), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteHost(id int64) error {
	res, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveHostInfo(id int64, info proto.HostInfo, remoteIP string) error {
	b, _ := json.Marshal(info)
	_, err := s.db.Exec(`UPDATE hosts SET info = ?, remote_ip = ? WHERE id = ?`, string(b), remoteIP, id)
	return err
}

func (s *Store) SetHostStatus(id int64, status string, since, lastSeen int64) error {
	_, err := s.db.Exec(`UPDATE hosts SET status = ?, status_since = ?, last_seen = max(last_seen, ?) WHERE id = ?`, status, since, lastSeen, id)
	return err
}

func (s *Store) TouchHost(id, lastSeen int64) error {
	_, err := s.db.Exec(`UPDATE hosts SET last_seen = max(last_seen, ?) WHERE id = ?`, lastSeen, id)
	return err
}

func (s *Store) HostChannels(hostID int64) ([]*Channel, error) {
	return s.queryChannels(`SELECT c.id, c.name, c.type, c.config, c.is_default, c.active FROM channels c
		JOIN host_channels hc ON hc.channel_id = c.id WHERE hc.host_id = ? AND c.active = 1`, hostID)
}

// ApplyChannelToAllHosts links a channel to every server (used with monitors' "apply to all").
func (s *Store) ApplyChannelToAllHosts(channelID int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO host_channels(host_id, channel_id) SELECT id, ? FROM hosts`, channelID)
	return err
}

// ---- metrics ----

// metricCols matches proto.Metrics field order in metricVals.
var metricCols = []string{"cpu", "iowait", "steal", "load1", "load5", "load15", "mem_used", "mem_total", "swap_used", "swap_total",
	"disk_read", "disk_write", "net_rx", "net_tx", "tcp", "procs", "disk_pct"}

// maxCols also keep their peak in rollups.
var maxCols = []string{"cpu", "mem_used", "load1", "disk_read", "disk_write", "net_rx", "net_tx"}

func metricVals(m *proto.Metrics) []any {
	return []any{m.CPU, m.IOWait, m.Steal, m.Load1, m.Load5, m.Load15, m.MemUsed, m.MemTotal, m.SwapUsed, m.SwapTotal,
		m.DiskRead, m.DiskWrite, m.NetRx, m.NetTx, m.TCP, m.Procs, m.DiskPct}
}

func metricPtrs(m *proto.Metrics) []any {
	return []any{&m.CPU, &m.IOWait, &m.Steal, &m.Load1, &m.Load5, &m.Load15, &m.MemUsed, &m.MemTotal, &m.SwapUsed, &m.SwapTotal,
		&m.DiskRead, &m.DiskWrite, &m.NetRx, &m.NetTx, &m.TCP, &m.Procs, &m.DiskPct}
}

var insertMetricSQL = `INSERT OR IGNORE INTO host_metrics(host_id, ts, ` + strings.Join(metricCols, ", ") + `) VALUES(?, ?` + strings.Repeat(", ?", len(metricCols)) + `)`

func (s *Store) InsertMetrics(hostID int64, m *proto.Metrics) error {
	_, err := s.db.Exec(insertMetricSQL, append([]any{hostID, m.TS}, metricVals(m)...)...)
	return err
}

// rollupSQL aggregates `from` into `to` buckets of `bucket` ms. Raw rows
// average directly; rollup rows are weighted by their sample counts.
func rollupSQL(from, to string, bucket int64, fromRollup bool) string {
	var sel, upd []string
	for _, c := range metricCols {
		if fromRollup {
			sel = append(sel, "sum("+c+" * n) / sum(n)")
		} else {
			sel = append(sel, "avg("+c+")")
		}
		upd = append(upd, c+" = excluded."+c)
	}
	var mx []string
	for _, c := range maxCols {
		if fromRollup {
			mx = append(mx, "max("+c+"_max)")
		} else {
			mx = append(mx, "max("+c+")")
		}
		upd = append(upd, c+"_max = excluded."+c+"_max")
	}
	count := "count(*)"
	if fromRollup {
		count = "sum(n)"
	}
	b := strconv.FormatInt(bucket, 10)
	return `INSERT INTO ` + to + `(host_id, ts, n, ` + strings.Join(metricCols, ", ") + `, ` + strings.Join(suffix(maxCols, "_max"), ", ") + `)
SELECT host_id, (ts / ` + b + `) * ` + b + ` AS bucket, ` + count + `, ` + strings.Join(sel, ", ") + `, ` + strings.Join(mx, ", ") + `
FROM ` + from + ` WHERE ts >= ? GROUP BY host_id, bucket
ON CONFLICT(host_id, ts) DO UPDATE SET n = excluded.n, ` + strings.Join(upd, ", ")
}

func suffix(xs []string, s string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x + s
	}
	return out
}

var (
	rollup1mSQL = rollupSQL("host_metrics", "host_rollup_1m", 60_000, false)
	rollup1hSQL = rollupSQL("host_rollup_1m", "host_rollup_1h", 3_600_000, true)
)

// RefreshHostRollups recomputes minute buckets from sinceMs and hour buckets
// touching sinceMs.
func (s *Store) RefreshHostRollups(sinceMs int64) error {
	if _, err := s.db.Exec(rollup1mSQL, sinceMs/60_000*60_000); err != nil {
		return err
	}
	_, err := s.db.Exec(rollup1hSQL, sinceMs/3_600_000*3_600_000)
	return err
}

// MetricPoint is one point of a server chart. Peaks are only set for rollups.
type MetricPoint struct {
	proto.Metrics
	CPUMax       *float64 `json:"cpuMax,omitempty"`
	MemUsedMax   *float64 `json:"memUsedMax,omitempty"`
	Load1Max     *float64 `json:"load1Max,omitempty"`
	DiskReadMax  *float64 `json:"diskReadMax,omitempty"`
	DiskWriteMax *float64 `json:"diskWriteMax,omitempty"`
	NetRxMax     *float64 `json:"netRxMax,omitempty"`
	NetTxMax     *float64 `json:"netTxMax,omitempty"`
}

// HostSeries reads points since sinceMs from "raw", "1m" or "1h".
func (s *Store) HostSeries(hostID, sinceMs int64, resolution string) ([]MetricPoint, error) {
	table, rollup := "host_metrics", false
	switch resolution {
	case "1m":
		table, rollup = "host_rollup_1m", true
	case "1h":
		table, rollup = "host_rollup_1h", true
	}
	cols := "ts, " + strings.Join(metricCols, ", ")
	if rollup {
		cols += ", " + strings.Join(suffix(maxCols, "_max"), ", ")
	}
	rows, err := s.db.Query(`SELECT `+cols+` FROM `+table+` WHERE host_id = ? AND ts >= ? ORDER BY ts`, hostID, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		dest := append([]any{&p.TS}, metricPtrs(&p.Metrics)...)
		if rollup {
			p.CPUMax, p.MemUsedMax, p.Load1Max, p.DiskReadMax, p.DiskWriteMax, p.NetRxMax, p.NetTxMax =
				new(float64), new(float64), new(float64), new(float64), new(float64), new(float64), new(float64)
			dest = append(dest, p.CPUMax, p.MemUsedMax, p.Load1Max, p.DiskReadMax, p.DiskWriteMax, p.NetRxMax, p.NetTxMax)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LatestMetrics returns each host's newest raw frame (for the list after a restart).
func (s *Store) LatestMetrics() (map[int64]proto.Metrics, error) {
	rows, err := s.db.Query(`SELECT host_id, ts, ` + strings.Join(metricCols, ", ") + ` FROM host_metrics m
		WHERE ts = (SELECT max(ts) FROM host_metrics WHERE host_id = m.host_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]proto.Metrics{}
	for rows.Next() {
		var id int64
		var m proto.Metrics
		if err := rows.Scan(append([]any{&id, &m.TS}, metricPtrs(&m)...)...); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}

func (s *Store) PruneHosts(rawBefore, minuteBefore, hourBefore int64) error {
	for _, q := range []struct {
		sql string
		ts  int64
	}{
		{`DELETE FROM host_metrics WHERE ts < ?`, rawBefore},
		{`DELETE FROM host_rollup_1m WHERE ts < ?`, minuteBefore},
		{`DELETE FROM host_rollup_1h WHERE ts < ?`, hourBefore},
	} {
		if _, err := s.db.Exec(q.sql, q.ts); err != nil {
			return err
		}
	}
	return nil
}

// ---- snapshots ----

func (s *Store) SaveSnapshot(hostID int64, snap *proto.Snapshot) error {
	b, _ := json.Marshal(snap)
	_, err := s.db.Exec(`INSERT INTO host_snapshots(host_id, ts, data) VALUES(?,?,?)
		ON CONFLICT(host_id) DO UPDATE SET ts = excluded.ts, data = excluded.data`, hostID, snap.TS, string(b))
	return err
}

func (s *Store) Snapshot(hostID int64) (*proto.Snapshot, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM host_snapshots WHERE host_id = ?`, hostID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var snap proto.Snapshot
	return &snap, json.Unmarshal([]byte(data), &snap)
}

// ---- events ----

type HostEvent struct {
	ID         int64   `json:"id"`
	HostID     int64   `json:"hostId"`
	Kind       string  `json:"kind"`    // offline | cpu | mem | disk | container | service
	Subject    string  `json:"subject"` // container or unit name; empty otherwise
	StartedAt  int64   `json:"startedAt"`
	ResolvedAt *int64  `json:"resolvedAt"`
	Peak       float64 `json:"peak"`
	Detail     string  `json:"detail"`
}

// OpenHostEvent starts an event for a kind and subject unless one is already
// open; it reports whether it opened a new one.
func (s *Store) OpenHostEvent(hostID int64, kind, subject string, at int64, peak float64, detail string) (bool, error) {
	res, err := s.db.Exec(`INSERT INTO host_events(host_id, kind, subject, started_at, peak, detail)
		SELECT ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM host_events WHERE host_id = ? AND kind = ? AND subject = ? AND resolved_at IS NULL)`,
		hostID, kind, subject, at, peak, detail, hostID, kind, subject)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) BumpHostEventPeak(hostID int64, kind string, v float64) error {
	_, err := s.db.Exec(`UPDATE host_events SET peak = max(peak, ?) WHERE host_id = ? AND kind = ? AND subject = '' AND resolved_at IS NULL`, v, hostID, kind)
	return err
}

// ResolveHostEvent closes the open event and returns when it started (0 if none).
func (s *Store) ResolveHostEvent(hostID int64, kind, subject string, at int64) (int64, error) {
	var started int64
	err := s.db.QueryRow(`UPDATE host_events SET resolved_at = ? WHERE host_id = ? AND kind = ? AND subject = ? AND resolved_at IS NULL RETURNING started_at`, at, hostID, kind, subject).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return started, err
}

// OpenHostEvents lists unresolved events, so the hub can restore state after a restart.
func (s *Store) OpenHostEvents() ([]HostEvent, error) {
	return s.queryHostEvents(`SELECT id, host_id, kind, subject, started_at, resolved_at, peak, detail FROM host_events WHERE resolved_at IS NULL`)
}

func (s *Store) HostEvents(hostID int64, limit int) ([]HostEvent, error) {
	return s.queryHostEvents(`SELECT id, host_id, kind, subject, started_at, resolved_at, peak, detail FROM host_events WHERE host_id = ? ORDER BY started_at DESC LIMIT ?`, hostID, limit)
}

func (s *Store) queryHostEvents(q string, args ...any) ([]HostEvent, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostEvent{}
	for rows.Next() {
		var e HostEvent
		var res sql.NullInt64
		if err := rows.Scan(&e.ID, &e.HostID, &e.Kind, &e.Subject, &e.StartedAt, &res, &e.Peak, &e.Detail); err != nil {
			return nil, err
		}
		if res.Valid {
			e.ResolvedAt = &res.Int64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
