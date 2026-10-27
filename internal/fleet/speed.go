package fleet

import (
	"context"
	"errors"
	"time"

	"hook/internal/events"
	"hook/internal/proto"
	"hook/internal/store"
)

// ErrNotConnected means the server's agent isn't online to run a test.
var ErrNotConnected = errors.New("the agent isn't connected")

// SpeedTest asks a server's agent to measure its bandwidth and stores the
// result. It takes up to about 20 seconds.
func (f *Fleet) SpeedTest(ctx context.Context, hostID int64, scheduled bool) (*store.SpeedTest, error) {
	f.mu.Lock()
	c := f.conns[hostID]
	l := f.live[hostID]
	f.mu.Unlock()
	if c == nil || l == nil {
		return nil, ErrNotConnected
	}
	if l.host.Info.NoSpeedTest {
		return nil, errors.New("speed tests are turned off on this server (HOOK_SPEEDTEST=0)")
	}
	id := probeID()
	ch := make(chan proto.SpeedResult, 1)
	f.mu.Lock()
	f.speeds[id] = ch
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.speeds, id)
		f.mu.Unlock()
	}()
	if err := c.send(proto.TypeSpeedTest, proto.SpeedTestRequest{ID: id}); err != nil {
		return nil, ErrNotConnected
	}
	wait, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	select {
	case r := <-ch:
		if r.Error != "" && r.DownBps == 0 && scheduled {
			// A refused scheduled test (too soon, busy) isn't worth a row.
			return nil, errors.New(r.Error)
		}
		t, err := f.store.SaveSpeedTest(hostID, f.now().UnixMilli(), r, scheduled)
		if err != nil {
			return nil, err
		}
		f.events.Publish(events.Event{Type: "speedTest", Data: map[string]any{"hostId": hostID}})
		return t, nil
	case <-wait.Done():
		return nil, errors.New("the agent didn't answer in time")
	}
}

func (f *Fleet) deliverSpeed(r proto.SpeedResult) {
	f.mu.Lock()
	ch, ok := f.speeds[r.ID]
	f.mu.Unlock()
	if ok {
		select {
		case ch <- r:
		default:
		}
	}
}

// RunSpeedTests runs scheduled tests for servers that asked for them, one
// server at a time so tests don't compete for the same uplink.
func (f *Fleet) RunSpeedTests(ctx context.Context) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		last, err := f.store.LastSpeedTests()
		if err != nil {
			f.log.Error("speed test schedule", "err", err)
			continue
		}
		var due []store.Host
		now := f.now()
		f.mu.Lock()
		for id, l := range f.live {
			every := l.host.Thresholds.SpeedTestHours
			if every <= 0 || l.host.Status != "online" || f.conns[id] == nil || l.host.Info.NoSpeedTest {
				continue
			}
			if now.Sub(time.UnixMilli(last[id])) >= time.Duration(every)*time.Hour {
				due = append(due, *l.host)
			}
		}
		f.mu.Unlock()
		for _, h := range due {
			if _, err := f.SpeedTest(ctx, h.ID, true); err != nil {
				f.log.Info("scheduled speed test skipped", "server", h.Name, "err", err)
			}
		}
	}
}
