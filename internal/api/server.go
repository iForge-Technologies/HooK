// Package api serves the JSON API, the live event stream and the SPA.
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hook/internal/alert"
	"hook/internal/auth"
	"hook/internal/config"
	"hook/internal/events"
	"hook/internal/fleet"
	"hook/internal/health"
	"hook/internal/maint"
	"hook/internal/monitor"
	"hook/internal/pwa"
	"hook/internal/store"
)

const Version = "0.1.0"

type Server struct {
	cfg        config.Config
	store      *store.Store
	sched      *monitor.Scheduler
	fleet      *fleet.Fleet
	cal        *maint.Calendar
	health     *health.Monitor
	pusher     *pwa.Pusher
	alerts     *alert.Dispatcher
	events     *events.Broker
	log        *slog.Logger
	static     fs.FS
	loginLimit *auth.Limiter
	pushLimit  *auth.Limiter // one push per monitor per 10 s
	setupMu    sync.Mutex

	// sessionsChanged is bumped whenever sessions are revoked, so open event
	// streams re-check theirs (see stream).
	sessionsChanged atomic.Uint64
}

func New(cfg config.Config, st *store.Store, sched *monitor.Scheduler, fl *fleet.Fleet, cal *maint.Calendar, hm *health.Monitor, push *pwa.Pusher, al *alert.Dispatcher, ev *events.Broker, static fs.FS, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: st, sched: sched, fleet: fl, cal: cal, health: hm, pusher: push, alerts: al, events: ev, static: static, log: log,
		loginLimit: auth.NewLimiter(10, 10*time.Minute), pushLimit: auth.NewLimiter(1, 10*time.Second)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /api/system", s.authed(s.system))
	mux.Handle("GET /api/webpush/key", s.authed(s.webpushKey))
	mux.Handle("GET /api/webpush/devices", s.authed(s.listDevices))
	mux.Handle("POST /api/webpush/devices", s.authed(s.addDevice))
	mux.Handle("DELETE /api/webpush/devices/{id}", s.authed(s.removeDevice))
	mux.Handle("POST /api/webpush/forget", s.authed(s.forgetDevice))
	mux.Handle("POST /api/webpush/test", s.authed(s.testPush))
	mux.Handle("GET /api/kiosk-links", s.authed(s.listKioskLinks))
	mux.Handle("POST /api/kiosk-links", s.authed(s.createKioskLink))
	mux.Handle("DELETE /api/kiosk-links/{id}", s.authed(s.deleteKioskLink))

	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/setup", s.setup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/totp", s.loginTOTP)
	mux.HandleFunc("POST /api/auth/logout", s.logout)

	mux.Handle("POST /api/account/password", s.authed(s.changePassword))
	mux.Handle("POST /api/account/totp/begin", s.authed(s.totpBegin))
	mux.Handle("POST /api/account/totp/enable", s.authed(s.totpEnable))
	mux.Handle("POST /api/account/totp/disable", s.authed(s.totpDisable))

	mux.Handle("GET /api/meta", s.authed(s.meta))
	mux.Handle("GET /api/events", s.authed(s.stream))

	mux.Handle("GET /api/monitors", s.authed(s.listMonitors))
	mux.Handle("POST /api/monitors", s.authed(s.createMonitor))
	mux.Handle("GET /api/monitors/{id}", s.authed(s.getMonitor))
	mux.Handle("PUT /api/monitors/{id}", s.authed(s.updateMonitor))
	mux.Handle("DELETE /api/monitors/{id}", s.authed(s.deleteMonitor))
	mux.Handle("POST /api/monitors/{id}/pause", s.authed(s.pauseMonitor))
	mux.Handle("POST /api/monitors/{id}/resume", s.authed(s.resumeMonitor))
	mux.Handle("POST /api/monitors/{id}/check", s.authed(s.checkMonitor))
	mux.Handle("GET /api/monitors/{id}/series", s.authed(s.monitorSeries))
	mux.Handle("POST /api/monitors/{id}/push-token", s.authed(s.rotatePushToken))
	mux.HandleFunc("/api/push/{token}", s.push)

	mux.Handle("GET /api/maintenance", s.authed(s.listMaintenance))
	mux.Handle("POST /api/maintenance", s.authed(s.saveMaintenance))
	mux.Handle("PUT /api/maintenance/{id}", s.authed(s.saveMaintenance))
	mux.Handle("DELETE /api/maintenance/{id}", s.authed(s.deleteMaintenance))

	mux.Handle("GET /api/status-pages", s.authed(s.listStatusPages))
	mux.Handle("POST /api/status-pages", s.authed(s.saveStatusPage))
	mux.Handle("PUT /api/status-pages/{id}", s.authed(s.saveStatusPage))
	mux.Handle("DELETE /api/status-pages/{id}", s.authed(s.deleteStatusPage))
	mux.HandleFunc("GET /api/public/status/{slug}", s.publicStatus)
	mux.HandleFunc("GET /api/kiosk/{token}", s.kiosk)

	mux.Handle("GET /api/integrations", s.authed(s.listIntegrations))
	mux.Handle("POST /api/integrations", s.authed(s.saveIntegration))
	mux.Handle("PUT /api/integrations/{id}", s.authed(s.saveIntegration))
	mux.Handle("DELETE /api/integrations/{id}", s.authed(s.deleteIntegration))
	mux.Handle("GET /api/integrations/{id}/projects", s.authed(s.integrationProjects))

	mux.Handle("GET /api/channels", s.authed(s.listChannels))
	mux.Handle("POST /api/channels", s.authed(s.saveChannel))
	mux.Handle("PUT /api/channels/{id}", s.authed(s.saveChannel))
	mux.Handle("DELETE /api/channels/{id}", s.authed(s.deleteChannel))
	mux.Handle("POST /api/channels/test", s.authed(s.testChannel))

	mux.Handle("GET /api/overview", s.authed(s.overview))
	mux.Handle("GET /api/servers", s.authed(s.listServers))
	mux.Handle("POST /api/servers", s.authed(s.createServer))
	mux.Handle("GET /api/servers/{id}", s.authed(s.getServer))
	mux.Handle("PUT /api/servers/{id}", s.authed(s.updateServer))
	mux.Handle("DELETE /api/servers/{id}", s.authed(s.deleteServer))
	mux.Handle("POST /api/servers/{id}/token", s.authed(s.rotateToken))
	mux.Handle("GET /api/servers/{id}/series", s.authed(s.serverSeries))
	mux.Handle("GET /api/servers/{id}/network", s.authed(s.serverNetwork))
	mux.Handle("POST /api/servers/{id}/speedtest", s.authed(s.runSpeedTest))

	// Agents authenticate with their own token, not a session.
	mux.HandleFunc("GET /api/agent/ws", s.agentWS)
	mux.HandleFunc("GET /install.sh", s.serveInstall)
	mux.HandleFunc("GET /download/{file}", s.serveAgent)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "not found") })
	mux.Handle("/", s.spa())
	return s.secure(mux)
}

