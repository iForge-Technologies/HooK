package store

import (
	"encoding/json"

	"hook/internal/proto"
)

// Fleet-wide aggregates for the overview page.

type LatencyBucket struct {
	TS       int64 `json:"ts"`
	Latency  int   `json:"latency"` // mean of successful checks across all monitors
	Up       int   `json:"up"`      // includes degraded
	Down     int   `json:"down"`
	Degraded int   `json:"degraded"`
}

// FleetLatency folds hourly monitor rollups into buckets of bucketMs.
func (s *Store) FleetLatency(fromMs, bucketMs int64) ([]LatencyBucket, error) {
	rows, err := s.db.Query(`SELECT (hour / ?) * ? AS b, CASE WHEN sum(lat_n) > 0 THEN sum(lat_sum) / sum(lat_n) ELSE 0 END,
		sum(up), sum(down), sum(degraded)
		FROM rollup_1h WHERE hour >= ? GROUP BY b ORDER BY b`, bucketMs, bucketMs, fromMs/3_600_000*3_600_000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LatencyBucket{}
	for rows.Next() {
		var b LatencyBucket
		if err := rows.Scan(&b.TS, &b.Latency, &b.Up, &b.Down, &b.Degraded); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FleetUptime counts confirmed up and down checks across all monitors in [from, to).
func (s *Store) FleetUptime(fromMs, toMs int64) (up, down int64, err error) {
	err = s.db.QueryRow(`SELECT coalesce(sum(up), 0), coalesce(sum(down), 0) FROM rollup_1h WHERE hour >= ? AND hour < ?`,
		fromMs/3_600_000*3_600_000, toMs/3_600_000*3_600_000).Scan(&up, &down)
	return
}

// FeedItem is an outage or alert from either a monitor or a server.
type FeedItem struct {
	Source     string `json:"source"` // monitor | server
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // down | offline | cpu | mem | disk
	StartedAt  int64  `json:"startedAt"`
	ResolvedAt *int64 `json:"resolvedAt"`
	Detail     string `json:"detail"`
}

// Feed lists incidents and server events that were open at any point since fromMs.
func (s *Store) Feed(fromMs int64) ([]FeedItem, error) {
	rows, err := s.db.Query(`
SELECT 'monitor', m.id, m.name, 'down', i.started_at, i.resolved_at, i.cause
  FROM incidents i JOIN monitors m ON m.id = i.monitor_id
 WHERE i.resolved_at IS NULL OR i.resolved_at >= ?
UNION ALL
SELECT 'server', h.id, h.name || CASE WHEN e.subject != '' THEN ' · ' || e.subject ELSE '' END, e.kind, e.started_at, e.resolved_at, e.detail
  FROM host_events e JOIN hosts h ON h.id = e.host_id
 WHERE e.resolved_at IS NULL OR e.resolved_at >= ?
ORDER BY 5 DESC LIMIT 500`, fromMs, fromMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeedItem{}
	for rows.Next() {
		var f FeedItem
		if err := rows.Scan(&f.Source, &f.ID, &f.Name, &f.Kind, &f.StartedAt, &f.ResolvedAt, &f.Detail); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type NetBucket struct {
	TS int64   `json:"ts"`
	Rx float64 `json:"rx"` // bytes/s summed across servers
	Tx float64 `json:"tx"`
}

// FleetNetwork averages each server's rates per bucket, then sums the servers.
// fine picks minute rollups (short ranges) over hourly ones.
func (s *Store) FleetNetwork(fromMs, bucketMs int64, fine bool) ([]NetBucket, error) {
	table := "host_rollup_1h"
	if fine {
		table = "host_rollup_1m"
	}
	rows, err := s.db.Query(`SELECT b, sum(rx), sum(tx) FROM (
		SELECT host_id, (ts / ?) * ? AS b, avg(net_rx) AS rx, avg(net_tx) AS tx FROM `+table+` WHERE ts >= ? GROUP BY host_id, b
	) GROUP BY b ORDER BY b`, bucketMs, bucketMs, fromMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetBucket{}
	for rows.Next() {
		var b NetBucket
		if err := rows.Scan(&b.TS, &b.Rx, &b.Tx); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Snapshots returns every server's latest snapshot.
func (s *Store) Snapshots() (map[int64]*proto.Snapshot, error) {
	rows, err := s.db.Query(`SELECT host_id, data FROM host_snapshots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*proto.Snapshot{}
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var snap proto.Snapshot
		if json.Unmarshal([]byte(data), &snap) == nil {
			out[id] = &snap
		}
	}
	return out, rows.Err()
}
