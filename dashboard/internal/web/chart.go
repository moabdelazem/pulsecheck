package web

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/moabdelazem/pulsecheck-dashboard/internal/apiclient"
)

// chartRange is one of the time ranges the latency chart can show.
type chartRange struct {
	Key    string // value of ?range=
	Window time.Duration
	Bucket time.Duration
	Format string // x-axis label format
	Per    string // bucket length for the chart title
}

var chartRanges = []chartRange{
	{"1h", time.Hour, time.Minute, "15:04", "minute"},
	{"24h", 24 * time.Hour, 15 * time.Minute, "15:04", "15 minutes"},
	{"7d", 7 * 24 * time.Hour, 2 * time.Hour, "Jan 2", "2 hours"},
}

func findRange(key string) chartRange {
	for _, r := range chartRanges {
		if r.Key == key {
			return r
		}
	}
	return chartRanges[1] // 24h
}

type chartTick struct {
	Pos    float64
	Label  string
	Anchor string // SVG text-anchor
}

// chartSlot is one bucket: a hover target, and a red band if any check failed.
type chartSlot struct {
	X, W  float64
	Down  bool
	Label string
}

// chart holds everything the SVG template needs, in viewBox units.
type chart struct {
	W, H                     float64
	Left, Right, Top, Bottom float64
	Path                     string
	YTicks, XTicks           []chartTick
	Slots                    []chartSlot
	HasDown, Empty           bool
	Summary                  string
}

func buildChart(buckets []apiclient.Bucket, rg chartRange, now time.Time) chart {
	c := chart{W: 800, H: 240, Left: 64, Right: 788, Top: 12, Bottom: 208}
	if len(buckets) == 0 {
		c.Empty = true
		return c
	}
	since := now.Add(-rg.Window)
	plotW, plotH := c.Right-c.Left, c.Bottom-c.Top
	x := func(t time.Time) float64 {
		f := float64(t.Sub(since)) / float64(rg.Window)
		return c.Left + math.Max(0, math.Min(1, f))*plotW
	}

	lo, hi := math.Inf(1), 0.0
	for _, b := range buckets {
		if b.AvgLatencyMS != nil {
			lo, hi = math.Min(lo, *b.AvgLatencyMS), math.Max(hi, *b.AvgLatencyMS)
		}
	}
	top := niceCeil(hi)
	y := func(v float64) float64 { return c.Bottom - v/top*plotH }

	var path strings.Builder
	var prev time.Time
	pen, downSlots := false, 0
	for _, b := range buckets {
		xs, xe := x(b.Time), x(b.Time.Add(rg.Bucket))
		slot := chartSlot{X: xs, W: math.Max(xe-xs, 1), Down: b.Down > 0}
		label := b.Time.Format("Jan 2 15:04") + ": "
		if b.AvgLatencyMS != nil {
			label += fmt.Sprintf("avg %.0f ms", *b.AvgLatencyMS)
			// Start a new line segment after a gap so missing data is not drawn over.
			cmd := "L"
			if !pen || b.Time.Sub(prev) > rg.Bucket {
				cmd = "M"
			}
			fmt.Fprintf(&path, "%s%.1f %.1f ", cmd, x(b.Time.Add(rg.Bucket/2)), y(*b.AvgLatencyMS))
			pen, prev = true, b.Time
		} else {
			label += "no successful checks"
			pen = false
		}
		label += fmt.Sprintf(", %d checks", b.Checks)
		if b.Down > 0 {
			label += fmt.Sprintf(", %d failed", b.Down)
			c.HasDown = true
			downSlots++
		}
		slot.Label = label
		c.Slots = append(c.Slots, slot)
	}
	c.Path = strings.TrimSpace(path.String())

	for _, v := range []float64{0, top / 2, top} {
		c.YTicks = append(c.YTicks, chartTick{Pos: y(v), Label: fmt.Sprintf("%.0f ms", v)})
	}
	for i := 0; i <= 4; i++ {
		t := since.Add(rg.Window * time.Duration(i) / 4)
		anchor := "middle"
		switch i {
		case 0:
			anchor = "start"
		case 4:
			anchor = "end"
		}
		c.XTicks = append(c.XTicks, chartTick{Pos: x(t), Label: t.Format(rg.Format), Anchor: anchor})
	}

	c.Summary = fmt.Sprintf("Average response time over the last %s.", rg.Key)
	if hi > 0 {
		c.Summary += fmt.Sprintf(" Lowest %.0f ms, highest %.0f ms.", lo, hi)
	}
	if downSlots > 0 {
		c.Summary += fmt.Sprintf(" %d time slots had failed checks.", downSlots)
	}
	return c
}

// niceCeil rounds v up to 1, 2 or 5 times a power of ten, so axis labels stay round.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 100
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}