// secure adds security headers and blocks cross-site writes. Together with the
// SameSite=Strict cookie, requiring a JSON body forces a CORS preflight that
// other origins can't pass.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		// Push URLs are called by cron jobs with plain curl; they carry their own
		// token and no cookies, so the JSON-only guard doesn't apply.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasPrefix(r.URL.Path, "/api/push/") {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, "expected application/json")
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					writeErr(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

// ---- sessions ----

const (
	cookieName = "hook_session"
	sessionTTL = 30 * 24 * time.Hour
	mfaTTL     = 5 * time.Minute
)

type ctxKey struct{}

type session struct {
	token string
	user  *store.User
	sess  *store.Session
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) setCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
}

// currentSession returns the cookie's session, including MFA-pending ones.
func (s *Server) currentSession(r *http.Request) (*session, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil, store.ErrNotFound
	}
	se, err := s.store.GetSession(c.Value)
	if err != nil {
		return nil, err
	}
	u, err := s.store.UserByID(se.UserID)
	if err != nil {
		return nil, err
	}
	return &session{token: c.Value, user: u, sess: se}, nil
}

func (s *Server) authed(h func(http.ResponseWriter, *http.Request, *session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		se, err := s.currentSession(r)
		if err != nil || se.sess.MFAPending {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		if time.Since(se.sess.LastSeen) > time.Hour {
			s.store.TouchSession(se.token, sessionTTL)
			s.setCookie(w, se.token, sessionTTL)
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, se)), se)
	})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1]) // the hop our proxy saw
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- helpers ----

// writeJSON encodes before writing the status, so a value that can't be
// encoded becomes a 500 instead of an empty 200.
func writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("encode response", "err", err)
		code, body = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	w.Write(append(body, '\n'))
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func (s *Server) internal(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }
