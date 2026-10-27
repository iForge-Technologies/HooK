CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT    NOT NULL,
  totp_secret   BLOB,                       -- sealed; set while enrolling or enabled
  totp_enabled  INTEGER NOT NULL DEFAULT 0,
  totp_last     INTEGER NOT NULL DEFAULT 0, -- last accepted time step (replay guard)
  created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
  token_hash  BLOB    PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  mfa_pending INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  last_seen   INTEGER NOT NULL,
  ip          TEXT    NOT NULL DEFAULT '',
  user_agent  TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE channels (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  type       TEXT    NOT NULL,
  config     BLOB    NOT NULL,              -- sealed JSON
  is_default INTEGER NOT NULL DEFAULT 0,
  active     INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

CREATE TABLE monitors (
  id               INTEGER PRIMARY KEY,
  name             TEXT    NOT NULL,
  type             TEXT    NOT NULL,        -- http | keyword | tcp | dns | tls
  target           TEXT    NOT NULL,
  interval_s       INTEGER NOT NULL,
  retry_interval_s INTEGER NOT NULL,
  timeout_s        INTEGER NOT NULL,
  retries          INTEGER NOT NULL,
  degraded_ms      INTEGER NOT NULL DEFAULT 0,
  grp              TEXT    NOT NULL DEFAULT '',
  active           INTEGER NOT NULL DEFAULT 1,
  config           BLOB    NOT NULL,        -- sealed JSON
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  -- runtime state
  status           TEXT    NOT NULL DEFAULT 'pending',
  status_since     INTEGER NOT NULL DEFAULT 0,
  last_check       INTEGER NOT NULL DEFAULT 0,
  last_latency     INTEGER NOT NULL DEFAULT 0,
  last_msg         TEXT    NOT NULL DEFAULT '',
  cert_expires     INTEGER NOT NULL DEFAULT 0,
  cert_issuer      TEXT    NOT NULL DEFAULT '',
  cert_notified    INTEGER NOT NULL DEFAULT 0  -- smallest days-left threshold already alerted
);

CREATE TABLE monitor_channels (
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  PRIMARY KEY (monitor_id, channel_id)
) WITHOUT ROWID;

CREATE TABLE heartbeats (
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  ts         INTEGER NOT NULL,              -- unix ms
  status     TEXT    NOT NULL,              -- up | degraded | pending | down
  latency    INTEGER NOT NULL,
  code       INTEGER NOT NULL DEFAULT 0,
  msg        TEXT    NOT NULL DEFAULT '',
  timings    TEXT                           -- JSON, http only
);
CREATE INDEX heartbeats_monitor_ts ON heartbeats(monitor_id, ts);
CREATE INDEX heartbeats_ts ON heartbeats(ts);

CREATE TABLE rollup_1h (
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  hour       INTEGER NOT NULL,              -- unix ms, start of hour
  up         INTEGER NOT NULL,              -- includes degraded
  down       INTEGER NOT NULL,
  degraded   INTEGER NOT NULL,
  lat_sum    INTEGER NOT NULL,
  lat_n      INTEGER NOT NULL,
  lat_max    INTEGER NOT NULL,
  PRIMARY KEY (monitor_id, hour)
) WITHOUT ROWID;

CREATE TABLE incidents (
  id          INTEGER PRIMARY KEY,
  monitor_id  INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  started_at  INTEGER NOT NULL,
  resolved_at INTEGER,
  cause       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX incidents_monitor ON incidents(monitor_id, started_at);
