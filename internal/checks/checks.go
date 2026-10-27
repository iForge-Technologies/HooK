// Package checks runs a single probe of a monitor and reports the outcome.
// Network checks live in package probe (shared with the agent); database
// checks live here because only the hub carries the drivers.
package checks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"hook/internal/probe"
	"hook/internal/store"
)

const UserAgent = probe.UserAgent

// Types are every monitor type. push, vercel and render aren't probed by Run:
// push waits for the job to call in, and the platforms need an account token.
var Types = []string{"http", "keyword", "tcp", "dns", "tls", "postgres", "mysql", "redis", "mongodb", "push", "vercel", "render"}

var DNSRecords = probe.DNSRecords

type Result = probe.Result

var ValidStatusSpec = probe.ValidStatusSpec

// IsNetwork reports whether a type can also be checked from other locations.
func IsNetwork(t string) bool { return slices.Contains(probe.Types, t) }

// SpecFor converts a monitor into a network probe spec.
func SpecFor(m *store.Monitor) probe.Spec {
	c := m.Config
	return probe.Spec{
		Type: m.Type, Target: m.Target, Timeout: m.Timeout,
		Method: c.Method, Headers: c.Headers, Body: c.Body, ExpectedStatus: c.ExpectedStatus,
		FollowRedirects: c.FollowRedirects, IgnoreTLS: c.IgnoreTLS, Keyword: c.Keyword, KeywordInvert: c.KeywordInvert,
		DNSRecord: c.DNSRecord, DNSResolver: c.DNSResolver, DNSExpected: c.DNSExpected,
	}
}

// Run checks a monitor once. Drivers parse whatever the target sends back, so
// a panic inside one becomes a failed result rather than a crash of the hub.
func Run(ctx context.Context, m *store.Monitor) (res Result) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("check %d (%s) panicked: %v", m.ID, m.Type, p)
			res = Result{Msg: "Check failed: malformed reply from the target"}
		}
	}()
	if IsNetwork(m.Type) {
		return probe.Run(ctx, SpecFor(m))
	}
	timeout := time.Duration(m.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var r Result
	switch m.Type {
	case "postgres":
		r = checkPostgres(ctx, m)
	case "mysql":
		r = checkMySQL(ctx, m)
	case "redis":
		r = checkRedis(ctx, m)
	case "mongodb":
		r = checkMongo(ctx, m)
	default:
		r = Result{Msg: "unknown monitor type " + m.Type}
	}
	if !r.OK && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.Msg = fmt.Sprintf("Timed out after %ds", m.Timeout)
	}
	return probe.Clip(r)
}

func errMsg(err error) string { return probe.ErrMsg(err) }
