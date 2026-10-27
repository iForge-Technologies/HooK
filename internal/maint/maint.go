// Package maint keeps the maintenance windows in memory so every check and
// alert can ask "is this in maintenance right now?" without a query.
package maint

import (
	"log/slog"
	"sync"
	"time"

	"hook/internal/store"
)

type Calendar struct {
	st  *store.Store
	log *slog.Logger

	mu      sync.RWMutex
	windows []*store.Maintenance
}

func New(st *store.Store, log *slog.Logger) *Calendar {
	c := &Calendar{st: st, log: log}
	c.Reload()
	return c
}

// Reload re-reads the windows; call it after any change.
func (c *Calendar) Reload() {
	ws, err := c.st.MaintenanceWindows()
	if err != nil {
		c.log.Error("load maintenance windows", "err", err)
		return
	}
	c.mu.Lock()
	c.windows = ws
	c.mu.Unlock()
}

// Monitor returns the active window covering a monitor at t, if any.
func (c *Calendar) Monitor(id int64, t time.Time) *store.Maintenance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, w := range c.windows {
		if w.CoversMonitor(id) && w.ActiveAt(t) {
			return w
		}
	}
	return nil
}

// Host returns the active window covering a server at t, if any.
func (c *Calendar) Host(id int64, t time.Time) *store.Maintenance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, w := range c.windows {
		if w.CoversHost(id) && w.ActiveAt(t) {
			return w
		}
	}
	return nil
}

// Muted reports whether alerts for a monitor or server are silenced now.
func (c *Calendar) Muted(source string, id int64) bool {
	now := time.Now()
	if source == "server" {
		return c.Host(id, now) != nil
	}
	return c.Monitor(id, now) != nil
}
