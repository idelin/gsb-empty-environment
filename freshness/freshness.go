// Package freshness computes the corrected age and remaining freshness of a
// cached authentication response for a security-agent cache host.
//
// The corrected initial age follows the HTTP caching model (RFC 9111):
//
//	apparent_age = max(0, received_at - date_value)
//	age_value    = normalized Age header (saturated, overflow capped)
//	corrected    = max(apparent_age, age_value + response_delay)
//
// All time differences are clamped at zero, every arithmetic operation is
// saturating, and the wall clock is injectable. Once an age has been observed,
// a later clock regression can never lower the recorded age.
package freshness

import (
	"sync"
	"time"
)

// maxSaturatingDuration is the largest representable time.Duration.
const maxSaturatingDuration = time.Duration(1<<63 - 1)

// ageOverflowSeconds is the mandatory cap for an Age header whose value cannot
// be represented as a 31-bit non-negative integer. It already exceeds any
// realistic maximum lifetime, so the entry is treated as stale.
const ageOverflowSeconds = 1 << 31

// Clock returns the current wall-clock time. Implementations must be safe for
// concurrent use. A nil Clock is replaced by a clock backed by time.Now.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts an ordinary function to the Clock interface.
type ClockFunc func() time.Time

// Now calls the wrapped function.
func (f ClockFunc) Now() time.Time { return f() }

// systemClock is the default Clock used when a host injects none.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Params carries every input required to record a response and derive its
// corrected initial age. All times may use any monotonic-clock-free wall-clock
// representation; comparisons rely only on time.Time ordering.
type Params struct {
	// SentAt is when the host sent the request.
	SentAt time.Time
	// ReceivedAt is when the host received the response.
	ReceivedAt time.Time
	// Date is the value of the response Date header. When HasDate is false the
	// Date is absent and is treated as equal to ReceivedAt (zero apparent age).
	Date time.Time
	// HasDate reports whether the response carried a Date header.
	HasDate bool
	// AgeSeconds is the parsed value of the response Age header in seconds.
	AgeSeconds uint64
	// HasAge reports whether the response carried an Age header.
	HasAge bool
	// MaxLifetime bounds how long the cached authentication result may be used.
	MaxLifetime time.Duration
}

// Entry tracks the freshness of one cached authentication response. Its methods
// are safe for concurrent use.
type Entry struct {
	clock        Clock
	receivedAt   time.Time
	maxLifetime  time.Duration
	initialAge   time.Duration
	lastAge      time.Duration
	lastObserved time.Time
	mu           sync.Mutex
}

// NewEntry records a response and computes its corrected initial age.
//
// A nil clock defaults to the system clock. When ReceivedAt is zero, the
// current clock time is used so hosts that omit timestamps still get a defined
// age. Every difference and sum is non-negative and saturated.
func NewEntry(clock Clock, p Params) *Entry {
	if clock == nil {
		clock = systemClock{}
	}
	receivedAt := p.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = clock.Now()
	}
	initial := CorrectedInitialAge(clock, p)
	return &Entry{
		clock:        clock,
		receivedAt:   receivedAt,
		maxLifetime:  p.MaxLifetime,
		initialAge:   initial,
		lastAge:      initial,
		lastObserved: receivedAt,
	}
}

// CorrectedInitialAge computes the corrected age at the moment the response was
// received without retaining any state. It is exported so hosts using their own
// storage can reuse the exact RFC 9111 derivation.
func CorrectedInitialAge(clock Clock, p Params) time.Duration {
	if clock == nil {
		clock = systemClock{}
	}
	receivedAt := p.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = clock.Now()
	}

	responseDelay := time.Duration(0)
	if !p.SentAt.IsZero() {
		responseDelay = nonNegative(receivedAt.Sub(p.SentAt))
	}

	apparentAge := time.Duration(0)
	if p.HasDate && !p.Date.IsZero() {
		apparentAge = nonNegative(receivedAt.Sub(p.Date))
	}

	ageValue := time.Duration(0)
	if p.HasAge {
		ageValue = ageSecondsToDuration(p.AgeSeconds)
	}

	corrected := maxDuration(apparentAge, saturatingAdd(ageValue, responseDelay))
	return corrected
}

// Age returns the current corrected age. The value is monotonic for the life of
// the entry: if the injected clock moves backwards, Age keeps the highest value
// previously observed instead of decreasing.
func (e *Entry) Age() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ageAt(e.clock.Now())
}

// Remaining returns how long the cached result stays fresh, saturated at zero.
// A non-positive maximum lifetime means the entry is immediately stale.
func (e *Entry) Remaining() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return saturatingSub(e.maxLifetime, e.ageAt(e.clock.Now()))
}

// Fresh reports whether the cached authentication result may still be used.
func (e *Entry) Fresh() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxLifetime > 0 && e.ageAt(e.clock.Now()) < e.maxLifetime
}

// InitialAge returns the corrected age computed when the response was received.
func (e *Entry) InitialAge() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.initialAge
}

// MaxLifetime returns the configured maximum lifetime.
func (e *Entry) MaxLifetime() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxLifetime
}

// ReceivedAt returns the effective response-reception timestamp.
func (e *Entry) ReceivedAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.receivedAt
}

// ageAt must be called with e.mu held. Resident time since the previous
// observation is clamped to zero and accumulated, so the age equals the initial
// corrected age plus every positive interval that elapses. A clock regression
// contributes zero resident time, which can never lower the recorded age.
func (e *Entry) ageAt(now time.Time) time.Duration {
	resident := nonNegative(now.Sub(e.lastObserved))
	e.lastAge = saturatingAdd(e.lastAge, resident)
	e.lastObserved = now
	return e.lastAge
}

// ageSecondsToDuration converts a parsed Age header to a duration, applying the
// RFC 9111 overflow cap and saturating at the maximum representable duration.
func ageSecondsToDuration(seconds uint64) time.Duration {
	if seconds >= ageOverflowSeconds {
		seconds = ageOverflowSeconds
	}
	// seconds <= 2^31 here, so the multiplication cannot overflow int64.
	return time.Duration(seconds) * time.Second
}

// nonNegative clamps a duration to the non-negative range.
func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// saturatingAdd returns a+b without ever overflowing.
func saturatingAdd(a, b time.Duration) time.Duration {
	if b > 0 && a > maxSaturatingDuration-b {
		return maxSaturatingDuration
	}
	if b < 0 && a < -maxSaturatingDuration-b {
		return -maxSaturatingDuration
	}
	return a + b
}

// saturatingSub returns a-b without ever overflowing and clamps the result to
// zero so remaining lifetimes never become negative.
func saturatingSub(a, b time.Duration) time.Duration {
	if b >= a {
		return 0
	}
	return a - b
}

// maxDuration returns the larger of two durations.
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
