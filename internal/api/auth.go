package api

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hook/internal/auth"

	qrcode "github.com/skip2/go-qrcode"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers()
	if err != nil {
		s.internal(w, err)
		return
	}
	out := map[string]any{"setupRequired": n == 0, "authenticated": false, "mfaPending": false, "version": Version}
	if se, err := s.currentSession(r); err == nil {
		out["mfaPending"] = se.sess.MFAPending
		out["authenticated"] = !se.sess.MFAPending
		if !se.sess.MFAPending {
			out["user"] = se.user
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setup creates the first (admin) account. It only works on an empty database.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if n, err := s.store.CountUsers(); err != nil || n > 0 {
		writeErr(w, http.StatusForbidden, "setup already completed")
		return
	}
	var in credentials
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(in.Username) {
		writeErr(w, http.StatusBadRequest, "username must be 3–32 letters, digits, dots, dashes or underscores")
		return
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.internal(w, err)
		return
	}
	u, err := s.store.CreateUser(in.Username, hash)
	if err != nil {
		s.internal(w, err)
		return
	}
	token := newToken()
	if err := s.store.CreateSession(token, u.ID, false, sessionTTL, s.clientIP(r), r.UserAgent()); err != nil {
		s.internal(w, err)
		return
	}
	s.setCookie(w, token, sessionTTL)
	s.log.Info("admin account created", "user", u.Username)
	ok(w)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.loginLimit.Blocked(ip) {
		writeErr(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}
	var in credentials
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.store.UserByName(strings.TrimSpace(in.Username))
	if err != nil {
		auth.CheckDummy(in.Password)
		s.loginLimit.Fail(ip)
		writeErr(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}
	if !auth.CheckPassword(in.Password, u.PasswordHash) {
		s.loginLimit.Fail(ip)
		s.log.Warn("failed login", "user", u.Username, "ip", ip)
		writeErr(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}
	token := newToken()
	ttl := sessionTTL
	if u.TOTPEnabled {
		ttl = mfaTTL
	}
	if err := s.store.CreateSession(token, u.ID, u.TOTPEnabled, ttl, ip, r.UserAgent()); err != nil {
		s.internal(w, err)
		return
	}
	s.setCookie(w, token, ttl)
	if u.TOTPEnabled {
		writeJSON(w, http.StatusOK, map[string]bool{"mfaRequired": true})
		return
	}
	s.loginLimit.Reset(ip)
	ok(w)
}

// loginTOTP completes a password login that is waiting for a 2FA code.
func (s *Server) loginTOTP(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.loginLimit.Blocked(ip) {
		writeErr(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}
	se, err := s.currentSession(r)
	if err != nil || !se.sess.MFAPending {
		writeErr(w, http.StatusUnauthorized, "Sign-in expired, start again")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	step, valid := auth.VerifyTOTP(se.user.TOTPSecret, in.Code, time.Now())
	if valid {
		valid, err = s.store.UseTOTPStep(se.user.ID, step)
		if err != nil {
			s.internal(w, err)
			return
		}
	}
	if !valid {
		s.loginLimit.Fail(ip)
		writeErr(w, http.StatusUnauthorized, "Invalid code")
		return
	}
	if err := s.store.CompleteMFA(se.token, sessionTTL); err != nil {
		s.internal(w, err)
		return
	}
	s.loginLimit.Reset(ip)
	s.setCookie(w, se.token, sessionTTL)
	ok(w)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.store.DeleteSession(c.Value)
	}
	s.clearCookie(w)
	ok(w)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, se *session) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !auth.CheckPassword(in.Current, se.user.PasswordHash) {
		writeErr(w, http.StatusBadRequest, "Current password is wrong")
		return
	}
	if err := auth.ValidatePassword(in.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := s.store.SetPassword(se.user.ID, hash); err != nil {
		s.internal(w, err)
		return
	}
	s.store.DeleteOtherSessions(se.user.ID, se.token)
	ok(w)
}

// totpBegin creates a fresh secret and returns it with a QR code to scan.
func (s *Server) totpBegin(w http.ResponseWriter, r *http.Request, se *session) {
	if se.user.TOTPEnabled {
		writeErr(w, http.StatusBadRequest, "Two-factor authentication is already on")
		return
	}
	secret := auth.NewTOTPSecret()
	if err := s.store.SetTOTP(se.user.ID, secret, false); err != nil {
		s.internal(w, err)
		return
	}
	uri := auth.TOTPURI(secret, "Hook", se.user.Username)
	png, err := qrcode.Encode(uri, qrcode.Medium, 240)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": auth.TOTPSecretString(secret),
		"uri":    uri,
		"qr":     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request, se *session) {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if se.user.TOTPEnabled || se.user.TOTPSecret == nil {
		writeErr(w, http.StatusBadRequest, "Start two-factor setup first")
		return
	}
	step, valid := auth.VerifyTOTP(se.user.TOTPSecret, in.Code, time.Now())
	if !valid {
		writeErr(w, http.StatusBadRequest, "That code didn't match. Check your phone's clock and try the next code.")
		return
	}
	if err := s.store.SetTOTP(se.user.ID, se.user.TOTPSecret, true); err != nil {
		s.internal(w, err)
		return
	}
	s.store.UseTOTPStep(se.user.ID, step)
	s.store.DeleteOtherSessions(se.user.ID, se.token)
	ok(w)
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request, se *session) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !se.user.TOTPEnabled {
		writeErr(w, http.StatusBadRequest, "Two-factor authentication is off")
		return
	}
	if !auth.CheckPassword(in.Password, se.user.PasswordHash) {
		writeErr(w, http.StatusBadRequest, "Password is wrong")
		return
	}
	if _, valid := auth.VerifyTOTP(se.user.TOTPSecret, in.Code, time.Now()); !valid {
		writeErr(w, http.StatusBadRequest, "Invalid code")
		return
	}
	if err := s.store.SetTOTP(se.user.ID, nil, false); err != nil {
		s.internal(w, err)
		return
	}
	ok(w)
}
