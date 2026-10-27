-- Phase 6: hub settings, browser push subscriptions, kiosk links.

-- Small key/value settings. Values are sealed when they're secret (see
-- Store.SecretSetting), e.g. the VAPID private key.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value BLOB NOT NULL
) WITHOUT ROWID;

-- One row per browser/phone that enabled notifications.
CREATE TABLE push_subscriptions (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint   TEXT    NOT NULL UNIQUE,
  keys       BLOB    NOT NULL,              -- sealed JSON {p256dh, auth}
  label      TEXT    NOT NULL DEFAULT '',   -- e.g. "Chrome on Android"
  created_at INTEGER NOT NULL,
  last_ok    INTEGER NOT NULL DEFAULT 0,
  failures   INTEGER NOT NULL DEFAULT 0
);

-- Read-only links for wall displays. Only the token's hash is stored.
CREATE TABLE kiosk_links (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  token_hash BLOB    NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL DEFAULT 0
);
