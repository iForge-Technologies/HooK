package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// Regression tests for the 2026-10-27 security audit.

// Deleted monitors and servers used to stay listed on status pages and in
// maintenance windows, and SQLite gave their IDs to the next new row, which
// then got published or muted.
func TestDeletedIDsAreDroppedAndNeverReused(t *testing.T) {
	s := open(t)
	mon := func(name string) *Monitor {
		m := &Monitor{Name: name, Type: "http", Target: "https://example.com", Interval: 60, RetryInterval: 60, Timeout: 10, Active: true}
		if err := s.CreateMonitor(m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	host := func(name string) *Host {
		h := &Host{Name: name, Thresholds: DefaultThresholds()}
		if err := s.CreateHost(h, "hk_"+name); err != nil {
			t.Fatal(err)
		}
		return h
	}
	m1, m2 := mon("public"), mon("old")
	h1, h2 := host("a"), host("b")
	page := &StatusPage{Slug: "acme", Title: "Acme", MonitorIDs: []int64{m2.ID, m1.ID}}
	if err := s.SaveStatusPage(page); err != nil {
		t.Fatal(err)
	}
	win := &Maintenance{Title: "w", StartsAt: 0, EndsAt: time.Now().Add(time.Hour).UnixMilli(), MonitorIDs: []int64{m2.ID}, HostIDs: []int64{h2.ID, h1.ID}}
	if err := s.SaveMaintenance(win); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteMonitor(m2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHost(h2.ID); err != nil {
		t.Fatal(err)
	}
	m3, h3 := mon("new-private"), host("c")
	if m3.ID == m2.ID || h3.ID == h2.ID {
		t.Fatalf("deleted IDs were reused: monitor %d→%d, host %d→%d", m2.ID, m3.ID, h2.ID, h3.ID)
	}

	p, err := s.StatusPageBySlug("acme")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.MonitorIDs, []int64{m1.ID}) {
		t.Fatalf("status page still lists the deleted monitor: %v", p.MonitorIDs)
	}
	ws, _ := s.MaintenanceWindows()
	if len(ws) != 1 || len(ws[0].MonitorIDs) != 0 || !slices.Equal(ws[0].HostIDs, []int64{h1.ID}) {
		t.Fatalf("window still covers deleted members: %+v", ws[0])
	}
}

// An unknown kiosk token used to take SQLite's single writer lock and wait
// behind any running write.
func TestKioskUnknownTokenDoesNotWaitForWriter(t *testing.T) {
	s := open(t)
	tx, err := s.db.Begin() // _txlock=immediate: holds the writer lock
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES('busy', x'00')`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := s.KioskByToken("made-up"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("lookup waited %v for the writer", d)
	}
}

// Backups were written 0644 next to a 0600 key.
func TestBackupIsOwnerOnlyAndNeverOverwrites(t *testing.T) {
	s := open(t)
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	path := filepath.Join(dir, "hook.db")
	if err := s.Backup(path); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		t.Fatalf("backup missing or empty: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v, want 0600", st.Mode().Perm())
	}
	if err := s.Backup(path); err == nil {
		t.Fatal("overwrote an existing backup")
	}
}

func TestFailMFACountsPerSignIn(t *testing.T) {
	s := open(t)
	u, err := s.CreateUser("admin", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession("pending", u.ID, true, time.Minute, "", ""); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		if n, err := s.FailMFA("pending"); err != nil || n != want {
			t.Fatalf("FailMFA = %d, %v; want %d", n, err, want)
		}
	}
	if _, err := s.FailMFA("unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
}

// Resolved server events and incidents had no retention at all.
func TestPruneDropsOldResolvedEvents(t *testing.T) {
	s := open(t)
	h := &Host{Name: "h", Thresholds: DefaultThresholds()}
	if err := s.CreateHost(h, "hk_h"); err != nil {
		t.Fatal(err)
	}
	s.OpenHostEvent(h.ID, "container", "old", 1000, 0, "")
	s.ResolveHostEvent(h.ID, "container", "old", 2000)
	s.OpenHostEvent(h.ID, "container", "open", 1000, 0, "")
	if err := s.PruneHosts(0, 0, 0, 5000); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.HostEvents(h.ID, 10)
	if len(evs) != 1 || evs[0].Subject != "open" {
		t.Fatalf("want only the open event kept, got %+v", evs)
	}
}
