package forecast

import (
	"math"
	"testing"

	"hook/internal/store"
)

func series(hours int, f func(h int) float64) []store.MetricPoint {
	out := make([]store.MetricPoint, hours)
	for h := range out {
		out[h].TS = int64(h) * 3_600_000
		out[h].DiskPct = f(h)
	}
	return out
}

func TestDiskFull(t *testing.T) {
	// 60% growing 2 points a day with a little noise: full in ~20 days.
	d := DiskFull(series(7*24, func(h int) float64 { return 60 + 2*float64(h)/24 + math.Sin(float64(h))*0.3 }))
	if d.DaysToFull == nil || !d.Confident || math.Abs(*d.DaysToFull-(100-d.Current)/2) > 0.5 || math.Abs(d.SlopePerDay-2) > 0.05 {
		t.Fatalf("growing disk: %+v (days %v)", d, d.DaysToFull)
	}
	// Flat usage never forecasts.
	if d := DiskFull(series(7*24, func(int) float64 { return 70 })); d.DaysToFull != nil {
		t.Fatalf("flat disk forecast %v", *d.DaysToFull)
	}
	// Shrinking usage never forecasts.
	if d := DiskFull(series(7*24, func(h int) float64 { return 80 - float64(h)/24 })); d.DaysToFull != nil {
		t.Fatal("shrinking disk should not forecast")
	}
	// Under a day of data: no forecast even if it's climbing.
	if d := DiskFull(series(12, func(h int) float64 { return 50 + float64(h) })); d.DaysToFull != nil {
		t.Fatal("too little history")
	}
	// Big noisy jumps: a date, but flagged as not confident.
	d = DiskFull(series(7*24, func(h int) float64 { return 50 + float64(h%37) + float64(h)/48 }))
	if d.DaysToFull != nil && d.Confident {
		t.Fatalf("noisy data shouldn't be confident: r2=%.2f", d.R2)
	}
}

func TestRightSize(t *testing.T) {
	mk := func(days int, cpu, mem float64) []store.MetricPoint {
		out := make([]store.MetricPoint, days*24)
		for i := range out {
			c, m := cpu, mem
			out[i] = store.MetricPoint{CPUMax: &c, MemUsedMax: &m}
			out[i].TS, out[i].CPU, out[i].MemUsed, out[i].MemTotal = int64(i)*3_600_000, cpu/2, mem, 100
		}
		return out
	}
	if v := RightSize(mk(30, 12, 30)).Verdict; v != "oversized" {
		t.Fatalf("quiet server: %s", v)
	}
	if v := RightSize(mk(30, 95, 95)).Verdict; v != "tight" {
		t.Fatalf("busy server: %s", v)
	}
	if v := RightSize(mk(30, 60, 60)).Verdict; v != "ok" {
		t.Fatalf("normal server: %s", v)
	}
	if v := RightSize(mk(3, 5, 5)).Verdict; v != "" {
		t.Fatalf("3 days is too little to judge: %s", v)
	}
}
