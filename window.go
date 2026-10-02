// Package slidinghist implements a sliding-window histogram for streaming
// high-percentile (P90/P99) latency estimation.
//
// Bucket layout is hybrid: fixed-width buckets cover the low value range,
// and logarithmically growing buckets cover the tail. This keeps the
// relative error of quantile estimates bounded by a known theoretical
// limit while using only O(number-of-buckets) memory plus one uint32
// bucket index per in-window sample (raw samples are never retained).
//
// Expiry is handled by reverse compensation: when a sample slides out of
// the window, only its bucket counter is decremented.
package slidinghist

import (
	"fmt"
	"math"
)

// Config describes the hybrid bucket layout. Values are unit-agnostic
// (e.g. milliseconds); all fields share the same unit.
type Config struct {
	// FixedWidth is the width of each fixed bucket in [0, FixedMax).
	FixedWidth float64
	// FixedMax is the boundary between the fixed and logarithmic regions.
	FixedMax float64
	// LogGrowth is the growth ratio (>1) of consecutive log buckets.
	// The representative-value relative error in the log region is
	// bounded by (LogGrowth-1)/2.
	LogGrowth float64
	// MaxValue clamps the layout; larger values go to an overflow bucket.
	MaxValue float64
}

// DefaultConfig returns a layout tuned for millisecond latencies:
// 0.01ms fixed buckets up to 10ms, then 0.4%-growth log buckets up to
// 60s. Worst-case relative error of a quantile estimate is <= 0.2% in
// the log region and <= 0.005ms absolute in the fixed region.
func DefaultConfig() Config {
	return Config{FixedWidth: 0.01, FixedMax: 10, LogGrowth: 1.004, MaxValue: 60000}
}

func (c Config) validate() error {
	switch {
	case c.FixedWidth <= 0:
		return fmt.Errorf("slidinghist: FixedWidth must be > 0")
	case c.FixedMax <= c.FixedWidth:
		return fmt.Errorf("slidinghist: FixedMax must be > FixedWidth")
	case c.LogGrowth <= 1:
		return fmt.Errorf("slidinghist: LogGrowth must be > 1")
	case c.MaxValue <= c.FixedMax:
		return fmt.Errorf("slidinghist: MaxValue must be > FixedMax")
	}
	return nil
}

// layout precomputes the bucket geometry derived from Config.
type layout struct {
	cfg        Config
	fixedCount int // number of fixed-width buckets
	logCount   int // number of logarithmic buckets
	logGrowth  float64
	logFixed   float64
}

func newLayout(cfg Config) layout {
	l := layout{cfg: cfg, logGrowth: math.Log(cfg.LogGrowth), logFixed: math.Log(cfg.FixedMax)}
	l.fixedCount = int(math.Ceil(cfg.FixedMax / cfg.FixedWidth))
	l.logCount = int(math.Ceil((math.Log(cfg.MaxValue) - l.logFixed) / l.logGrowth))
	return l
}

// totalBuckets includes one overflow bucket for values >= MaxValue.
func (l *layout) totalBuckets() int { return l.fixedCount + l.logCount + 1 }

// index maps a value to its bucket index.
func (l *layout) index(v float64) int {
	if v < 0 {
		v = 0
	}
	if v < l.cfg.FixedMax {
		i := int(v / l.cfg.FixedWidth)
		if i >= l.fixedCount {
			i = l.fixedCount - 1
		}
		return i
	}
	if v >= l.cfg.MaxValue {
		return l.totalBuckets() - 1 // overflow bucket
	}
	i := int((math.Log(v) - l.logFixed) / l.logGrowth)
	if i >= l.logCount {
		i = l.logCount - 1
	}
	return l.fixedCount + i
}

// bounds returns the [lo, hi) bounds of bucket i.
func (l *layout) bounds(i int) (lo, hi float64) {
	switch {
	case i < l.fixedCount:
		lo = float64(i) * l.cfg.FixedWidth
		return lo, lo + l.cfg.FixedWidth
	case i < l.fixedCount+l.logCount:
		lo = math.Exp(l.logFixed + float64(i-l.fixedCount)*l.logGrowth)
		return lo, lo * l.cfg.LogGrowth
	default:
		return l.cfg.MaxValue, math.Inf(1)
	}
}

// representative returns the value used to estimate any sample in bucket i
// (the geometric midpoint for log buckets, arithmetic for fixed buckets),
// which minimises the worst-case relative error.
func (l *layout) representative(i int) float64 {
	lo, hi := l.bounds(i)
	if math.IsInf(hi, 1) {
		return lo
	}
	if i >= l.fixedCount {
		return math.Sqrt(lo * hi) // geometric mean: rel err <= (g-1)/2
	}
	return (lo + hi) / 2
}

// Window is a count-based sliding-window histogram over the most recent
// samples. It is not safe for concurrent use.
type Window struct {
	lay    layout
	counts []uint64 // per-bucket live counts
	ring   []uint32 // bucket index of each in-window sample (ring buffer)
	head   int      // next write slot == oldest sample when full
	size   int
	total  uint64
}

// NewWindow creates a window holding at most capacity samples.
func NewWindow(capacity int, cfg Config) (*Window, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("slidinghist: capacity must be > 0")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	lay := newLayout(cfg)
	return &Window{
		lay:    lay,
		counts: make([]uint64, lay.totalBuckets()),
		ring:   make([]uint32, capacity),
	}, nil
}

// Add records one sample. When the window is full, the oldest sample is
// evicted first by decrementing its bucket counter (reverse compensation);
// the raw value is never stored.
func (w *Window) Add(v float64) {
	idx := w.lay.index(v)
	if w.size == len(w.ring) {
		old := w.ring[w.head]
		w.counts[old]--
		w.total--
		w.size--
	}
	w.ring[w.head] = uint32(idx)
	w.head = (w.head + 1) % len(w.ring)
	w.counts[idx]++
	w.total++
	w.size++
}

// Count returns the number of live samples in the window.
func (w *Window) Count() int { return w.size }

// MaxRelativeError returns the theoretical worst-case relative error of
// quantile estimates that fall in the logarithmic region.
func (w *Window) MaxRelativeError() float64 { return (w.lay.cfg.LogGrowth - 1) / 2 }

// Quantile returns the estimated q-quantile (0 < q <= 1) of the live
// window, using the same rank rule as the exact reference: the value at
// rank ceil(q*n) in ascending order.
func (w *Window) Quantile(q float64) float64 {
	if w.total == 0 || q <= 0 {
		return 0
	}
	if q > 1 {
		q = 1
	}
	rank := uint64(math.Ceil(q * float64(w.total)))
	if rank == 0 {
		rank = 1
	}
	var cum uint64
	for i, c := range w.counts {
		cum += c
		if cum >= rank {
			return w.lay.representative(i)
		}
	}
	return w.lay.representative(len(w.counts) - 1)
}

// P90 returns the estimated 90th percentile.
func (w *Window) P90() float64 { return w.Quantile(0.90) }

// P99 returns the estimated 99th percentile.
func (w *Window) P99() float64 { return w.Quantile(0.99) }
