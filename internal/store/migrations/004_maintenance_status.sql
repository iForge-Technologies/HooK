-- Phase 5: maintenance windows and public status pages.

CREATE TABLE maintenance (
  id          INTEGER PRIMARY KEY,
  title       TEXT    NOT NULL,
  starts_at   INTEGER NOT NULL,             -- unix ms of the first occurrence
  ends_at     INTEGER NOT NULL,
  repeat      TEXT    NOT NULL DEFAULT '',  -- '' | daily | weekly
  everything  INTEGER NOT NULL DEFAULT 0,   -- applies to every monitor and server
  monitor_ids TEXT    NOT NULL DEFAULT '[]',
  host_ids    TEXT    NOT NULL DEFAULT '[]',
  created_at  INTEGER NOT NULL
);

CREATE TABLE status_pages (
  id           INTEGER PRIMARY KEY,
  slug         TEXT    NOT NULL UNIQUE,
  title        TEXT    NOT NULL,
  description  TEXT    NOT NULL DEFAULT '',
  monitor_ids  TEXT    NOT NULL DEFAULT '[]', -- shown in this order
  show_latency INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL
);
