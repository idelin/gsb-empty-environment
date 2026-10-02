// Package tokenbucket implements a lock-free, CAS-based token bucket.
//
// The whole bucket state (last refill timestamp + available tokens) is packed
// into a single atomic.Uint64 and updated with Compare-And-Swap, so there is
// no mutex anywhere and millions of goroutines can call Acquire concurrently
// without lock contention, deadlocks, or torn state.
//
// State layout (64 bits):
//
//	high 32 bits: last refill time, milliseconds since bucket creation (uint32,
//	              wraparound-safe for intervals < ~24.8 days)
//	low 32 bits:  available tokens as a 16.16 fixed-point number
//	              (max 65535 whole tokens, 1/65536 token resolution)
package tokenbucket

import (
	"math"
	"sync/atomic"
	"time"
)

const (
	fracBits = 16
	oneToken = 1 << fracBits // 1.0 in 16.16 fixed point
	// MaxCapacity is the largest bucket capacity in whole tokens.
	MaxCapacity = math.MaxUint16
)

// TokenBucket is a lock-free rate limiter. It is safe for concurrent use.
type TokenBucket struct {
	state     atomic.Uint64 // packed: lastRefillMs<<32 | tokensFixed
	capacity  uint32        // bucket capacity, 16.16 fixed point
	ratePerMs uint64        // refill rate, 16.16 fixed-point tokens per millisecond
	epoch     time.Time
	now       func() time.Time    // injectable for deterministic tests
	sleep     func(time.Duration) // injectable for deterministic tests
}

// NewTokenBucket returns a full bucket with the given capacity (in tokens)
// that refills at ratePerSecond tokens per second. It panics if capacity is
// out of range [1, MaxCapacity] or the rate is not positive and representable.
func NewTokenBucket(capacity int, ratePerSecond float64) *TokenBucket {
	return newTokenBucket(capacity, ratePerSecond, time.Now, time.Sleep)
}

func newTokenBucket(capacity int, ratePerSecond float64, now func() time.Time, sleep func(time.Duration)) *TokenBucket {
	if capacity < 1 || capacity > MaxCapacity {
		panic("tokenbucket: capacity out of range")
	}
	ratePerMs := uint64(math.Round(ratePerSecond * oneToken / 1000))
	if ratePerSecond <= 0 || math.IsNaN(ratePerSecond) || ratePerMs == 0 {
		panic("tokenbucket: invalid rate")
	}
	b := &TokenBucket{
		capacity:  uint32(capacity) << fracBits,
		ratePerMs: ratePerMs,
		epoch:     now(),
		now:       now,
		sleep:     sleep,
	}
	// Start full.
	b.state.Store(pack(b.millis(), b.capacity))
	return b
}

func pack(ms uint32, tokens uint32) uint64 { return uint64(ms)<<32 | uint64(tokens) }

func unpack(state uint64) (ms uint32, tokens uint32) {
	return uint32(state >> 32), uint32(state)
}

// millis returns milliseconds since bucket creation. uint32 subtraction makes
// elapsed-time computation wraparound-safe.
func (b *TokenBucket) millis() uint32 {
	return uint32(b.now().Sub(b.epoch) / time.Millisecond)
}

// refill returns the token count after elapsed milliseconds, clamped to capacity.
func (b *TokenBucket) refill(tokens uint32, elapsedMs uint32) uint32 {
	sum := uint64(tokens) + uint64(elapsedMs)*b.ratePerMs
	if sum >= uint64(b.capacity) {
		return b.capacity
	}
	return uint32(sum)
}

// TryAcquire attempts to take one token without blocking.
func (b *TokenBucket) TryAcquire() bool {
	for {
		old := b.state.Load()
		lastMs, tokens := unpack(old)
		nowMs := b.millis()
		tokens = b.refill(tokens, nowMs-lastMs)
		if tokens < oneToken {
			return false
		}
		if b.state.CompareAndSwap(old, pack(nowMs, tokens-oneToken)) {
			return true
		}
		// Lost the CAS race: another goroutine updated the state. Retry.
	}
}

// Acquire blocks until one token is available and takes it.
func (b *TokenBucket) Acquire() {
	for {
		old := b.state.Load()
		lastMs, tokens := unpack(old)
		nowMs := b.millis()
		tokens = b.refill(tokens, nowMs-lastMs)
		if tokens >= oneToken {
			if b.state.CompareAndSwap(old, pack(nowMs, tokens-oneToken)) {
				return
			}
			continue
		}
		// Best-effort: persist the advanced clock so other goroutines and the
		// next iteration see fresh state. Losing this CAS is harmless.
		b.state.CompareAndSwap(old, pack(nowMs, tokens))
		// Sleep exactly the time needed for the missing fraction of a token.
		deficit := uint64(oneToken - tokens)
		waitMs := (deficit + b.ratePerMs - 1) / b.ratePerMs
		b.sleep(time.Duration(waitMs) * time.Millisecond)
	}
}

// Tokens returns the current (refilled) number of available tokens.
// It is a read-only snapshot and does not modify the state.
func (b *TokenBucket) Tokens() float64 {
	old := b.state.Load()
	lastMs, tokens := unpack(old)
	tokens = b.refill(tokens, b.millis()-lastMs)
	return float64(tokens) / oneToken
}
