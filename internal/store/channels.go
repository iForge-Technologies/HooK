package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

type Channel struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Config    map[string]string `json:"config"`
	IsDefault bool              `json:"isDefault"`
	Active    bool              `json:"active"`
}

func (s *Store) scanChannel(r scanner) (*Channel, error) {
	var c Channel
	var sealed []byte
	err := r.Scan(&c.ID, &c.Name, &c.Type, &sealed, &c.IsDefault, &c.Active)
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
	c.Config = map[string]string{}
	return &c, json.Unmarshal(plain, &c.Config)
}

const channelCols = `id, name, type, config, is_default, active`

func (s *Store) Channel(id int64) (*Channel, error) {
	return s.scanChannel(s.db.QueryRow(`SELECT `+channelCols+` FROM channels WHERE id = ?`, id))
}

func (s *Store) Channels() ([]*Channel, error) {
	return s.queryChannels(`SELECT ` + channelCols + ` FROM channels ORDER BY name COLLATE NOCASE`)
}

// MonitorChannels returns the active channels linked to a monitor.
func (s *Store) MonitorChannels(monitorID int64) ([]*Channel, error) {
	return s.queryChannels(`SELECT c.id, c.name, c.type, c.config, c.is_default, c.active FROM channels c
		JOIN monitor_channels mc ON mc.channel_id = c.id WHERE mc.monitor_id = ? AND c.active = 1`, monitorID)
}

func (s *Store) queryChannels(q string, args ...any) ([]*Channel, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Channel{}
	for rows.Next() {
		c, err := s.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveChannel(c *Channel) error {
	b, _ := json.Marshal(c.Config)
	sealed := s.box.Seal(b)
	if c.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO channels(name, type, config, is_default, active, created_at) VALUES(?,?,?,?,?,?)`,
			c.Name, c.Type, sealed, boolInt(c.IsDefault), boolInt(c.Active), nowMs())
		if err != nil {
			return err
		}
		c.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.Exec(`UPDATE channels SET name=?, type=?, config=?, is_default=?, active=? WHERE id=?`,
		c.Name, c.Type, sealed, boolInt(c.IsDefault), boolInt(c.Active), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ApplyChannelToAll links a channel to every existing monitor.
func (s *Store) ApplyChannelToAll(channelID int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO monitor_channels(monitor_id, channel_id) SELECT id, ? FROM monitors`, channelID)
	return err
}

func (s *Store) DeleteChannel(id int64) error {
	res, err := s.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
