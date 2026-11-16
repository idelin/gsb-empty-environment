package netsec

import (
	"net"
	"strings"
	"sync"
	"time"
)

// ThrottleConfig controls RAKP failure throttling per source address.
//
// A zero Window or MaxFailures selects the default (60 seconds and 10
// failures). A negative MaxFailures disables throttling.
type ThrottleConfig struct {
	Window      time.Duration
	MaxFailures int
}

// DefaultThrottleConfig is the policy required by the firmware: more than 10
// failures from one source in 60 seconds blocks subsequent session requests.
var DefaultThrottleConfig = ThrottleConfig{
	Window:      60 * time.Second,
	MaxFailures: 10,
}

func (c ThrottleConfig) withDefaults() ThrottleConfig {
	if c.Window == 0 {
		c.Window = DefaultThrottleConfig.Window
	}
	if c.MaxFailures == 0 {
		c.MaxFailures = DefaultThrottleConfig.MaxFailures
	}
	return c
}

// FailureThrottle records RAKP failures in a per-source sliding window.
// It is safe for concurrent use.
type FailureThrottle struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	failures map[string][]time.Time
}

// NewFailureThrottle creates a throttle. The zero value is not usable; use
// this constructor.
func NewFailureThrottle(config ThrottleConfig) *FailureThrottle {
	config = config.withDefaults()
	return &FailureThrottle{
		window:   config.Window,
		max:      config.MaxFailures,
		failures: make(map[string][]time.Time),
	}
}

// Blocked reports whether the source has more than the allowed number of RAKP
// failures inside the window. The request that produces the (MaxFailures+1)th
// failure is still processed; the next session request is discarded.
func (t *FailureThrottle) Blocked(source string, now time.Time) bool {
	if t == nil || t.max < 0 {
		return false
	}
	source = normalizeSource(source)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(source, now)
	return len(t.failures[source]) > t.max
}

// RecordFailure records one failed RAKP exchange for the source.
func (t *FailureThrottle) RecordFailure(source string, now time.Time) {
	if t == nil || t.max < 0 {
		return
	}
	source = normalizeSource(source)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(source, now)
	t.failures[source] = append(t.failures[source], now)
}

// FailureCount returns the number of failures currently inside the window.
func (t *FailureThrottle) FailureCount(source string, now time.Time) int {
	if t == nil || t.max < 0 {
		return 0
	}
	source = normalizeSource(source)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(source, now)
	return len(t.failures[source])
}

// Reset removes the failure history for one source.
func (t *FailureThrottle) Reset(source string) {
	if t == nil {
		return
	}
	source = normalizeSource(source)
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, source)
}

func (t *FailureThrottle) pruneLocked(source string, now time.Time) {
	entries := t.failures[source]
	if len(entries) == 0 {
		return
	}
	cutoff := now.Add(-t.window)
	first := 0
	for first < len(entries) && !entries[first].After(cutoff) {
		first++
	}
	switch {
	case first == len(entries):
		delete(t.failures, source)
	case first > 0:
		copy(entries, entries[first:])
		t.failures[source] = entries[:len(entries)-first]
	}
}

// normalizeSource treats host:port and bare-host spellings of the same
// address as one throttling key.
func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if host, _, err := net.SplitHostPort(source); err == nil && host != "" {
		return host
	}
	return source
}
