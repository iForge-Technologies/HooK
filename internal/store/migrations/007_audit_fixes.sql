-- Security audit fixes (2026-10-27).

-- Wrong 2FA codes per password sign-in; the session is dropped after a few.
ALTER TABLE sessions ADD COLUMN mfa_fails INTEGER NOT NULL DEFAULT 0;

-- Only servers the admin picked act as confirmation locations. An agent's own
-- "probes" flag says it can, not that it may.
ALTER TABLE hosts ADD COLUMN probe_location INTEGER NOT NULL DEFAULT 0;

-- Monitor and server IDs are never reused, so anything still pointing at a
-- deleted one can't silently start covering a new one.
CREATE TABLE id_seq (
  name TEXT PRIMARY KEY,
  last INTEGER NOT NULL
) WITHOUT ROWID;
INSERT INTO id_seq(name, last) SELECT 'monitors', coalesce(max(id), 0) FROM monitors;
INSERT INTO id_seq(name, last) SELECT 'hosts', coalesce(max(id), 0) FROM hosts;

-- Status pages and maintenance windows keep their members as JSON lists, which
-- foreign keys don't reach: drop deleted monitors and servers from them.
UPDATE status_pages SET monitor_ids = (SELECT json_group_array(value ORDER BY key) FROM json_each(status_pages.monitor_ids)
  WHERE value IN (SELECT id FROM monitors));
UPDATE maintenance SET
  monitor_ids = (SELECT json_group_array(value ORDER BY key) FROM json_each(maintenance.monitor_ids) WHERE value IN (SELECT id FROM monitors)),
  host_ids = (SELECT json_group_array(value ORDER BY key) FROM json_each(maintenance.host_ids) WHERE value IN (SELECT id FROM hosts));

CREATE TRIGGER monitors_unlink AFTER DELETE ON monitors BEGIN
  UPDATE status_pages SET monitor_ids = (SELECT json_group_array(value ORDER BY key) FROM json_each(status_pages.monitor_ids) WHERE value != OLD.id)
    WHERE EXISTS (SELECT 1 FROM json_each(status_pages.monitor_ids) WHERE value = OLD.id);
  UPDATE maintenance SET monitor_ids = (SELECT json_group_array(value ORDER BY key) FROM json_each(maintenance.monitor_ids) WHERE value != OLD.id)
    WHERE EXISTS (SELECT 1 FROM json_each(maintenance.monitor_ids) WHERE value = OLD.id);
END;

CREATE TRIGGER hosts_unlink AFTER DELETE ON hosts BEGIN
  UPDATE maintenance SET host_ids = (SELECT json_group_array(value ORDER BY key) FROM json_each(maintenance.host_ids) WHERE value != OLD.id)
    WHERE EXISTS (SELECT 1 FROM json_each(maintenance.host_ids) WHERE value = OLD.id);
END;
