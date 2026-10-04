package paxos

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

var (
	// ErrOverloaded is returned when the in-flight limit is reached and the
	// controller sheds load instead of queueing unboundedly.
	ErrOverloaded = errors.New("adaptive: overloaded, shedding load")
	// ErrBreakerOpen is returned while the circuit breaker is cooling down.
	ErrBreakerOpen = errors.New("adaptive: circuit breaker open")
)

// Config tunes the adaptive controller.
type Config struct {
	MinLimit         int           // hard floor for the in-flight limit
	MaxLimit         int           // hard ceiling for the in-flight limit
	InitialLimit     int           // starting in-flight limit
	TargetLoad       float64       // load (0..1) above which we back off
	DecreaseFactor   float64       // multiplicative decrease factor (0,1)
	FailureThreshold int           // consecutive failures before the breaker opens
	BreakerCooldown  time.Duration // base cooldown before half-open probe
	MinBatch         int           // smallest batch under full load
	MaxBatch         int           // largest batch when idle
	MinLease         time.Duration // shortest leader lease
	MaxLease         time.Duration // longest leader lease
	LeaseFactor      int           // lease = smoothed RTT * LeaseFactor
}

func DefaultConfig() Config {
	return Config{
		MinLimit:         1,
		MaxLimit:         64,
		InitialLimit:     8,
		TargetLoad:       0.8,
		DecreaseFactor:   0.5,
		FailureThreshold: 3,
		BreakerCooldown:  500 * time.Millisecond,
		MinBatch:         1,
		MaxBatch:         32,
		MinLease:         2 * time.Second,
		MaxLease:         30 * time.Second,
		LeaseFactor:      10,
	}
}

func (c *Config) normalize() {
	if c.MinLimit < 1 {
		c.MinLimit = 1
	}
	if c.MaxLimit < c.MinLimit {
		c.MaxLimit = c.MinLimit
	}
	if c.InitialLimit < c.MinLimit {
		c.InitialLimit = c.MinLimit
	}
	if c.InitialLimit > c.MaxLimit {
		c.InitialLimit = c.MaxLimit
	}
	if c.TargetLoad <= 0 || c.TargetLoad > 1 {
		c.TargetLoad = 0.8
	}
	if c.DecreaseFactor <= 0 || c.DecreaseFactor >= 1 {
		c.DecreaseFactor = 0.5
	}
	if c.FailureThreshold < 1 {
		c.FailureThreshold = 1
	}
	if c.MinBatch < 1 {
		c.MinBatch = 1
	}
	if c.MaxBatch < c.MinBatch {
		c.MaxBatch = c.MinBatch
	}
	if c.MinLease <= 0 {
		c.MinLease = time.Second
	}
	if c.MaxLease < c.MinLease {
		c.MaxLease = c.MinLease
	}
	if c.LeaseFactor < 1 {
		c.LeaseFactor = 1
	}
}

// Controller self-tunes internal parameters from external pressure signals:
//
//   - in-flight limit: AIMD (additive increase when saturated and load is
//     below target, multiplicative decrease on overload or failure)
//   - circuit breaker: opens after consecutive failures, half-opens after a
//     jittered cooldown to avoid thundering-herd retries (avalanche control)
//   - batch size: linearly shrinks as load rises (load-aware)
//   - leader lease: tracks smoothed RTT, clamped to [MinLease, MaxLease]
type Controller struct {
	mu  sync.Mutex
	cfg Config

	limit    int
	inflight int

	failures     int // consecutive failures
	breakerOpen  bool
	breakerUntil time.Time

	rttAvg time.Duration // EWMA of observed round trips

	loadFn func() float64
	now    func() time.Time
	jitter func() time.Duration

	tunerStop chan struct{}
	tunerWg   sync.WaitGroup
	tunerOnce sync.Once
	stopOnce  sync.Once
}

// NewController builds a controller. loadFn reports current external load in
// [0,1] (values are clamped); nil means "no signal" (treated as 0).
func NewController(cfg Config, loadFn func() float64) *Controller {
	cfg.normalize()
	c := &Controller{
		cfg:    cfg,
		limit:  cfg.InitialLimit,
		loadFn: loadFn,
		now:    time.Now,
	}
	c.jitter = func() time.Duration {
		return time.Duration(rand.Int63n(int64(cfg.BreakerCooldown) + 1))
	}
	return c
}

