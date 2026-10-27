-- Phase 4: push monitors, platform accounts, per-subject server events.

-- sha256 of a push monitor's token, so /api/push/{token} can find it.
ALTER TABLE monitors ADD COLUMN push_hash BLOB;
CREATE UNIQUE INDEX monitors_push_hash ON monitors(push_hash) WHERE push_hash IS NOT NULL;

-- Vercel / Render accounts. The API token lives in the sealed config.
CREATE TABLE integrations (
  id         INTEGER PRIMARY KEY,
  kind       TEXT    NOT NULL,              -- vercel | render
  name       TEXT    NOT NULL,
  config     BLOB    NOT NULL,              -- sealed JSON: token, teamId
  created_at INTEGER NOT NULL
);

-- Container and service alerts are per container / unit, so events need a
-- subject alongside their kind. Existing rows (cpu, mem, disk, offline) keep ''.
ALTER TABLE host_events ADD COLUMN subject TEXT NOT NULL DEFAULT '';
CREATE INDEX host_events_open ON host_events(host_id, kind, subject) WHERE resolved_at IS NULL;
