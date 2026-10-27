package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hook/internal/pwa"
	"hook/internal/store"
)

func (s *Server) webpushKey(w http.ResponseWriter, r *http.Request, se *session) {
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": s.pusher.PublicKey()})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, se *session) {
	subs, err := s.store.PushSubscriptions()
	if err != nil {
		s.internal(w, err)
		return
	}
	for _, sub := range subs {
		if u, err := url.Parse(sub.Endpoint); err == nil {
			sub.Host = u.Host
		}
	}
	writeJSON(w, http.StatusOK, subs)
}

// addDevice stores a browser's push subscription (from PushManager.subscribe).
func (s *Server) addDevice(w http.ResponseWriter, r *http.Request, se *session) {
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(in.Endpoint) > 1000 || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		writeErr(w, http.StatusBadRequest, "that isn't a valid push subscription")
		return
	}
	label := strings.TrimSpace(in.Label)
	if len(label) > 80 {
		label = label[:80]
	}
	sub := &store.PushSubscription{UserID: se.user.ID, Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth, Label: label}
	if err := s.store.SavePushSubscription(sub); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": sub.ID})
}

func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeletePushSubscription(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "device not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	ok(w)
}

// forgetDevice removes this browser's subscription by endpoint, which the
// browser knows and the device list deliberately doesn't expose.
func (s *Server) forgetDevice(w http.ResponseWriter, r *http.Request, se *session) {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DeletePushEndpoint(in.Endpoint); err != nil {
		s.internal(w, err)
		return
	}
	ok(w)
}

func (s *Server) testPush(w http.ResponseWriter, r *http.Request, se *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	n, err := s.pusher.Send(ctx, pwa.Notification{Title: "✅ Hook test notification", Body: "Push notifications work on this device.", URL: "/settings", Tag: "test"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}
