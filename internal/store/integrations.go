package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Integration is a connected Vercel or Render account.
type Integration struct {
	ID     int64             `json:"id"`
	Kind   string            `json:"kind"` // vercel | render
	Name   string            `json:"name"`
	Config map[string]string `json:"config"` // token, teamId
}

func (s *Store) scanIntegration(r scanner) (*Integration, error) {
	var in Integration
	var sealed []byte
	err := r.Scan(&in.ID, &in.Kind, &in.Name, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return nil, err
	}
	in.Config = map[string]string{}
	return &in, json.Unmarshal(plain, &in.Config)
}

func (s *Store) Integration(id int64) (*Integration, error) {
	return s.scanIntegration(s.db.QueryRow(`SELECT id, kind, name, config FROM integrations WHERE id = ?`, id))
}

func (s *Store) Integrations() ([]*Integration, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, config FROM integrations ORDER BY kind, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Integration{}
	for rows.Next() {
		in, err := s.scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) SaveIntegration(in *Integration) error {
	b, _ := json.Marshal(in.Config)
	sealed := s.box.Seal(b)
	if in.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO integrations(kind, name, config, created_at) VALUES(?,?,?,?)`, in.Kind, in.Name, sealed, nowMs())
		if err != nil {
			return err
		}
		in.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.Exec(`UPDATE integrations SET name = ?, config = ? WHERE id = ?`, in.Name, sealed, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteIntegration(id int64) error {
	res, err := s.db.Exec(`DELETE FROM integrations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- push monitors ----

// SetPushToken stores the lookup hash for a push monitor's token.
func (s *Store) SetPushToken(monitorID int64, token string) error {
	_, err := s.db.Exec(`UPDATE monitors SET push_hash = ? WHERE id = ?`, hashToken(token), monitorID)
	return err
}

func (s *Store) MonitorByPushToken(token string) (*Monitor, error) {
	return s.scanMonitor(s.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE push_hash = ?`, hashToken(token)))
}
