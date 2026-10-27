package api

import (
	"net/http"
	"time"
)

// healthz is the public self-check for load balancers and external monitors:
// 200 when the database writes, the scheduler is moving and the data disk has
// room; 503 otherwise. It reuses a check from the last 10 seconds so it can't
// be used to hammer the database.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	rep, _ := s.health.Last()
	if time.Since(time.UnixMilli(rep.Checked)) > 10*time.Second {
		rep = s.health.Check()
	}
	code := http.StatusOK
	if !rep.OK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ok": rep.OK, "checks": rep.Checks, "version": Version})
}

// system is the Settings page's view of the hub itself.
func (s *Server) system(w http.ResponseWriter, r *http.Request, se *session) {
	rep, dm := s.health.Last()
	if rep.Checked == 0 {
		rep = s.health.Check()
	}
	keyFromEnv := s.cfg.Secret != ""
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    Version,
		"health":     rep,
		"deadman":    dm,
		"dbBytes":    s.store.Size(s.cfg.DataDir),
		"dataDir":    s.cfg.DataDir,
		"keyFromEnv": keyFromEnv,
		"publicUrl":  s.cfg.PublicURL,
	})
}
