package taskpkg

import (
	"fmt"
	"math"
)

// Config controls the predictive concealment engine.
//
// Window is N, the number of most recent real samples used for the
// autocorrelation and Levinson-Durbin estimate. Order is P. Periods are
// measured in samples and both ends of the range are inclusive.
//
// Decay is applied once per consecutive missing sample. The first missing
// sample is at full gain and sample m (zero based) has gain Decay^m.
// Crossfade is L, the number of real samples used to fade a resumed signal
// back in.
type Config struct {
	Window         int
	Order          int
	MinPeriod      int
	MaxPeriod      int
	MinCorrelation float64
	Decay          float64
	Crossfade      int
	Seed           int64
}

// DefaultConfig returns settings suitable for the supplied sample rate.
// A non-positive sample rate is treated as 48 kHz.
func DefaultConfig(sampleRate int) Config {
	if sampleRate <= 0 {
		sampleRate = 48000
	}

	window := sampleRate / 25 // 40 ms
	if window < 64 {
		window = 64
	}
	minPeriod := sampleRate / 500 // 2 ms
	if minPeriod < 2 {
		minPeriod = 2
	}
	maxPeriod := sampleRate / 50 // 20 ms
	if maxPeriod >= window {
		maxPeriod = window/2 - 1
	}
	if maxPeriod < minPeriod {
		maxPeriod = minPeriod
	}
	crossfade := sampleRate / 200 // 5 ms
	if crossfade < 1 {
		crossfade = 1
	}

	return Config{
		Window:         window,
		Order:          16,
		MinPeriod:      minPeriod,
		MaxPeriod:      maxPeriod,
		MinCorrelation: 0.70,
		Decay:          0.9999,
		Crossfade:      crossfade,
		Seed:           1,
	}
}

// Validate checks that the configuration can produce a stable, predictable
// stream. It does not clamp values silently.
func (c Config) Validate() error {
	if c.Window < 2 {
		return fmt.Errorf("window must be at least 2, got %d", c.Window)
	}
	if c.Order < 0 {
		return fmt.Errorf("order must not be negative, got %d", c.Order)
	}
	if c.Order >= c.Window {
		return fmt.Errorf("order (%d) must be smaller than window (%d)", c.Order, c.Window)
	}
	if c.MinPeriod < 1 {
		return fmt.Errorf("minimum period must be at least 1, got %d", c.MinPeriod)
	}
	if c.MaxPeriod < c.MinPeriod {
		return fmt.Errorf("maximum period (%d) is below minimum period (%d)", c.MaxPeriod, c.MinPeriod)
	}
	if c.MaxPeriod >= c.Window {
		return fmt.Errorf("maximum period (%d) must be smaller than window (%d)", c.MaxPeriod, c.Window)
	}
	if math.IsNaN(c.MinCorrelation) || c.MinCorrelation < 0 || c.MinCorrelation > 1 {
		return fmt.Errorf("minimum correlation must be in [0, 1], got %g", c.MinCorrelation)
	}
	if math.IsNaN(c.Decay) || c.Decay < 0 || c.Decay > 1 {
		return fmt.Errorf("decay must be in [0, 1], got %g", c.Decay)
	}
	if c.Crossfade < 0 {
		return fmt.Errorf("crossfade must not be negative, got %d", c.Crossfade)
	}
	return nil
}
