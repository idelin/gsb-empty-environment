package hexgrid

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// EnvMetrics carries external pressure and environment indicators used
// to drive adaptive threshold scaling. All fields are normalized to the
// [0, 1] range; out-of-range values are clamped.
type EnvMetrics struct {
	CPUUtil    float64 // CPU utilization, 0..1
	MemUtil    float64 // memory utilization, 0..1
	QueueDepth float64 // normalized backlog/queue depth, 0..1
	ErrorRate  float64 // recent error rate, 0..1
}

// Composite pressure weights for EnvMetrics.
const (
	weightCPU   = 0.40
	weightMem   = 0.20
	weightQueue = 0.25
	weightError = 0.15
)

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Pressure computes the weighted composite load pressure in [0, 1].
func (m EnvMetrics) Pressure() float64 {
	p := weightCPU*clamp01(m.CPUUtil) +
		weightMem*clamp01(m.MemUtil) +
		weightQueue*clamp01(m.QueueDepth) +
		weightError*clamp01(m.ErrorRate)
	return clamp01(p)
}

// AdaptiveConfig configures an AdaptiveController.
type AdaptiveConfig struct {
	// Min/Max bound the dynamic threshold. Initial is the starting value.
	Min, Max, Initial float64
	// HighWatermark/LowWatermark define the hysteresis band on smoothed
	// load pressure in [0, 1]. Pressure above High triggers a decrease,
	// below Low triggers an increase, inside the band nothing happens.
	HighWatermark, LowWatermark float64
	// IncreaseStep is the additive increase applied when load is low.
	IncreaseStep float64
	// DecreaseFactor is the multiplicative decrease factor in (0, 1)
	// applied when load is high (AIMD-style backoff).
	DecreaseFactor float64
	// Smoothing is the EMA factor alpha in (0, 1] for load smoothing.
	Smoothing float64
	// Cooldown is the minimum interval between two adjustments; it
	// prevents oscillation and thundering-herd (avalanche) reactions.
	Cooldown time.Duration
	// PressureGain scales the adjustment magnitude with how far the
	// smoothed pressure lies beyond the watermark (load awareness).
	// 0 disables pressure-proportional scaling.
	PressureGain float64
}

func (cfg AdaptiveConfig) validate() error {
	switch {
	case cfg.Min <= 0:
		return errors.New("hexgrid: Min must be > 0")
	case cfg.Max < cfg.Min:
		return errors.New("hexgrid: Max must be >= Min")
	case cfg.Initial < cfg.Min || cfg.Initial > cfg.Max:
		return errors.New("hexgrid: Initial must be within [Min, Max]")
	case cfg.LowWatermark < 0 || cfg.HighWatermark > 1 || cfg.LowWatermark >= cfg.HighWatermark:
		return errors.New("hexgrid: require 0 <= LowWatermark < HighWatermark <= 1")
	case cfg.IncreaseStep <= 0:
		return errors.New("hexgrid: IncreaseStep must be > 0")
	case cfg.DecreaseFactor <= 0 || cfg.DecreaseFactor >= 1:
		return errors.New("hexgrid: DecreaseFactor must be in (0, 1)")
	case cfg.Smoothing <= 0 || cfg.Smoothing > 1:
		return errors.New("hexgrid: Smoothing must be in (0, 1]")
	case cfg.Cooldown < 0:
		return errors.New("hexgrid: Cooldown must be >= 0")
	case cfg.PressureGain < 0:
		return errors.New("hexgrid: PressureGain must be >= 0")
	}
	return nil
}

// Adjustment describes the outcome of one Observe call.
type Adjustment struct {
	Load         float64 // raw composite pressure of this sample
	SmoothedLoad float64 // EMA of pressure after this sample
	OldThreshold float64
	NewThreshold float64
	Adjusted     bool // whether the threshold changed
	Direction    int  // -1 decreased, 0 unchanged, +1 increased
}

// Snapshot is a consistent read-only view of the controller state.
type Snapshot struct {
	Threshold    float64
	SmoothedLoad float64
	Min, Max     float64
}

