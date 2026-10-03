// Package adaptive implements a load-aware controller that dynamically
// scales internal limits (in-flight concurrency, batch size, leader lease)
// from external pressure signals, with AIMD adjustment, change cooldown
// (hysteresis) and a circuit breaker to avoid cascading failure (avalanche).
package adaptive

import (
	"sync"
	"time"
)

// Metrics is one sample of external pressure / environment signals.
type Metrics struct {
	Load       float64       // 0..1 normalized system load
	QueueDepth int           // pending work items
	RTT        time.Duration // observed consensus round-trip time
	ErrorRate  float64       // 0..1 recent failure ratio
}

// Limits are the tunable internal parameters produced by the controller.
type Limits struct {
	MaxInFlight   int           // max concurrent proposals
	BatchSize     int           // entries per consensus batch
	LeaseDuration time.Duration // leader lease length
}

// Config bounds and tunes the controller.
type Config struct {
	MinInFlight int
	MaxInFlight int
	MinBatch    int
	MaxBatch    int

	BaseLease time.Duration
	MinLease  time.Duration
	MaxLease  time.Duration

	LowLoad  float64 // scale up below this
	HighLoad float64 // scale down above this

	ErrorThreshold  float64       // error rate that trips the breaker
	Cooldown        time.Duration // min interval between limit changes
	BreakerDuration time.Duration // how long the breaker stays open

	IncreaseStep   int     // additive increase per good sample
	DecreaseFactor float64 // multiplicative decrease, 0<f<1
}

// DefaultConfig returns sane defaults for a 5-node cluster.
func DefaultConfig() Config {
	return Config{
		MinInFlight:     4,
		MaxInFlight:     1024,
		MinBatch:        1,
		MaxBatch:        256,
		BaseLease:       150 * time.Millisecond,
		MinLease:        50 * time.Millisecond,
		MaxLease:        2 * time.Second,
		LowLoad:         0.3,
		HighLoad:        0.8,
		ErrorThreshold:  0.2,
		Cooldown:        50 * time.Millisecond,
		BreakerDuration: 500 * time.Millisecond,
		IncreaseStep:    8,
		DecreaseFactor:  0.7,
	}
}

// Controller is safe for concurrent use. It spawns no goroutines.
type Controller struct {
	mu     sync.Mutex
	cfg    Config
	limits Limits

	lastChange   time.Time
	breakerOpen  bool
	breakerUntil time.Time

	now func() time.Time // injectable clock (tests)
}

// NewController validates cfg (clamping nonsense to defaults) and starts
// from the minimal limits — scale up is earned by healthy samples.
func NewController(cfg Config) *Controller {
	d := DefaultConfig()
	if cfg.MinInFlight <= 0 {
		cfg.MinInFlight = d.MinInFlight
	}
	if cfg.MaxInFlight < cfg.MinInFlight {
		cfg.MaxInFlight = d.MaxInFlight
	}
	if cfg.MinBatch <= 0 {
		cfg.MinBatch = d.MinBatch
	}
	if cfg.MaxBatch < cfg.MinBatch {
		cfg.MaxBatch = d.MaxBatch
	}
	if cfg.BaseLease <= 0 {
		cfg.BaseLease = d.BaseLease
	}
	if cfg.MinLease <= 0 {
		cfg.MinLease = d.MinLease
	}
	if cfg.MaxLease < cfg.MinLease {
		cfg.MaxLease = d.MaxLease
	}
	if cfg.LowLoad < 0 || cfg.LowLoad > 1 {
		cfg.LowLoad = d.LowLoad
	}
	if cfg.HighLoad <= cfg.LowLoad || cfg.HighLoad > 1 {
		cfg.HighLoad = d.HighLoad
	}
	if cfg.ErrorThreshold <= 0 || cfg.ErrorThreshold > 1 {
		cfg.ErrorThreshold = d.ErrorThreshold
	}
	if cfg.Cooldown < 0 {
		cfg.Cooldown = d.Cooldown
	}
	if cfg.BreakerDuration <= 0 {
		cfg.BreakerDuration = d.BreakerDuration
	}
	if cfg.IncreaseStep <= 0 {
		cfg.IncreaseStep = d.IncreaseStep
	}
	if cfg.DecreaseFactor <= 0 || cfg.DecreaseFactor >= 1 {
		cfg.DecreaseFactor = d.DecreaseFactor
	}
	c := &Controller{cfg: cfg, now: time.Now}
	c.limits = Limits{
		MaxInFlight:   cfg.MinInFlight,
		BatchSize:     cfg.MinBatch,
		LeaseDuration: clampDuration(cfg.BaseLease, cfg.MinLease, cfg.MaxLease),
	}
	return c
}

