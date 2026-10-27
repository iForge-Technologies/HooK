// Command agent runs on each VPS and streams its metrics to the Hook hub.
//
// It only dials out (no listening port), only reports, and never executes
// anything the hub sends. Frames collected while the hub is unreachable are
// buffered (up to one hour) and delivered on reconnect.
//
//	HOOK_URL=https://hook.example.com HOOK_TOKEN=hk_… hook-agent
//	hook-agent -once      print one metrics frame and snapshot, then exit
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"hook/internal/collect"
	"hook/internal/probe"
	"hook/internal/proto"
)

const version = "0.1.0"

func main() {
	hubURL := flag.String("url", os.Getenv("HOOK_URL"), "hub base URL (env HOOK_URL)")
	token := flag.String("token", os.Getenv("HOOK_TOKEN"), "agent token (env HOOK_TOKEN)")
	once := flag.Bool("once", false, "print one metrics frame and snapshot as JSON, then exit")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *showVersion {
		fmt.Println(version)
		return
	}
	c := collect.New(version)
	if *once {
		units := strings.Fields(strings.ReplaceAll(os.Getenv("HOOK_UNITS"), ",", " "))
		c.Metrics(ctx)
		c.Snapshot(ctx, units)
		time.Sleep(time.Second) // rates and CPU % need two samples
		out := map[string]any{"info": c.Info(ctx), "snapshot": c.Snapshot(ctx, units), "metrics": c.Metrics(ctx)}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return
	}

	wsURL, err := websocketURL(*hubURL)
	if err != nil || *token == "" {
		fmt.Fprintln(os.Stderr, "set HOOK_URL (https://your-hub) and HOOK_TOKEN")
		os.Exit(2)
	}
	a := &agent{c: c, log: log, url: wsURL, token: *token, probes: envOn("HOOK_PROBES")}
	a.probeSlots = make(chan struct{}, 4)
	a.interval.Store(5)
	a.snapInterval.Store(30)
	log.Info("hook agent starting", "version", version, "hub", *hubURL, "probes", a.probes)
	go a.collectLoop(ctx)
	a.connectLoop(ctx)
}

func envOn(k string) bool {
	switch strings.ToLower(os.Getenv(k)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func websocketURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" {
		return "", errors.New("invalid hub URL")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", errors.New("hub URL must be http(s)")
	}
	u.Path += "/api/agent/ws"
	return u.String(), nil
}

type agent struct {
	c     *collect.Collector
	log   *slog.Logger
	url   string
	token string

	interval     atomic.Int64 // seconds
	snapInterval atomic.Int64
	units        atomic.Pointer[[]string] // systemd units the hub wants reported
	probes       bool                     // HOOK_PROBES: run confirmation checks for the hub
	probeSlots   chan struct{}
	sendMu       sync.Mutex // probes reply from their own goroutines

	mu     sync.Mutex
	queue  []proto.Metrics
	collMu sync.Mutex // Collector isn't safe for concurrent Metrics/Snapshot
	ready  chan struct{}
}

const maxQueue = 720 // one hour at 5 s

// collectLoop samples metrics forever, connected or not.
func (a *agent) collectLoop(ctx context.Context) {
	a.collMu.Lock()
	a.c.Metrics(ctx) // prime counters so the first real frame has rates
	a.c.Snapshot(ctx, nil)
	a.collMu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(a.interval.Load()) * time.Second):
		}
		a.collMu.Lock()
		m := a.c.Metrics(ctx)
		a.collMu.Unlock()
		a.mu.Lock()
		a.queue = append(a.queue, m)
		if len(a.queue) > maxQueue {
			a.queue = a.queue[len(a.queue)-maxQueue:]
		}
		ready := a.ready
		a.mu.Unlock()
		if ready != nil {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
}

func (a *agent) connectLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // it was a healthy session; retry fast
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff)/2+1))
		a.log.Warn("disconnected from hub", "err", err, "retry_in", wait.Round(time.Second))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// session runs one WebSocket connection until it fails.
