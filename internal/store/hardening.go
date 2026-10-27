package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// ---- settings ----

func (s *Store) Setting(key string) ([]byte, error) {
	var v []byte
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(key string, value []byte) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// SecretSetting reads a setting stored sealed with the hub's key.
func (s *Store) SecretSetting(key string) ([]byte, error) {
	v, err := s.Setting(key)
	if err != nil {
		return nil, err
	}
	return s.box.Open(v)
}

func (s *Store) SetSecretSetting(key string, value []byte) error {
	return s.SetSetting(key, s.box.Seal(value))
}

// ---- browser push subscriptions ----

type PushSubscription struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	Endpoint  string `json:"-"`
	P256dh    string `json:"-"`
	Auth      string `json:"-"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"createdAt"`
	LastOK    int64  `json:"lastOk"`
	Failures  int    `json:"failures"`
	Host      string `json:"host"` // push service host, for display
}

// SavePushSubscription upserts by endpoint (a browser re-subscribing).
func (s *Store) SavePushSubscription(p *PushSubscription) error {
	keys, _ := json.Marshal(map[string]string{"p256dh": p.P256dh, "auth": p.Auth})
	return s.db.QueryRow(`INSERT INTO push_subscriptions(user_id, endpoint, keys, label, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET user_id = excluded.user_id, keys = excluded.keys, label = excluded.label, failures = 0
		RETURNING id`, p.UserID, p.Endpoint, s.box.Seal(keys), p.Label, nowMs()).Scan(&p.ID)
}

func (s *Store) PushSubscriptions() ([]*PushSubscription, error) {
	rows, err := s.db.Query(`SELECT id, user_id, endpoint, keys, label, created_at, last_ok, failures FROM push_subscriptions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PushSubscription{}
	for rows.Next() {
		var p PushSubscription
		var sealed []byte
		if err := rows.Scan(&p.ID, &p.UserID, &p.Endpoint, &sealed, &p.Label, &p.CreatedAt, &p.LastOK, &p.Failures); err != nil {
			return nil, err
		}
		if plain, err := s.box.Open(sealed); err == nil {
			var k map[string]string
			json.Unmarshal(plain, &k)
			p.P256dh, p.Auth = k["p256dh"], k["auth"]
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) PushDelivered(id int64, ok bool) error {
	if ok {
		_, err := s.db.Exec(`UPDATE push_subscriptions SET last_ok = ?, failures = 0 WHERE id = ?`, nowMs(), id)
		return err
	}
	_, err := s.db.Exec(`UPDATE push_subscriptions SET failures = failures + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) DeletePushSubscription(id int64) error {
	res, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePushEndpoint(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// ---- kiosk links ----

type KioskLink struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
}

func (s *Store) CreateKioskLink(name, token string) (*KioskLink, error) {
	k := &KioskLink{Name: name, CreatedAt: nowMs()}
	res, err := s.db.Exec(`INSERT INTO kiosk_links(name, token_hash, created_at) VALUES(?,?,?)`, name, hashToken(token), k.CreatedAt)
	if err != nil {
		return nil, err
	}
	k.ID, _ = res.LastInsertId()
	return k, nil
}

func (s *Store) KioskLinks() ([]KioskLink, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, last_seen FROM kiosk_links ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KioskLink{}
	for rows.Next() {
		var k KioskLink
		if err := rows.Scan(&k.ID, &k.Name, &k.CreatedAt, &k.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KioskByToken finds a link and records that a display used it.
func (s *Store) KioskByToken(token string) (*KioskLink, error) {
	var k KioskLink
	err := s.db.QueryRow(`UPDATE kiosk_links SET last_seen = ? WHERE token_hash = ? RETURNING id, name, created_at, last_seen`, nowMs(), hashToken(token)).
		Scan(&k.ID, &k.Name, &k.CreatedAt, &k.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &k, err
}

func (s *Store) DeleteKioskLink(id int64) error {
	res, err := s.db.Exec(`DELETE FROM kiosk_links WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- health and backups ----

// Ping proves the database answers and can write.
func (s *Store) Ping() error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES('health', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, []byte(strconv.FormatInt(nowMs(), 10)))
	return err
}

// Backup writes a consistent copy of the live database to path with
// VACUUM INTO, which is safe while the hub is running.
func (s *Store) Backup(path string) error {
	if _, err := os.Stat(path); err == nil {
		return errors.New("refusing to overwrite " + path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_, err := s.db.Exec(`VACUUM INTO ?`, path)
	return err
}

// Size returns the database file size in bytes (main file plus WAL).
func (s *Store) Size(dataDir string) int64 {
	var n int64
	for _, f := range []string{"hook.db", "hook.db-wal"} {
		if st, err := os.Stat(filepath.Join(dataDir, f)); err == nil {
			n += st.Size()
		}
	}
	return n
}
