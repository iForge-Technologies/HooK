-- GPU history, daily traffic totals and speed tests.

ALTER TABLE host_metrics ADD COLUMN gpu REAL NOT NULL DEFAULT 0;
ALTER TABLE host_metrics ADD COLUMN gpu_mem REAL NOT NULL DEFAULT 0;
ALTER TABLE host_rollup_1m ADD COLUMN gpu REAL NOT NULL DEFAULT 0;
ALTER TABLE host_rollup_1m ADD COLUMN gpu_mem REAL NOT NULL DEFAULT 0;
ALTER TABLE host_rollup_1m ADD COLUMN gpu_max REAL NOT NULL DEFAULT 0;
ALTER TABLE host_rollup_1h ADD COLUMN gpu REAL NOT NULL DEFAULT 0;
ALTER TABLE host_rollup_1h ADD COLUMN gpu_mem REAL NOT NULL DEFAULT 0;
ALTER TABLE host_rollup_1h ADD COLUMN gpu_max REAL NOT NULL DEFAULT 0;

-- Bytes in and out per UTC day, summed from the agent's per-frame deltas.
CREATE TABLE host_traffic (
  host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  day     INTEGER NOT NULL,             -- UTC midnight, unix ms
  rx      REAL    NOT NULL DEFAULT 0,
  tx      REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (host_id, day)
) WITHOUT ROWID;

CREATE TABLE speed_tests (
  id         INTEGER PRIMARY KEY,
  host_id    INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  ts         INTEGER NOT NULL,
  down_bps   REAL    NOT NULL DEFAULT 0,
  up_bps     REAL    NOT NULL DEFAULT 0,
  latency_ms REAL    NOT NULL DEFAULT 0,
  jitter_ms  REAL    NOT NULL DEFAULT 0,
  server     TEXT    NOT NULL DEFAULT '',
  bytes      INTEGER NOT NULL DEFAULT 0,
  error      TEXT    NOT NULL DEFAULT '',
  scheduled  INTEGER NOT NULL DEFAULT 0  -- 1 when run by the schedule, 0 when asked for
);
CREATE INDEX speed_tests_host ON speed_tests(host_id, ts);
