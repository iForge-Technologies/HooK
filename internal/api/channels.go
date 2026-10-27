package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hook/internal/alert"
	"hook/internal/store"
)

func maskedChannel(c *store.Channel) *store.Channel {
	out := *c
	out.Config = map[string]string{}
	for k, v := range c.Config {
		out.Config[k] = v
	}
	for _, f := range alert.Types[c.Type].Secret {
		if out.Config[f] != "" {
			out.Config[f] = mask
		}
	}
	return &out
}

// unmask restores secret fields the browser sent back as the mask. A stored
// secret only goes where it was entered for: if a destination field changed,
// it has to be typed in again, so a session holder can't redirect it to a
// server of their own and read it there.
func unmask(cfg map[string]string, kind string, old *store.Channel) error {
	spec := alert.Types[kind]
	for _, f := range spec.Secret {
		if cfg[f] != mask {
			continue
		}
		if old == nil || old.Type != kind {
			cfg[f] = ""
			continue
		}
		for _, d := range spec.Dest {
			if strings.TrimSpace(cfg[d]) != old.Config[d] {
				return fmt.Errorf("re-enter the %s: the %s changed", f, d)
			}
		}
		cfg[f] = old.Config[f]
	}
	return nil
}

func cleanConfig(cfg map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cfg {
		if v = strings.TrimSpace(v); v != "" && len(k) <= 40 && len(v) <= 2000 {
			out[k] = v
		}
	}
	return out
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request, se *session) {
	cs, err := s.store.Channels()
	if err != nil {
		s.internal(w, err)
		return
	}
	out := make([]*store.Channel, len(cs))
	for i, c := range cs {
		out[i] = maskedChannel(c)
	}
	writeJSON(w, http.StatusOK, out)
}

type channelInput struct {
	store.Channel
	ApplyToAll bool `json:"applyToAll"`
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request, se *session) {
	var in channelInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var old *store.Channel
	if r.PathValue("id") != "" {
		id, valid := pathID(r)
		if !valid {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		var err error
		if old, err = s.store.Channel(id); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "channel not found")
			return
		} else if err != nil {
			s.internal(w, err)
			return
		}
		in.ID = id
	} else {
		in.ID = 0
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		writeErr(w, http.StatusBadRequest, "name is required (max 60 characters)")
		return
	}
	if in.Config == nil {
		in.Config = map[string]string{}
	}
	if err := unmask(in.Config, in.Type, old); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Config = cleanConfig(in.Config)
	if err := alert.Validate(in.Type, in.Config); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ch := in.Channel
	if err := s.store.SaveChannel(&ch); err != nil {
		s.internal(w, err)
		return
	}
	if in.ApplyToAll {
		if err := s.store.ApplyChannelToAll(ch.ID); err != nil {
			s.internal(w, err)
			return
		}
		if err := s.store.ApplyChannelToAllHosts(ch.ID); err != nil {
			s.internal(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, maskedChannel(&ch))
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request, se *session) {
	id, valid := pathID(r)
	if !valid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteChannel(id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	ok(w)
}

// testChannel sends a test message with the posted (possibly unsaved) settings.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request, se *session) {
	var in store.Channel
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var old *store.Channel
	if in.ID != 0 {
		old, _ = s.store.Channel(in.ID)
	}
	if in.Config == nil {
		in.Config = map[string]string{}
	}
	if err := unmask(in.Config, in.Type, old); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Config = cleanConfig(in.Config)
	if err := alert.Validate(in.Type, in.Config); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := s.alerts.Test(ctx, &in); err != nil {
		writeErr(w, http.StatusBadGateway, "Delivery failed: "+err.Error())
		return
	}
	ok(w)
}
