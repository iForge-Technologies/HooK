package store

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	TOTPSecret   []byte `json:"-"` // plaintext after load
	TOTPEnabled  bool   `json:"totpEnabled"`
	TOTPLast     int64  `json:"-"`
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, passwordHash string) (*User, error) {
	res, err := s.db.Exec(`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`, username, passwordHash, nowMs())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, PasswordHash: passwordHash}, nil
}

const userCols = `id, username, password_hash, totp_secret, totp_enabled, totp_last`

func (s *Store) scanUser(row *sql.Row) (*User, error) {
	var u User
	var sealed []byte
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &sealed, &u.TOTPEnabled, &u.TOTPLast); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(sealed) > 0 {
		plain, err := s.box.Open(sealed)
		if err != nil {
			return nil, err
		}
		u.TOTPSecret = plain
	}
	return &u, nil
}

func (s *Store) UserByName(username string) (*User, error) {
	return s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, username))
}

func (s *Store) UserByID(id int64) (*User, error) {
	return s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) SetPassword(userID int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	return err
}

// SetTOTP stores a secret (nil clears it) and the enabled flag.
func (s *Store) SetTOTP(userID int64, secret []byte, enabled bool) error {
	var sealed []byte
	if secret != nil {
		sealed = s.box.Seal(secret)
	}
	_, err := s.db.Exec(`UPDATE users SET totp_secret = ?, totp_enabled = ?, totp_last = 0 WHERE id = ?`, sealed, boolInt(enabled), userID)
	return err
}

// UseTOTPStep records an accepted time step; false means it was already used.
func (s *Store) UseTOTPStep(userID, step int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE users SET totp_last = ? WHERE id = ? AND totp_last < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- sessions ----

type Session struct {
	UserID     int64
	MFAPending bool
	ExpiresAt  time.Time
	LastSeen   time.Time
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func (s *Store) CreateSession(token string, userID int64, mfaPending bool, ttl time.Duration, ip, ua string) error {
	now := time.Now()
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash, user_id, mfa_pending, created_at, expires_at, last_seen, ip, user_agent) VALUES(?,?,?,?,?,?,?,?)`,
		hashToken(token), userID, boolInt(mfaPending), now.UnixMilli(), now.Add(ttl).UnixMilli(), now.UnixMilli(), ip, ua)
	return err
}

func (s *Store) GetSession(token string) (*Session, error) {
	var se Session
	var exp, seen int64
	err := s.db.QueryRow(`SELECT user_id, mfa_pending, expires_at, last_seen FROM sessions WHERE token_hash = ?`, hashToken(token)).
		Scan(&se.UserID, &se.MFAPending, &exp, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	se.ExpiresAt, se.LastSeen = time.UnixMilli(exp), time.UnixMilli(seen)
	if time.Now().After(se.ExpiresAt) {
		s.DeleteSession(token)
		return nil, ErrNotFound
	}
	return &se, nil
}

// TouchSession slides the expiry forward.
func (s *Store) TouchSession(token string, ttl time.Duration) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE sessions SET last_seen = ?, expires_at = ? WHERE token_hash = ?`, now.UnixMilli(), now.Add(ttl).UnixMilli(), hashToken(token))
	return err
}

func (s *Store) CompleteMFA(token string, ttl time.Duration) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE sessions SET mfa_pending = 0, expires_at = ?, last_seen = ? WHERE token_hash = ?`, now.Add(ttl).UnixMilli(), now.UnixMilli(), hashToken(token))
	return err
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// DeleteOtherSessions signs a user out everywhere except the given session.
func (s *Store) DeleteOtherSessions(userID int64, keepToken string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, hashToken(keepToken))
	return err
}

func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}
