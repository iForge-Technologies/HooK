-- Phase 2: servers reporting through the agent.

CREATE TABLE hosts (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL,
  grp          TEXT    NOT NULL DEFAULT '',
  token_hash   BLOB    NOT NULL UNIQUE,      -- sha256 of the agent token
  info         TEXT    NOT NULL DEFAULT '{}', -- proto.HostInfo JSON from the last hello
  thresholds   TEXT    NOT NULL DEFAULT '{}',
  status       TEXT    NOT NULL DEFAULT 'new', -- new | online | offline
  status_since INTEGER NOT NULL DEFAULT 0,
  last_seen    INTEGER NOT NULL DEFAULT 0,
  remote_ip    TEXT    NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL
);

CREATE TABLE host_channels (
  host_id    INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  PRIMARY KEY (host_id, channel_id)
) WITHOUT ROWID;

-- Raw frames, every few seconds. Replayed frames after a reconnect are ignored
-- by the primary key.
CREATE TABLE host_metrics (
  host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  ts      INTEGER NOT NULL,
  cpu         REAL NOT NULL DEFAULT 0,
  iowait      REAL NOT NULL DEFAULT 0,
  steal       REAL NOT NULL DEFAULT 0,
  load1       REAL NOT NULL DEFAULT 0,
  load5       REAL NOT NULL DEFAULT 0,
  load15      REAL NOT NULL DEFAULT 0,
  mem_used    REAL NOT NULL DEFAULT 0,
  mem_total   REAL NOT NULL DEFAULT 0,
  swap_used   REAL NOT NULL DEFAULT 0,
  swap_total  REAL NOT NULL DEFAULT 0,
  disk_read   REAL NOT NULL DEFAULT 0,
  disk_write  REAL NOT NULL DEFAULT 0,
  net_rx      REAL NOT NULL DEFAULT 0,
  net_tx      REAL NOT NULL DEFAULT 0,
  tcp         REAL NOT NULL DEFAULT 0,
  procs       REAL NOT NULL DEFAULT 0,
  disk_pct    REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (host_id, ts)
) WITHOUT ROWID;
CREATE INDEX host_metrics_ts ON host_metrics(ts);

CREATE TABLE host_rollup_1m (
  host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  ts      INTEGER NOT NULL,                -- bucket start, unix ms
  n       INTEGER NOT NULL,                -- samples in the bucket
  cpu         REAL NOT NULL DEFAULT 0,
  iowait      REAL NOT NULL DEFAULT 0,
  steal       REAL NOT NULL DEFAULT 0,
  load1       REAL NOT NULL DEFAULT 0,
  load5       REAL NOT NULL DEFAULT 0,
  load15      REAL NOT NULL DEFAULT 0,
  mem_used    REAL NOT NULL DEFAULT 0,
  mem_total   REAL NOT NULL DEFAULT 0,
  swap_used   REAL NOT NULL DEFAULT 0,
  swap_total  REAL NOT NULL DEFAULT 0,
  disk_read   REAL NOT NULL DEFAULT 0,
  disk_write  REAL NOT NULL DEFAULT 0,
  net_rx      REAL NOT NULL DEFAULT 0,
  net_tx      REAL NOT NULL DEFAULT 0,
  tcp         REAL NOT NULL DEFAULT 0,
  procs       REAL NOT NULL DEFAULT 0,
  disk_pct    REAL NOT NULL DEFAULT 0,
  cpu_max         REAL NOT NULL DEFAULT 0,
  mem_used_max    REAL NOT NULL DEFAULT 0,
  load1_max       REAL NOT NULL DEFAULT 0,
  disk_read_max   REAL NOT NULL DEFAULT 0,
  disk_write_max  REAL NOT NULL DEFAULT 0,
  net_rx_max      REAL NOT NULL DEFAULT 0,
  net_tx_max      REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (host_id, ts)
) WITHOUT ROWID;
CREATE INDEX host_rollup_1m_ts ON host_rollup_1m(ts);

CREATE TABLE host_rollup_1h (
  host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  ts      INTEGER NOT NULL,                -- bucket start, unix ms
  n       INTEGER NOT NULL,                -- samples in the bucket
  cpu         REAL NOT NULL DEFAULT 0,
  iowait      REAL NOT NULL DEFAULT 0,
  steal       REAL NOT NULL DEFAULT 0,
  load1       REAL NOT NULL DEFAULT 0,
  load5       REAL NOT NULL DEFAULT 0,
  load15      REAL NOT NULL DEFAULT 0,
  mem_used    REAL NOT NULL DEFAULT 0,
  mem_total   REAL NOT NULL DEFAULT 0,
  swap_used   REAL NOT NULL DEFAULT 0,
  swap_total  REAL NOT NULL DEFAULT 0,
  disk_read   REAL NOT NULL DEFAULT 0,
  disk_write  REAL NOT NULL DEFAULT 0,
  net_rx      REAL NOT NULL DEFAULT 0,
  net_tx      REAL NOT NULL DEFAULT 0,
  tcp         REAL NOT NULL DEFAULT 0,
  procs       REAL NOT NULL DEFAULT 0,
  disk_pct    REAL NOT NULL DEFAULT 0,
  cpu_max         REAL NOT NULL DEFAULT 0,
  mem_used_max    REAL NOT NULL DEFAULT 0,
  load1_max       REAL NOT NULL DEFAULT 0,
  disk_read_max   REAL NOT NULL DEFAULT 0,
  disk_write_max  REAL NOT NULL DEFAULT 0,
  net_rx_max      REAL NOT NULL DEFAULT 0,
  net_tx_max      REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (host_id, ts)
) WITHOUT ROWID;
CREATE INDEX host_rollup_1h_ts ON host_rollup_1h(ts);

CREATE TABLE host_snapshots (
  host_id INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
  ts      INTEGER NOT NULL,
  data    TEXT    NOT NULL                 -- proto.Snapshot JSON
);

-- Threshold breaches and offline periods.
CREATE TABLE host_events (
  id          INTEGER PRIMARY KEY,
  host_id     INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  kind        TEXT    NOT NULL,             -- offline | cpu | mem | disk
  started_at  INTEGER NOT NULL,
  resolved_at INTEGER,
  peak        REAL    NOT NULL DEFAULT 0,
  detail      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX host_events_host ON host_events(host_id, started_at);
