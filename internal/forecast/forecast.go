// Package forecast derives simple, honest predictions from server history:
// when the fullest disk will fill up, and whether a server is over- or
// under-sized. Everything here is pure arithmetic on stored rollups.
package forecast

import (
	"math"
	"slices"

	"hook/internal/store"
)

// Disk is a linear projection of the fullest mount's usage.
type Disk struct {
	Current     float64       `json:"current"`     // % used now
	SlopePerDay float64       `json:"slopePerDay"` // percentage points per day
	R2          float64       `json:"r2"`          // fit quality, 0–1
	DaysToFull  *float64      `json:"daysToFull"`  // nil: not filling, or not enough data
	Confident   bool          `json:"confident"`   // enough data and a good fit
	Points      int           `json:"points"`
	Line        [2][2]float64 `json:"line"` // fitted line endpoints [ts ms, %] for charts
}

const msPerDay = 86_400_000.0

// DiskFull fits a least-squares line through hourly disk usage. It reports a
// date only when there's at least a day of history, usage is growing by more
// than 0.05 points a day, and the line explains most of the variation.
func DiskFull(points []store.MetricPoint) Disk {
	var xs, ys []float64
	for _, p := range points {
		if p.DiskPct > 0 {
			xs = append(xs, float64(p.TS)/msPerDay)
			ys = append(ys, p.DiskPct)
		}
	}
	d := Disk{Points: len(xs)}
	if len(xs) < 24 || xs[len(xs)-1]-xs[0] < 1 {
		if len(ys) > 0 {
			d.Current = ys[len(ys)-1]
		}
		return d
	}
	slope, icpt, r2 := fit(xs, ys)
	d.Current, d.SlopePerDay, d.R2 = ys[len(ys)-1], slope, r2
	d.Line = [2][2]float64{{xs[0] * msPerDay, slope*xs[0] + icpt}, {xs[len(xs)-1] * msPerDay, slope*xs[len(xs)-1] + icpt}}
	if slope > 0.05 {
		days := (100 - d.Current) / slope
		d.DaysToFull = &days
		d.Confident = r2 >= 0.6
	}
	return d
}

func fit(xs, ys []float64) (slope, icpt, r2 float64) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, sy / n, 0
	}
	slope = (n*sxy - sx*sy) / den
	icpt = (sy - slope*sx) / n
	mean := sy / n
	var ssTot, ssRes float64
	for i := range xs {
		ssTot += (ys[i] - mean) * (ys[i] - mean)
		e := ys[i] - (slope*xs[i] + icpt)
		ssRes += e * e
	}
	if ssTot == 0 {
		return slope, icpt, 0
	}
	return slope, icpt, math.Max(0, 1-ssRes/ssTot)
}

// Sizing summarises a month of peaks to suggest resizing.
type Sizing struct {
	Days    float64 `json:"days"`    // history covered
	CPUPeak float64 `json:"cpuPeak"` // highest hourly peak, %
	CPUP95  float64 `json:"cpuP95"`  // 95th percentile of hourly averages, %
	MemPeak float64 `json:"memPeak"` // highest memory use, %
	Verdict string  `json:"verdict"` // oversized | tight | ok | "" (not enough data)
}

// RightSize needs at least a week of hourly rollups to say anything.
func RightSize(points []store.MetricPoint) Sizing {
	var s Sizing
	if len(points) == 0 {
		return s
	}
	s.Days = float64(points[len(points)-1].TS-points[0].TS) / msPerDay
	var avgs []float64
	for _, p := range points {
		cpuMax := p.CPU
		if p.CPUMax != nil {
			cpuMax = *p.CPUMax
		}
		s.CPUPeak = math.Max(s.CPUPeak, cpuMax)
		avgs = append(avgs, p.CPU)
		if p.MemTotal > 0 {
			used := p.MemUsed
			if p.MemUsedMax != nil {
				used = *p.MemUsedMax
			}
			s.MemPeak = math.Max(s.MemPeak, used/p.MemTotal*100)
		}
	}
	slices.Sort(avgs)
	s.CPUP95 = avgs[int(math.Ceil(0.95*float64(len(avgs))))-1]
	if s.Days < 7 {
		return s
	}
	switch {
	case s.CPUP95 >= 80 || s.MemPeak >= 90:
		s.Verdict = "tight"
	case s.CPUPeak < 25 && s.MemPeak < 40:
		s.Verdict = "oversized"
	default:
		s.Verdict = "ok"
	}
	return s
}
