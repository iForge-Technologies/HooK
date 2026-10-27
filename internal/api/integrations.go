package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"hook/internal/checks"
	"hook/internal/events"
	"hook/internal/platform"
	"hook/internal/store"
)

// ---- push monitors ----

// push records a result reported by a job. Public: the token is the credential.
//
//	curl -fsS https://hook.example.com/api/push/<token>
//	curl -fsS "https://hook.example.com/api/push/<token>?status=down&msg=backup+failed"
func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, err := s.store.MonitorByPushToken(r.PathValue("token"))
	if err != nil || m.Type != "push" {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "msg": "unknown push token"})
		return
	}
	q := r.URL.Query()
	status := strings.ToLower(q.Get("status"))
	res := checks.Result{OK: status == "" || status == "up" || status == "ok", Msg: strings.TrimSpace(q.Get("msg"))}
	if len(res.Msg) > 200 {
		res.Msg = res.Msg[:200]
	}
	if res.Msg == "" {
		res.Msg = "Push received"
		if !res.OK {
			res.Msg = "Job reported a failure"
		}
	}
	if ms, err := strconv.ParseFloat(q.Get("ping"), 64); err == nil && ms >= 0 && ms < 86_400_000 {
		res.Latency = time.Duration(ms * float64(time.Millisecond))
	}
	if !s.sched.Push(m.ID, res) {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "msg": "monitor is paused"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) rotatePushToken(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	m, err := s.store.Monitor(id)
	if err != nil || m.Type != "push" {
		writeErr(w, http.StatusNotFound, "push monitor not found")
		return
	}
	m.Config.PushToken = newToken()
	if err := s.store.UpdateMonitor(m, false); err != nil {
		s.internal(w, err)
		return
	}
	if err := s.store.SetPushToken(id, m.Config.PushToken); err != nil {
		s.internal(w, err)
		return
	}
	s.events.Publish(events.Event{Type: "monitors"})
	writeJSON(w, http.StatusOK, map[string]string{"token": m.Config.PushToken})
}

// ---- platform accounts ----

// checkIntegration makes sure a deploy monitor points at an account of its kind.
func (s *Server) checkIntegration(m *store.Monitor) error {
	if m.Type != "vercel" && m.Type != "render" {
		return nil
	}
	in, err := s.store.Integration(m.Config.IntegrationID)
	if err != nil || in.Kind != m.Type {
		return fmt.Errorf("connect a %s account first", m.Type)
	}
	return nil
}

func maskedIntegration(in *store.Integration) *store.Integration {
	out := *in
	out.Config = map[string]string{"teamId": in.Config["teamId"]}
	if in.Config["token"] != "" {
		out.Config["token"] = mask
	}
	return &out
}

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request, se *session) {
	ins, err := s.store.Integrations()
	if err != nil {
		s.internal(w, err)
		return
	}
	out := make([]*store.Integration, len(ins))
	for i, in := range ins {
		out[i] = maskedIntegration(in)
	}
	writeJSON(w, http.StatusOK, out)
}

// saveIntegration verifies the token against the platform before storing it.
func (s *Server) saveIntegration(w http.ResponseWriter, r *http.Request, se *session) {
	var in store.Integration
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.ID = 0
	if in.Config == nil {
		in.Config = map[string]string{}
	}
	if r.PathValue("id") != "" {
		id, valid := pathID(r)
		if !valid {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		old, err := s.store.Integration(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "account not found")
			return
		}
		in.ID, in.Kind = id, old.Kind
		if in.Config["token"] == mask || in.Config["token"] == "" {
			in.Config["token"] = old.Config["token"]
		}
	}
	if !slices.Contains(platform.Kinds, in.Kind) {
		writeErr(w, http.StatusBadRequest, "account type must be vercel or render")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		writeErr(w, http.StatusBadRequest, "name is required (max 60 characters)")
		return
	}
	token := strings.TrimSpace(in.Config["token"])
	if token == "" {
		writeErr(w, http.StatusBadRequest, "API token is required")
		return
	}
	in.Config = map[string]string{"token": token, "teamId": strings.TrimSpace(in.Config["teamId"])}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	n, err := platform.Verify(ctx, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Couldn't use that token: "+err.Error())
		return
	}
	if err := s.store.SaveIntegration(&in); err != nil {
		s.internal(w, err)
		return
	}
	platform.Forget(in.ID)
	writeJSON(w, http.StatusOK, map[string]any{"integration": maskedIntegration(&in), "projects": n})
}

func (s *Server) deleteIntegration(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteIntegration(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	platform.Forget(id)
	ok(w)
}

func (s *Server) integrationProjects(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	in, err := s.store.Integration(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ps, err := platform.Projects(ctx, in)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ps)
}
