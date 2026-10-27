package fleet

import (
	"fmt"
	"strings"
	"time"

	"hook/internal/alert"
	"hook/internal/events"
	"hook/internal/proto"
	"hook/internal/store"
)

// crashRestarted means "crashed and came back between snapshots": running both
// times with a higher restart count. Starting a stopped container, or one
// recovering from an open alert, isn't a restart.
func crashRestarted(c proto.Container, wasRunning bool, prevRestarts int, known bool) bool {
	return known && wasRunning && c.State == "running" && c.Restarts > prevRestarts
}

// inspect turns a snapshot's containers and watched units into alerts. Open
// events live in the database, so service state survives hub restarts.
// Containers only alert on "stopped" after we've seen them running, so long-
// dead containers left on a server never page anyone.
func (f *Fleet) inspect(id int64, s *proto.Snapshot, at time.Time) {
	f.mu.Lock()
	l := f.live[id]
	if l == nil {
		f.mu.Unlock()
		return
	}
	h := *l.host
	wasRunning, prevRestarts := l.running, l.restarts
	if s.Docker != nil && s.Docker.Available {
		l.running, l.restarts = map[string]bool{}, map[string]int{}
		for _, c := range s.Docker.Containers {
			l.running[c.Name] = c.State == "running"
			l.restarts[c.Name] = c.Restarts
		}
	}
	f.mu.Unlock()

	open, err := f.store.OpenHostEvents()
	if err != nil {
		f.log.Error("load open events", "err", err)
		return
	}
	isOpen := map[string]store.HostEvent{}
	for _, e := range open {
		if e.HostID == id {
			isOpen[e.Kind+"/"+e.Subject] = e
		}
	}
	changed := false
	raise := func(kind, subject, msg string) {
		if opened, _ := f.store.OpenHostEvent(id, kind, subject, at.UnixMilli(), 0, msg); !opened {
			return
		}
		changed = true
		ev := alert.ServerEvent(kind, &h, at)
		ev.Subject, ev.Msg = subject, msg
		f.log.Warn("server problem", "server", h.Name, "kind", kind, "subject", subject, "detail", msg)
		f.alerts.Notify(ev)
	}
	clear := func(kind, okKind, subject string, notify bool) {
		started, _ := f.store.ResolveHostEvent(id, kind, subject, at.UnixMilli())
		if started == 0 {
			return
		}
		changed = true
		if notify {
			ev := alert.ServerEvent(okKind, &h, at)
			ev.Subject, ev.Downtime = subject, at.Sub(time.UnixMilli(started))
			f.alerts.Notify(ev)
		}
	}

	if s.Docker != nil && s.Docker.Available {
		present := map[string]bool{}
		for _, c := range s.Docker.Containers {
			present[c.Name] = true
			problem := ""
			switch {
			case c.State == "restarting":
				problem = "is crash-looping (" + c.Status + ")"
			case c.State == "running" && c.Health == "unhealthy":
				problem = "is unhealthy"
			case c.State != "running" && wasRunning[c.Name]:
				problem = "stopped (" + c.Status + ")"
			}
			_, open := isOpen[alert.KindContainer+"/"+c.Name]
			switch {
			case problem != "" && h.Thresholds.Containers:
				raise(alert.KindContainer, c.Name, problem)
			case problem == "" && open && c.State == "running":
				clear(alert.KindContainer, alert.KindContOK, c.Name, h.Thresholds.Containers)
			}
			prev, known := prevRestarts[c.Name]
			if problem == "" && h.Thresholds.Containers && crashRestarted(c, wasRunning[c.Name], prev, known) {
				ev := alert.ServerEvent(alert.KindRestart, &h, at)
				ev.Subject, ev.Msg = c.Name, fmt.Sprintf("%d restarts so far", c.Restarts)
				f.alerts.Notify(ev)
			}
		}
		for key := range isOpen { // removed containers: nothing left to watch
			if name, ok := strings.CutPrefix(key, alert.KindContainer+"/"); ok && !present[name] {
				clear(alert.KindContainer, "", name, false)
			}
		}
	}

	// "Watched" comes from the hub's settings, not the snapshot: an agent that
	// just restarted may report before it has received its unit list.
	watched := map[string]bool{}
	for _, u := range h.Thresholds.Units {
		watched[u] = true
	}
	for _, u := range s.Units {
		if !watched[u.Name] {
			continue
		}
		ok := u.Active == "active" || u.Active == "activating" || u.Active == "reloading"
		_, open := isOpen[alert.KindService+"/"+u.Name]
		switch {
		case !ok && h.Thresholds.Services:
			raise(alert.KindService, u.Name, u.Active+" ("+u.Sub+")")
		case ok && open:
			clear(alert.KindService, alert.KindServiceOK, u.Name, h.Thresholds.Services)
		}
	}
	for key := range isOpen { // no longer watched
		if name, ok := strings.CutPrefix(key, alert.KindService+"/"); ok && !watched[name] {
			clear(alert.KindService, "", name, false)
		}
	}
	if changed {
		f.events.Publish(events.Event{Type: "hosts"})
	}
}
