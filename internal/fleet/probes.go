package fleet

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"math/rand/v2"
	"time"

	"hook/internal/probe"
	"hook/internal/proto"
)

// Outcome is one remote location's answer to a confirmation probe.
type Outcome struct {
	Location string
	Skipped  bool // the location declined; it neither confirms nor contradicts
	probe.Result
}

type pendingProbe struct {
	hostID   int64 // only this server's answer counts
	location string
	ch       chan Outcome
}

// isLocation reports whether a server confirms failures: the admin enabled it
// as a location and its agent runs probes. An agent can't opt itself in.
func isLocation(l *live) bool { return l.host.ProbeLocation && l.host.Info.Probes }

// Locations lists the names of connected servers that run probes.
func (f *Fleet) Locations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id := range f.conns {
		if l := f.live[id]; l != nil && isLocation(l) {
			out = append(out, l.host.Name)
		}
	}
	return out
}

// Probe asks up to n probe-enabled agents to run spec and waits for their
// answers (or ctx). Headers and bodies never leave the hub.
func (f *Fleet) Probe(ctx context.Context, spec probe.Spec, n int) []Outcome {
	spec = spec.ForRemote()
	type target struct {
		c    *agentConn
		id   int64
		name string
	}
	f.mu.Lock()
	var ts []target
	for id, c := range f.conns {
		if l := f.live[id]; l != nil && isLocation(l) && l.host.Status == "online" {
			ts = append(ts, target{c, id, l.host.Name})
		}
	}
	f.mu.Unlock()
	rand.Shuffle(len(ts), func(i, j int) { ts[i], ts[j] = ts[j], ts[i] })
	ts = ts[:min(n, len(ts))]
	if len(ts) == 0 {
		return nil
	}

	wait, cancel := context.WithTimeout(ctx, time.Duration(spec.Timeout+5)*time.Second)
	defer cancel()
	chans := make([]chan Outcome, len(ts))
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = probeID()
		chans[i] = make(chan Outcome, 1)
		f.mu.Lock()
		f.pending[ids[i]] = pendingProbe{hostID: t.id, location: t.name, ch: chans[i]}
		f.mu.Unlock()
		if err := t.c.send(proto.TypeProbe, proto.ProbeRequest{ID: ids[i], Spec: spec}); err != nil {
			chans[i] <- Outcome{Location: t.name, Skipped: true}
		}
	}
	defer func() {
		f.mu.Lock()
		for _, id := range ids {
			delete(f.pending, id)
		}
		f.mu.Unlock()
	}()

	var out []Outcome
	for _, ch := range chans {
		select {
		case o := <-ch:
			if !o.Skipped {
				out = append(out, o)
			}
		case <-wait.Done():
			return out // locations that didn't answer in time don't count either way
		}
	}
	return out
}

// deliver hands an agent's answer to the waiting Probe call. Only the server
// the probe was sent to can answer it.
func (f *Fleet) deliver(hostID int64, r proto.ProbeResult) {
	f.mu.Lock()
	p, ok := f.pending[r.ID]
	if ok && p.hostID != hostID {
		ok = false
	} else {
		delete(f.pending, r.ID)
	}
	f.mu.Unlock()
	if !ok {
		return
	}
	p.ch <- Outcome{Location: p.location, Skipped: r.Skipped, Result: probe.Result{OK: r.OK, Latency: time.Duration(r.Latency) * time.Millisecond, Code: r.Code, Msg: r.Msg}}
}

func probeID() string {
	b := make([]byte, 12)
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