// SetClock overrides the time source (test hook).
func (c *Controller) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Snapshot returns the current limits without changing state.
func (c *Controller) Snapshot() Limits {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limits
}

// BreakerOpen reports whether the circuit breaker is currently open.
func (c *Controller) BreakerOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.breakerOpen
}

// Update ingests one metrics sample and returns the (possibly adjusted)
// limits. Behavior:
//   - breaker open: pin to minimal limits until the breaker window expires;
//   - error rate >= threshold: trip the breaker, halve throughput limits;
//   - within cooldown: hold limits steady (hysteresis, avoids oscillation);
//   - load > high or queue overflow: multiplicative decrease;
//   - load < low and empty queue: additive increase;
//   - lease tracks RTT and load, clamped to [MinLease, MaxLease].
func (c *Controller) Update(m Metrics) Limits {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()

	if c.breakerOpen {
		if now.Before(c.breakerUntil) {
			return c.limits // pinned minimal limits
		}
		c.breakerOpen = false // half-open: resume AIMD from reduced limits
	}

	if m.ErrorRate >= c.cfg.ErrorThreshold {
		c.breakerOpen = true
		c.breakerUntil = now.Add(c.cfg.BreakerDuration)
		c.limits.MaxInFlight = maxInt(c.cfg.MinInFlight, c.limits.MaxInFlight/2)
		c.limits.BatchSize = maxInt(c.cfg.MinBatch, c.limits.BatchSize/2)
		c.limits.LeaseDuration = c.cfg.MinLease
		c.lastChange = now
		return c.limits
	}

	if now.Before(c.lastChange.Add(c.cfg.Cooldown)) {
		return c.limits
	}

	changed := false
	switch {
	case m.Load > c.cfg.HighLoad || m.QueueDepth > c.limits.MaxInFlight:
		c.limits.MaxInFlight = maxInt(c.cfg.MinInFlight, int(float64(c.limits.MaxInFlight)*c.cfg.DecreaseFactor))
		c.limits.BatchSize = maxInt(c.cfg.MinBatch, int(float64(c.limits.BatchSize)*c.cfg.DecreaseFactor))
		changed = true
	case m.Load < c.cfg.LowLoad && m.QueueDepth == 0:
		c.limits.MaxInFlight = minInt(c.cfg.MaxInFlight, c.limits.MaxInFlight+c.cfg.IncreaseStep)
		c.limits.BatchSize = minInt(c.cfg.MaxBatch, c.limits.BatchSize+c.cfg.IncreaseStep)
		changed = true
	}

	// Lease: must comfortably cover the network RTT; under high load we
	// lengthen it to reduce re-election churn, under low load we shorten
	// it for faster failover.
	lease := c.cfg.BaseLease + 3*m.RTT
	if m.Load > c.cfg.HighLoad {
		lease = lease * 3 / 2
	}
	c.limits.LeaseDuration = clampDuration(lease, c.cfg.MinLease, c.cfg.MaxLease)

	if changed {
		c.lastChange = now
	}
	return c.limits
}

func clampDuration(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
