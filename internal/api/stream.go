package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// stream pushes live events to the browser as Server-Sent Events.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, se *session) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	rc.Flush()

	ch, unsubscribe := s.events.Subscribe()
	defer unsubscribe()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	// The stream ends when its session does: re-checked whenever a session
	// may have been revoked through the API, and at every ping otherwise.
	gen := s.sessionsChanged.Load()
	valid := func() bool {
		cur, err := s.store.GetSession(se.token)
		return err == nil && !cur.MFAPending
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if g := s.sessionsChanged.Load(); g != gen {
				if gen = g; !valid() {
					return
				}
			}
			b, _ := json.Marshal(ev.Data)
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b); err != nil {
				return
			}
		case <-ping.C:
			if !valid() {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// spa serves the built frontend, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(s.static, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					// sw.js and the manifest must be revalidated, or updates never land.
					w.Header().Set("Cache-Control", "no-cache")
				}
				if strings.HasSuffix(p, ".webmanifest") {
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.static, "index.html")
		if err != nil {
			http.Error(w, "frontend not built: run `npm run build` in web/", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
