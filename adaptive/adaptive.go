// Package adaptive implements a load-aware controller that self-tunes an
// internal concurrency threshold from external pressure metrics. It uses
// EWMA-smoothed load, additive-increase/multiplicative-decrease (AIMD)
// scaling, a hysteresis band and a decrease cooldown to avoid oscillation
// and retry-storm avalanches while maximizing resource utilization.
package adaptive

import (
	"context"
	"sync"
	"time"
)

// Metrics is one external pressure sample.
type Metrics struct {
	Load   float64 // utilization/pressure in [0,1]; values outside are clamped
	Errors int     // failures observed since the previous sample
}

// Options configures a Controller.
type Options struct {
	Min       int           // threshold floor, >= 1
	Max       int           // threshold ceiling, >= Min
	Initial   int           // starting threshold, clamped into [Min, Max]
	HighWater float64       // EWMA load above this triggers multiplicative decrease
	LowWater  float64       // EWMA load below this triggers additive increase
	Beta      float64       // multiplicative decrease factor in (0,1)
	Step      int           // additive increase step, >= 1
	Alpha     float64       // EWMA smoothing factor in (0,1]
	Cooldown  time.Duration // minimum interval between decreases
	Now       func() time.Time
}

// DefaultOptions returns a sane production profile.
func DefaultOptions() Options {
	return Options{
		Min: 1, Max: 1024, Initial: 64,
		HighWater: 0.8, LowWater: 0.4,
		Beta: 0.5, Step: 8, Alpha: 0.3,
		Cooldown: 200 * time.Millisecond,
		Now:      time.Now,
	}
}

func (o *Options) normalize() {
	if o.Min < 1 {
		o.Min = 1
	}
	if o.Max < o.Min {
		o.Max = o.Min
	}
	if o.Initial < o.Min {
		o.Initial = o.Min
	}
	if o.Initial > o.Max {
		o.Initial = o.Max
	}
	if o.HighWater <= 0 || o.HighWater > 1 {
		o.HighWater = 0.8
	}
	if o.LowWater < 0 || o.LowWater >= o.HighWater {
		o.LowWater = o.HighWater / 2
	}
	if o.Beta <= 0 || o.Beta >= 1 {
		o.Beta = 0.5
	}
	if o.Step < 1 {
		o.Step = 1
	}
	if o.Alpha <= 0 || o.Alpha > 1 {
		o.Alpha = 0.3
	}
	if o.Cooldown < 0 {
		o.Cooldown = 0
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Controller is safe for concurrent use.
type Controller struct {
	mu        sync.Mutex
	opts      Options
	threshold int
	ewma      float64
	sampled   bool
	inflight  int
	lastDrop  time.Time

	stopCh    chan struct{}
	doneCh    chan struct{}
	started   bool
	closeOnce sync.Once
}

// NewController builds a Controller with normalized options.
func NewController(opts Options) *Controller {
	opts.normalize()
	return &Controller{
		opts:      opts,
		threshold: opts.Initial,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Sample feeds one pressure reading, updates the smoothed load and rescales
// the threshold. It returns the threshold in effect after the adjustment.
func (c *Controller) Sample(m Metrics) int {
	load := m.Load
	if load < 0 {
		load = 0
	}
	if load > 1 {
		load = 1
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.sampled {
		c.ewma = load
		c.sampled = true
	} else {
		c.ewma = c.opts.Alpha*load + (1-c.opts.Alpha)*c.ewma
	}

	switch {
	case c.ewma > c.opts.HighWater:
		// Multiplicative decrease, rate-limited by the cooldown so a
		// sustained spike cannot thrash the threshold (avalanche guard).
		now := c.opts.Now()
		if now.Sub(c.lastDrop) >= c.opts.Cooldown {
			next := int(float64(c.threshold) * c.opts.Beta)
			if next < c.opts.Min {
				next = c.opts.Min
			}
			c.threshold = next
			c.lastDrop = now
		}
	case c.ewma < c.opts.LowWater:
		// Additive increase: reclaim capacity slowly to avoid a
		// thundering-herd rebound.
		next := c.threshold + c.opts.Step
		if next > c.opts.Max {
			next = c.opts.Max
		}
		c.threshold = next
	}
	return c.threshold
}

// Limit returns the current admission threshold.
func (c *Controller) Limit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threshold
}

// Load returns the EWMA-smoothed external load in [0,1].
func (c *Controller) Load() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ewma
}

// TryAcquire admits one unit of work if inflight < threshold.
func (c *Controller) TryAcquire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight >= c.threshold {
		return false
	}
	c.inflight++
	return true
}

// Release returns one unit of work. Releasing without a matching acquire
// is a no-op and never drives inflight negative.
func (c *Controller) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight > 0 {
		c.inflight--
	}
}

// Inflight returns the number of admitted, not-yet-released units.
func (c *Controller) Inflight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}

// Start launches a background sampler that calls sampler every interval
// until ctx is cancelled or Close is called. The goroutine always exits;
// Close blocks until it has.
func (c *Controller) Start(ctx context.Context, interval time.Duration, sampler func() Metrics) {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	go func() {
		defer close(c.doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stopCh:
				return
			case <-ticker.C:
				c.Sample(sampler())
			}
		}
	}()
}

// Close stops the background sampler (if any) and waits for it to exit.
// It is idempotent and safe to call without Start.
func (c *Controller) Close() {
	c.closeOnce.Do(func() {
		close(c.stopCh)
		c.mu.Lock()
		started := c.started
		c.mu.Unlock()
		if started {
			<-c.doneCh
		}
	})
}
