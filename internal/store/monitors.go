package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// MonitorConfig holds type-specific settings. It is stored sealed because
// headers often carry auth tokens.
type MonitorConfig struct {
	// http / keyword
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	ExpectedStatus  string            `json:"expectedStatus,omitempty"` // e.g. "200-299,301"
	FollowRedirects bool              `json:"followRedirects"`
	IgnoreTLS       bool              `json:"ignoreTls"`
	Keyword         string            `json:"keyword,omitempty"`
	KeywordInvert   bool              `json:"keywordInvert"`
	// http / keyword / tls
	CertAlerts bool `json:"certAlerts"`
	// network types: don't ask other locations to confirm a failure
	NoConfirm bool `json:"noConfirm"`
	// dns
	DNSRecord   string `json:"dnsRecord,omitempty"`   // A AAAA CNAME MX TXT NS
	DNSResolver string `json:"dnsResolver,omitempty"` // host[:port]; empty = system
	DNSExpected string `json:"dnsExpected,omitempty"`
	// postgres / mysql / redis / mongodb: connection string with credentials
	DSN string `json:"dsn,omitempty"`
	// push: the token in the push URL (its hash is indexed for lookups)
	PushToken string `json:"pushToken,omitempty"`
	// vercel / render: which account and project
	IntegrationID int64  `json:"integrationId,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
	ProjectName   string `json:"projectName,omitempty"`
}

type Monitor struct {
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	Type          string        `json:"type"`
	Target        string        `json:"target"`
	Interval      int           `json:"interval"`      // seconds
	RetryInterval int           `json:"retryInterval"` // seconds
	Timeout       int           `json:"timeout"`       // seconds
	Retries       int           `json:"retries"`
	DegradedMs    int           `json:"degradedMs"`
	Group         string        `json:"group"`
	Active        bool          `json:"active"`
	Config        MonitorConfig `json:"config"`
	ChannelIDs    []int64       `json:"channelIds"`
	CreatedAt     int64         `json:"createdAt"`

	Status       string `json:"status"`
	StatusSince  int64  `json:"statusSince"`
	LastCheck    int64  `json:"lastCheck"`
	LastLatency  int    `json:"lastLatency"`
	LastMsg      string `json:"lastMsg"`
	CertExpires  int64  `json:"certExpires"`
	CertIssuer   string `json:"certIssuer"`
	CertNotified int    `json:"-"`
}

const monitorCols = `id, name, type, target, interval_s, retry_interval_s, timeout_s, retries, degraded_ms, grp, active, config, created_at,
	status, status_since, last_check, last_latency, last_msg, cert_expires, cert_issuer, cert_notified`

type scanner interface{ Scan(...any) error }

func (s *Store) scanMonitor(r scanner) (*Monitor, error) {
	var m Monitor
	var sealed []byte
	err := r.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.Interval, &m.RetryInterval, &m.Timeout, &m.Retries, &m.DegradedMs, &m.Group, &m.Active, &sealed, &m.CreatedAt,
		&m.Status, &m.StatusSince, &m.LastCheck, &m.LastLatency, &m.LastMsg, &m.CertExpires, &m.CertIssuer, &m.CertNotified)
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
	if err := json.Unmarshal(plain, &m.Config); err != nil {
		return nil, err
	}
	m.ChannelIDs = []int64{}
	return &m, nil
}

func (s *Store) Monitor(id int64) (*Monitor, error) {
	m, err := s.scanMonitor(s.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	ids, err := s.monitorChannelIDs()
	if err != nil {
		return nil, err
	}
	if v := ids[id]; v != nil {
		m.ChannelIDs = v
	}
	return m, nil
}

func (s *Store) Monitors() ([]*Monitor, error) {
	rows, err := s.db.Query(`SELECT ` + monitorCols + ` FROM monitors ORDER BY grp COLLATE NOCASE, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Monitor
	for rows.Next() {
		m, err := s.scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids, err := s.monitorChannelIDs()
	if err != nil {
		return nil, err
	}
	for _, m := range out {
		if v := ids[m.ID]; v != nil {
			m.ChannelIDs = v
		}
	}
	return out, nil
}

func (s *Store) monitorChannelIDs() (map[int64][]int64, error) {
	rows, err := s.db.Query(`SELECT monitor_id, channel_id FROM monitor_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var m, c int64
		if err := rows.Scan(&m, &c); err != nil {
			return nil, err
		}
		out[m] = append(out[m], c)
	}
	return out, rows.Err()
}

func (s *Store) sealConfig(c MonitorConfig) []byte {
	b, _ := json.Marshal(c)
	return s.box.Seal(b)
}

func (s *Store) CreateMonitor(m *Monitor) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowMs()
	id, err := nextID(tx, "monitors")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO monitors(id, name, type, target, interval_s, retry_interval_s, timeout_s, retries, degraded_ms, grp, active, config, created_at, updated_at, status, status_since)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, m.Name, m.Type, m.Target, m.Interval, m.RetryInterval, m.Timeout, m.Retries, m.DegradedMs, m.Group, boolInt(m.Active), s.sealConfig(m.Config), now, now,
		initialStatus(m.Active), now); err != nil {
		return err
	}
	m.ID = id
	if err := setChannels(tx, m.ID, m.ChannelIDs); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateMonitor saves settings. Runtime state is reset when the check itself changes.
func (s *Store) UpdateMonitor(m *Monitor, resetState bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowMs()
	_, err = tx.Exec(`UPDATE monitors SET name=?, type=?, target=?, interval_s=?, retry_interval_s=?, timeout_s=?, retries=?, degraded_ms=?, grp=?, config=?, updated_at=? WHERE id=?`,
		m.Name, m.Type, m.Target, m.Interval, m.RetryInterval, m.Timeout, m.Retries, m.DegradedMs, m.Group, s.sealConfig(m.Config), now, m.ID)
	if err != nil {
		return err
	}
	if resetState {
		if _, err := tx.Exec(`UPDATE monitors SET status = ?, status_since = ?, last_msg = '', cert_expires = 0, cert_issuer = '', cert_notified = 0 WHERE id = ?`,
			initialStatus(m.Active), now, m.ID); err != nil {
			return err
		}
	}
	if err := setChannels(tx, m.ID, m.ChannelIDs); err != nil {
		return err
	}
	return tx.Commit()
}

// nextID hands out the next ID for monitors or hosts. IDs are never reused:
// SQLite would otherwise give a deleted row's ID to the next insert.
func nextID(tx *sql.Tx, table string) (int64, error) {
	if table != "monitors" && table != "hosts" {
		return 0, errors.New("nextID: unknown table " + table)
	}
	var id int64
	err := tx.QueryRow(`UPDATE id_seq SET last = max(last, (SELECT coalesce(max(id), 0) FROM `+table+`)) + 1 WHERE name = ? RETURNING last`, table).Scan(&id)
	return id, err
}

func setChannels(tx *sql.Tx, monitorID int64, ids []int64) error {
	if _, err := tx.Exec(`DELETE FROM monitor_channels WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}
	for _, c := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO monitor_channels(monitor_id, channel_id) SELECT ?, id FROM channels WHERE id = ?`, monitorID, c); err != nil {
			return err
		}
	}
	return nil
}

func initialStatus(active bool) string {
	if active {
		return "pending"
	}
	return "paused"
}

func (s *Store) DeleteMonitor(id int64) error {
	res, err := s.db.Exec(`DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetMonitorActive(id int64, active bool) error {
	res, err := s.db.Exec(`UPDATE monitors SET active = ?, status = ?, status_since = ?, updated_at = ? WHERE id = ?`,
		boolInt(active), initialStatus(active), nowMs(), nowMs(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MonitorState is what the scheduler writes after every check.
type MonitorState struct {
	Status      string
	StatusSince int64
	LastCheck   int64
	LastLatency int
	LastMsg     string
}

func (s *Store) SaveState(id int64, st MonitorState) error {
	_, err := s.db.Exec(`UPDATE monitors SET status=?, status_since=?, last_check=?, last_latency=?, last_msg=? WHERE id=? AND active = 1`,
		st.Status, st.StatusSince, st.LastCheck, st.LastLatency, st.LastMsg, id)
	return err
}

func (s *Store) SaveCert(id int64, expires int64, issuer string, notified int) error {
	_, err := s.db.Exec(`UPDATE monitors SET cert_expires=?, cert_issuer=?, cert_notified=? WHERE id=?`, expires, issuer, notified, id)
	return err
}

// Groups lists distinct non-empty group names, for form suggestions.
func (s *Store) Groups() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT grp FROM monitors WHERE grp != '' ORDER BY grp COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimSpace(g))
	}
	return out, rows.Err()
}
