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

const (
	// maxContainers and maxUnits are far above any real server; the rest of a
	// longer list is ignored.
	maxContainers = 500
	maxUnits      = 1000

	// Each server may open eventBudget container or service problems (or send
	// that many restart alerts) per eventWindow. Past that, one summary event
	// and alert stand in for the rest, so an agent inventing container names
	// can't flood alert channels or the database.
	eventBudget = 20
	eventWindow = 10 * time.Minute
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
	if s.Docker != nil && len(s.Docker.Containers) > maxContainers {
		s.Docker.Containers = s.Docker.Containers[:maxContainers]
	}
	if len(s.Units) > maxUnits {
		s.Units = s.Units[:maxUnits]
	}
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
	suppressed := 0
	// take spends one unit of the server's alert budget.
	take := func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if at.Sub(l.budgetStart) > eventWindow {
			l.budgetStart, l.budgetUsed, l.summarized = at, 0, false
		}
		if l.budgetUsed >= eventBudget {
			suppressed++
			return false
		}
		l.budgetUsed++
		return true
	}
	raise := func(kind, subject, msg string) {
		if _, open := isOpen[kind+"/"+subject]; open || !take() {
			return // already open, or over budget: no new row and no alert
		}
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
			if problem == "" && h.Thresholds.Containers && crashRestarted(c, wasRunning[c.Name], prev, known) && take() {
				ev := alert.ServerEvent(alert.KindRestart, &h, at)
				ev.Subject, ev.Msg = c.Name, fmt.Sprintf("%d restarts so far", c.Restarts)
				f.alerts.Notify(ev)
			}
		}
		for key := range isOpen { // removed containers: nothing left to watch
			if name, ok := strings.CutPrefix(key, alert.KindContainer+"/"); ok && !present[name] && name != summarySubject {
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
	if suppressed > 0 {
		f.mu.Lock()
		first := !l.summarized
		l.summarized = true
		f.mu.Unlock()
		msg := fmt.Sprintf("%d more container or service problems; alerts paused for this server for up to %s", suppressed, alert.HumanDuration(eventWindow))
		if first {
			if opened, _ := f.store.OpenHostEvent(id, alert.KindContainer, summarySubject, at.UnixMilli(), 0, msg); opened {
				changed = true
				ev := alert.ServerEvent(alert.KindContainer, &h, at)
				ev.Subject, ev.Msg = summarySubject, msg
				f.log.Warn("server problem alerts suppressed", "server", h.Name, "count", suppressed)
				f.alerts.Notify(ev)
			}
		}
	} else if _, open := isOpen[alert.KindContainer+"/"+summarySubject]; open {
		clear(alert.KindContainer, "", summarySubject, false)
	}
	if changed {
		f.events.Publish(events.Event{Type: "hosts"})
	}
}

// summarySubject names the one event that stands in for suppressed problems.
const summarySubject = "(many)"