// AdaptiveController self-tunes an internal threshold (e.g. an admission
// limit, batch size or concurrency budget) based on observed external
// pressure. It combines:
//
//   - EMA smoothing of the pressure signal (noise rejection)
//   - a hysteresis band (no reaction to insignificant drift)
//   - AIMD scaling: additive increase, multiplicative decrease
//   - pressure-proportional gain (load-aware step sizing)
//   - a cooldown window between adjustments (avalanche avoidance)
//   - hard [Min, Max] clamping (resource bounds)
//
// The controller spawns no goroutines and is safe for concurrent use.
type AdaptiveController struct {
	mu         sync.Mutex
	cfg        AdaptiveConfig
	threshold  float64
	ema        float64
	hasSample  bool
	lastAdjust time.Time
	now        func() time.Time
}

// AdaptiveOption customizes an AdaptiveController.
type AdaptiveOption func(*AdaptiveController)

// WithClock overrides the time source (useful for deterministic tests).
func WithClock(now func() time.Time) AdaptiveOption {
	return func(c *AdaptiveController) {
		if now != nil {
			c.now = now
		}
	}
}

// NewAdaptiveController creates a controller from the given config.
func NewAdaptiveController(cfg AdaptiveConfig, opts ...AdaptiveOption) (*AdaptiveController, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &AdaptiveController{
		cfg:       cfg,
		threshold: cfg.Initial,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Observe feeds one environment sample into the controller and returns
// the resulting adjustment decision.
func (c *AdaptiveController) Observe(m EnvMetrics) Adjustment {
	load := m.Pressure()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.hasSample {
		c.ema = load
		c.hasSample = true
	} else {
		c.ema += c.cfg.Smoothing * (load - c.ema)
	}

	adj := Adjustment{
		Load:         load,
		SmoothedLoad: c.ema,
		OldThreshold: c.threshold,
		NewThreshold: c.threshold,
	}

	if c.now().Sub(c.lastAdjust) < c.cfg.Cooldown {
		return adj // cooldown: hold steady to avoid avalanche reactions
	}

	var next float64
	var direction int
	switch {
	case c.ema > c.cfg.HighWatermark:
		// Multiplicative decrease, scaled by how far pressure exceeds
		// the watermark. The cut is capped to keep the decrease sane.
		span := 1 - c.cfg.HighWatermark
		pressure := 0.0
		if span > 0 {
			pressure = (c.ema - c.cfg.HighWatermark) / span
		}
		cut := (1 - c.cfg.DecreaseFactor) * (1 + c.cfg.PressureGain*pressure)
		if cut > 0.9 {
			cut = 0.9
		}
		next = c.threshold * (1 - cut)
		if next < c.cfg.Min {
			next = c.cfg.Min
		}
		direction = -1
	case c.ema < c.cfg.LowWatermark:
		// Additive increase, scaled by available headroom below the
		// low watermark.
		headroom := 0.0
		if c.cfg.LowWatermark > 0 {
			headroom = (c.cfg.LowWatermark - c.ema) / c.cfg.LowWatermark
		}
		next = c.threshold + c.cfg.IncreaseStep*(1+c.cfg.PressureGain*headroom)
		if next > c.cfg.Max {
			next = c.cfg.Max
		}
		direction = 1
	default:
		return adj // hysteresis band: deliberately no reaction
	}

	if next != c.threshold {
		c.threshold = next
		c.lastAdjust = c.now()
		adj.Adjusted = true
		adj.Direction = direction
		adj.NewThreshold = next
	}
	return adj
}

// Snapshot returns a consistent view of the current controller state.
func (c *AdaptiveController) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		Threshold:    c.threshold,
		SmoothedLoad: c.ema,
		Min:          c.cfg.Min,
		Max:          c.cfg.Max,
	}
}

// Threshold returns the current dynamic threshold.
func (c *AdaptiveController) Threshold() float64 {
	return c.Snapshot().Threshold
}

// Allow reports whether a unit of work with the given cost is admitted
// under the current dynamic threshold (load-aware admission control).
func (c *AdaptiveController) Allow(cost float64) bool {
	return cost <= c.Threshold()
}

// String describes the controller state for diagnostics.
func (c *AdaptiveController) String() string {
	s := c.Snapshot()
	return fmt.Sprintf("AdaptiveController{threshold=%.4f ema=%.4f bounds=[%.4f, %.4f]}",
		s.Threshold, s.SmoothedLoad, s.Min, s.Max)
}
