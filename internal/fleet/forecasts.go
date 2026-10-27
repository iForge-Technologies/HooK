package fleet

import (
	"context"
	"fmt"
	"time"

	"hook/internal/alert"
	"hook/internal/events"
	"hook/internal/forecast"
)

// RunForecasts re-projects every server's disk usage hourly. A confident
// "full within 7 days" opens a forecast event and alerts once; it clears
// (quietly) when the projection moves past 14 days or stops growing.
func (f *Fleet) RunForecasts(ctx context.Context) {
	timer := time.NewTimer(2 * time.Minute) // let rollups catch up after a restart
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		f.forecastAll(time.Now())
		timer.Reset(time.Hour)
	}
}

func (f *Fleet) forecastAll(now time.Time) {
	hosts, err := f.store.Hosts()
	if err != nil {
		f.log.Error("forecast: load hosts", "err", err)
		return
	}
	changed := false
	for _, h := range hosts {
		if h.Status == "new" {
			continue
		}
		pts, err := f.store.HostSeries(h.ID, now.Add(-7*24*time.Hour).UnixMilli(), "1h")
		if err != nil {
			continue
		}
		d := forecast.DiskFull(pts)
		soon := d.Confident && d.DaysToFull != nil && *d.DaysToFull <= 7 && h.Thresholds.Disk > 0
		fine := d.DaysToFull == nil || *d.DaysToFull > 14
		switch {
		case soon:
			detail := fmt.Sprintf("fullest mount at %.0f%%, growing %.1f points a day", d.Current, d.SlopePerDay)
			opened, _ := f.store.OpenHostEvent(h.ID, "forecast", "disk", now.UnixMilli(), *d.DaysToFull, detail)
			if opened {
				changed = true
				ev := alert.ServerEvent(alert.KindForecast, h, now)
				ev.Msg, ev.Value, ev.Peak = detail, d.Current, *d.DaysToFull
				f.log.Warn("disk forecast", "server", h.Name, "days", *d.DaysToFull)
				f.alerts.Notify(ev)
			}
		case fine:
			if started, _ := f.store.ResolveHostEvent(h.ID, "forecast", "disk", now.UnixMilli()); started > 0 {
				changed = true
			}
		}
	}
	if changed {
		f.events.Publish(events.Event{Type: "hosts"})
	}
}
