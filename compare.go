package zerobubble

import (
	"fmt"
	"strings"
)

// Comparison holds a 1F1B baseline and its Zero-Bubble counterpart.
type Comparison struct {
	Config Config

	Baseline   *Schedule
	ZeroBubble *Schedule

	BaselineBubbleRate      float64
	ZeroBubbleBubbleRate    float64
	BubbleRateImprovement   float64
	RelativeBubbleReduction float64
	MakespanImprovement     float64
}

// Compare builds both schedules and computes their bubble metrics.
func Compare(cfg Config) (*Comparison, error) {
	baseline, err := Build1F1B(cfg)
	if err != nil {
		return nil, fmt.Errorf("1F1B baseline: %w", err)
	}
	zb, err := BuildZeroBubble(cfg)
	if err != nil {
		return nil, fmt.Errorf("Zero-Bubble schedule: %w", err)
	}
	cmp := &Comparison{
		Config:                cfg,
		Baseline:              baseline,
		ZeroBubble:            zb,
		BaselineBubbleRate:    baseline.BubbleRate,
		ZeroBubbleBubbleRate:  zb.BubbleRate,
		BubbleRateImprovement: baseline.BubbleRate - zb.BubbleRate,
		MakespanImprovement:   baseline.Makespan - zb.Makespan,
	}
	if baseline.BubbleRate > timeEpsilon {
		cmp.RelativeBubbleReduction = cmp.BubbleRateImprovement / baseline.BubbleRate
	}
	return cmp, nil
}

// CompareModes is an alias for Compare.
func CompareModes(cfg Config) (*Comparison, error) {
	return Compare(cfg)
}

// StrictlyBetter reports whether the Zero-Bubble schedule has both a shorter
// makespan and a strictly smaller bubble rate.
func (c *Comparison) StrictlyBetter() bool {
	if c == nil || c.Baseline == nil || c.ZeroBubble == nil {
		return false
	}
	return c.ZeroBubble.Makespan < c.Baseline.Makespan-timeEpsilon &&
		c.ZeroBubble.BubbleRate < c.Baseline.BubbleRate-timeEpsilon
}

// Report renders the headline comparison metrics.
func (c *Comparison) Report() string {
	if c == nil || c.Baseline == nil || c.ZeroBubble == nil {
		return "invalid comparison"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "1F1B       makespan=%9.3f bubble=%7.3f%% peak=%v\n",
		c.Baseline.Makespan, 100*c.Baseline.BubbleRate, c.Baseline.PeakMemory)
	fmt.Fprintf(&b, "ZeroBubble makespan=%9.3f bubble=%7.3f%% peak=%v\n",
		c.ZeroBubble.Makespan, 100*c.ZeroBubble.BubbleRate, c.ZeroBubble.PeakMemory)
	fmt.Fprintf(&b, "reduction  makespan=%9.3f bubble=%7.3f pp (%.2f%%) strictly-better=%t\n",
		c.MakespanImprovement, 100*c.BubbleRateImprovement,
		100*c.RelativeBubbleReduction, c.StrictlyBetter())
	return b.String()
}
