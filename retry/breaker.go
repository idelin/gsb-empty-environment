package retry

import (
	"sync"
	"time"
)

// BreakerState is one of Closed, Open or HalfOpen.
type BreakerState int

// Circuit breaker states.
const (
	Closed BreakerState = iota
	Open
	HalfOpen
)

// String renders the state for logs and tests.
func (s BreakerState) String() string {
	switch s {
	case Open:
		return "Open"
	case HalfOpen:
		return "HalfOpen"
	default:
		return "Closed"
	}
}

// BreakerConfig configures the consecutive-failure circuit breaker.
type BreakerConfig struct {
	// FailureThreshold opens the breaker after this many consecutive failures.
	FailureThreshold int
	// OpenDuration is how long the breaker stays fully open before probing.
	OpenDuration time.Duration
	// HalfOpenMaxAttempts is the number of probe attempts allowed in HalfOpen.
	HalfOpenMaxAttempts int
}

// CircuitBreaker is a thread-safe consecutive-failure breaker.
type CircuitBreaker struct {
	cfg BreakerConfig

	mu               sync.Mutex
	state            BreakerState
	consecutiveFails int
	openedAt         time.Time
	halfOpenActive   int
	halfOpenSeen     int
}

// NewCircuitBreaker builds a breaker; a zero FailureThreshold disables it
// (the breaker stays Closed forever).
func NewCircuitBreaker(cfg BreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 0
	}
	if cfg.HalfOpenMaxAttempts <= 0 {
		cfg.HalfOpenMaxAttempts = 1
	}
	return &CircuitBreaker{cfg: cfg, state: Closed}
}

// Allow reports whether an attempt may start at now, applying the
// Open -> HalfOpen timeout transition.
func (c *CircuitBreaker) Allow(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.FailureThreshold == 0 {
		return true
	}
	switch c.state {
	case Closed:
		return true
	case Open:
		if now.Sub(c.openedAt) >= c.cfg.OpenDuration {
			c.state = HalfOpen
			c.halfOpenActive = 0
			c.halfOpenSeen = 0
			return true
		}
		return false
	case HalfOpen:
		if c.halfOpenSeen >= c.cfg.HalfOpenMaxAttempts {
			return false
		}
		return true
	}
	return true
}

// RecordSuccess closes the breaker and resets the failure streak.
func (c *CircuitBreaker) RecordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = Closed
	c.consecutiveFails = 0
	c.halfOpenActive = 0
	c.halfOpenSeen = 0
}

// RecordFailure feeds one failure; it may trip the breaker or reject a
// HalfOpen probe and re-open immediately.
func (c *CircuitBreaker) RecordFailure(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.FailureThreshold == 0 {
		return
	}
	switch c.state {
	case HalfOpen:
		c.trip(now)
	case Closed:
		c.consecutiveFails++
		if c.consecutiveFails >= c.cfg.FailureThreshold {
			c.trip(now)
		}
	}
}

// ProbeStarted tracks an in-flight HalfOpen probe so that the breaker
// admits at most HalfOpenMaxAttempts concurrent probes.
func (c *CircuitBreaker) ProbeStarted() {
	c.mu.Lock()
	if c.state == HalfOpen {
		c.halfOpenActive++
		c.halfOpenSeen++
	}
	c.mu.Unlock()
}

// ProbeFinished releases the probe slot.
func (c *CircuitBreaker) ProbeFinished() {
	c.mu.Lock()
	if c.halfOpenActive > 0 {
		c.halfOpenActive--
	}
	c.mu.Unlock()
}

// State returns the current state, performing the timeout transition at now.
func (c *CircuitBreaker) State(now time.Time) BreakerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == Open && c.cfg.FailureThreshold > 0 &&
		now.Sub(c.openedAt) >= c.cfg.OpenDuration {
		c.state = HalfOpen
		c.halfOpenActive = 0
		c.halfOpenSeen = 0
	}
	return c.state
}

// ConsecutiveFailures returns the current failure streak.
func (c *CircuitBreaker) ConsecutiveFailures() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.consecutiveFails
}

func (c *CircuitBreaker) trip(now time.Time) {
	c.state = Open
	c.openedAt = now
	c.consecutiveFails = 0
	c.halfOpenActive = 0
	c.halfOpenSeen = 0
}