func (a *agent) session(parent context.Context) error {
	dialCtx, cancel := context.WithTimeout(parent, 20*time.Second)
	conn, resp, err := websocket.Dial(dialCtx, a.url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + a.token}, "User-Agent": {"hook-agent/" + version}},
	})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errors.New("hub rejected the token (regenerated or server deleted?)")
		}
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 10)

	ctx, cancelSession := context.WithCancel(parent)
	defer cancelSession()
	ready := make(chan struct{}, 1)
	a.mu.Lock()
	a.ready = ready
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.ready = nil
		a.mu.Unlock()
	}()

	send := func(ctx context.Context, conn *websocket.Conn, typ string, data any) error {
		a.sendMu.Lock()
		defer a.sendMu.Unlock()
		return send(ctx, conn, typ, data)
	}
	info := a.c.Info(ctx)
	info.Probes = a.probes
	if err := send(ctx, conn, proto.TypeHello, info); err != nil {
		return err
	}
	a.log.Info("connected to hub")

	// Reader: only config messages are understood; anything else is ignored.
	errc := make(chan error, 1)
	refresh := make(chan struct{}, 1)
	go func() {
		for {
			var env struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if err := wsjson.Read(ctx, conn, &env); err != nil {
				errc <- err
				return
			}
			if env.Type == proto.TypeProbe {
				var req proto.ProbeRequest
				if json.Unmarshal(env.Data, &req) == nil {
					go a.runProbe(ctx, conn, req, send)
				}
				continue
			}
			if env.Type == proto.TypeConfig {
				var cfg proto.Config
				if json.Unmarshal(env.Data, &cfg) == nil {
					if cfg.Interval >= 2 && cfg.Interval <= 300 {
						a.interval.Store(int64(cfg.Interval))
					}
					if cfg.SnapshotInterval >= 10 && cfg.SnapshotInterval <= 3600 {
						a.snapInterval.Store(int64(cfg.SnapshotInterval))
					}
					units := slices.DeleteFunc(slices.Clone(cfg.Units), func(u string) bool { return !collect.UnitName.MatchString(u) })
					a.units.Store(&units)
					select { // report the new units right away
					case refresh <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	sendSnapshot := func() error {
		var watch []string
		if u := a.units.Load(); u != nil {
			watch = *u
		}
		a.collMu.Lock()
		s := a.c.Snapshot(ctx, watch)
		a.collMu.Unlock()
		return send(ctx, conn, proto.TypeSnapshot, s)
	}
	if err := sendSnapshot(); err != nil {
		return err
	}
	snapTimer := time.NewTimer(time.Duration(a.snapInterval.Load()) * time.Second)
	defer snapTimer.Stop()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	select { // flush anything buffered while offline
	case ready <- struct{}{}:
	default:
	}

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "agent stopping")
			return ctx.Err()
		case err := <-errc:
			return err
		case <-ready:
			if err := a.flush(ctx, conn); err != nil {
				return err
			}
		case <-snapTimer.C:
			if err := sendSnapshot(); err != nil {
				return err
			}
			snapTimer.Reset(time.Duration(a.snapInterval.Load()) * time.Second)
		case <-refresh:
			if err := sendSnapshot(); err != nil {
				return err
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return fmt.Errorf("ping: %w", err)
			}
		}
	}
}

// runProbe answers a confirmation check, unless probes are off on this server.
// Only network check types run, and only against public addresses.
func (a *agent) runProbe(ctx context.Context, conn *websocket.Conn, req proto.ProbeRequest, send func(context.Context, *websocket.Conn, string, any) error) {
	res := proto.ProbeResult{ID: req.ID}
	switch {
	case !a.probes:
		res.Msg, res.Skipped = "probes are disabled on this server", true
	case !slices.Contains(probe.Types, req.Spec.Type):
		res.Msg, res.Skipped = "unsupported probe type", true
	default:
		select {
		case a.probeSlots <- struct{}{}:
			defer func() { <-a.probeSlots }()
			req.Spec.Timeout = max(1, min(req.Spec.Timeout, 30))
			r := probe.RunPublicOnly(ctx, req.Spec.ForRemote())
			res.OK, res.Latency, res.Code, res.Msg = r.OK, int(r.Latency.Milliseconds()), r.Code, r.Msg
			res.Skipped = probe.WasRefused(r)
		default:
			res.Msg, res.Skipped = "too many probes in flight", true
		}
	}
	send(ctx, conn, proto.TypeProbeResult, res)
}

// flush sends queued frames oldest first, dropping each only once it's written.
func (a *agent) flush(ctx context.Context, conn *websocket.Conn) error {
	for {
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			return nil
		}
		m := a.queue[0]
		a.mu.Unlock()
		if err := send(ctx, conn, proto.TypeMetrics, m); err != nil {
			return err
		}
		a.mu.Lock()
		if len(a.queue) > 0 && a.queue[0].TS == m.TS {
			a.queue = a.queue[1:]
		}
		a.mu.Unlock()
	}
}

func send(ctx context.Context, conn *websocket.Conn, typ string, data any) error {
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return wsjson.Write(wctx, conn, proto.Envelope{Type: typ, Data: data})
}