// SetClock overrides the time source (tests).
func (c *Controller) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// SetJitter overrides the breaker-cooldown jitter source (tests).
func (c *Controller) SetJitter(j func() time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jitter = j
}

func (c *Controller) loadLocked() float64 {
	if c.loadFn == nil {
		return 0
	}
	l := c.loadFn()
	if l < 0 {
		return 0
	}
	return l
}

// Acquire admits one unit of work or sheds it. It never blocks: under
// pressure the caller gets ErrOverloaded/ErrBreakerOpen immediately.
func (c *Controller) Acquire() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.breakerOpen {
		if c.now().Before(c.breakerUntil) {
			return ErrBreakerOpen
		}
		// Half-open: allow a probe through.
		c.breakerOpen = false
	}
	if c.inflight >= c.limit {
		return ErrOverloaded
	}
	c.inflight++
	return nil
}

// Release returns one unit of work and feeds the outcome into the adaptive
// loop: AIMD limit adjustment, RTT smoothing, and breaker accounting.
func (c *Controller) Release(success bool, rtt time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	saturated := c.inflight >= c.limit
	if c.inflight > 0 {
		c.inflight--
	}
	if rtt > 0 {
		if c.rttAvg == 0 {
			c.rttAvg = rtt
		} else {
			c.rttAvg = c.rttAvg*7/8 + rtt/8
		}
	}
	if !success {
		c.failures++
		c.decreaseLocked()
		if c.failures >= c.cfg.FailureThreshold && !c.breakerOpen {
			c.breakerOpen = true
			c.breakerUntil = c.now().Add(c.cfg.BreakerCooldown + c.jitter())
		}
		return
	}
	c.failures = 0
	if c.loadLocked() > c.cfg.TargetLoad {
		c.decreaseLocked()
		return
	}
	if saturated {
		c.increaseLocked()
	}
}

func (c *Controller) decreaseLocked() {
	next := int(float64(c.limit) * c.cfg.DecreaseFactor)
	if next < c.cfg.MinLimit {
		next = c.cfg.MinLimit
	}
	c.limit = next
}

func (c *Controller) increaseLocked() {
	if c.limit < c.cfg.MaxLimit {
		c.limit++
	}
}

// Limit is the current adaptive in-flight ceiling.
func (c *Controller) Limit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limit
}

// Inflight is the number of admitted, not-yet-released units of work.
func (c *Controller) Inflight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}

// BreakerOpen reports whether the breaker is currently rejecting work.
func (c *Controller) BreakerOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.breakerOpen && c.now().Before(c.breakerUntil)
}

// BatchSize scales linearly with load: MaxBatch when idle, MinBatch at or
// above full load.
func (c *Controller) BatchSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	load := c.loadLocked()
	if load > 1 {
		load = 1
	}
	span := c.cfg.MaxBatch - c.cfg.MinBatch
	return c.cfg.MinBatch + int(float64(span)*(1-load))
}

// LeaseDuration derives the leader lease from the smoothed RTT, clamped.
func (c *Controller) LeaseDuration() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.rttAvg * time.Duration(c.cfg.LeaseFactor)
	if d < c.cfg.MinLease {
		return c.cfg.MinLease
	}
	if d > c.cfg.MaxLease {
		return c.cfg.MaxLease
	}
	return d
}

// StartTuner launches a background loop that keeps adapting the limit to the
// load signal even when no traffic flows (e.g. shrinking before a predicted
// burst drains in-flight capacity). Stop terminates it; no goroutine leaks.
func (c *Controller) StartTuner(interval time.Duration) {
	c.tunerOnce.Do(func() {
		c.tunerStop = make(chan struct{})
		c.tunerWg.Add(1)
		go func() {
			defer c.tunerWg.Done()
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-c.tunerStop:
					return
				case <-t.C:
					c.mu.Lock()
					if c.loadLocked() > c.cfg.TargetLoad {
						c.decreaseLocked()
					} else if c.failures == 0 {
						c.increaseLocked()
					}
					c.mu.Unlock()
				}
			}
		}()
	})
}

// Stop shuts the tuner down and waits for it to exit.
func (c *Controller) Stop() {
	c.stopOnce.Do(func() {
		if c.tunerStop != nil {
			close(c.tunerStop)
		}
	})
	c.tunerWg.Wait()
}
