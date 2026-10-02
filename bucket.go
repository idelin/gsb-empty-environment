// Package tokenbucket implements a high-performance, lock-free token bucket
// based on a single atomic Compare-And-Swap word.
//
// The entire mutable state (available tokens + last refill timestamp) is
// packed into one uint64 and updated with atomic CAS, so there is no mutex
// anywhere in the implementation and millions of goroutines can call
// Acquire concurrently without lock contention, deadlocks, or torn state.
//
// State layout (64 bits):
//
//	[ high 32 bits: available tokens ][ low 32 bits: last refill tick ]
//
// Time is quantized into "ticks": exactly one token is refilled per tick
// (tickNanos = 1e9 / rate nanoseconds). Because refill amounts are whole
// ticks, the quantization is exact — no fractional-token remainder is ever
// lost, and the total number of granted tokens matches theory precisely.
package tokenbucket

import (
	"fmt"
	"sync/atomic"
	"time"
)

// processStart anchors a monotonic clock that is immune to wall-clock jumps.
var processStart = time.Now()

func monotonicNanos() int64 { return time.Since(processStart).Nanoseconds() }

// Bucket is a lock-free token bucket. The zero value is not usable; use New.
type Bucket struct {
	state     atomic.Uint64 // packed: tokens<<32 | lastTick
	capacity  uint32
	tickNanos uint64 // nanoseconds per token (one token refilled per tick)
	now       func() int64
}

// New returns a Bucket that refills rate tokens per second up to capacity,
// starting full. rate must be > 0.
func New(rate float64, capacity uint32) *Bucket {
	return newBucket(rate, capacity, monotonicNanos)
}

func newBucket(rate float64, capacity uint32, now func() int64) *Bucket {
	if rate <= 0 {
		panic(fmt.Sprintf("tokenbucket: rate must be positive, got %v", rate))
	}
	tick := uint64(1e9 / rate)
	if tick == 0 { // rate >= 1e9 tokens/s: clamp to 1ns per token
		tick = 1
	}
	b := &Bucket{capacity: capacity, tickNanos: tick, now: now}
	b.state.Store(pack(capacity, b.currentTick()))
	return b
}

func pack(tokens, tick uint32) uint64 { return uint64(tokens)<<32 | uint64(tick) }

func (b *Bucket) currentTick() uint32 {
	return uint32(uint64(b.now()) / b.tickNanos)
}

// Acquire attempts to consume one token. It returns true if a token was
// granted, false if the bucket is empty. It never blocks and never holds a
// lock: contenders retry via CAS and exactly one wins each token.
func (b *Bucket) Acquire() bool {
	for {
		// Load the state BEFORE reading the clock. Any lastTick observed
		// in s was produced by a clock read that happened-before the store
		// of s, which in turn happened-before this load; our subsequent
		// clock read is therefore guaranteed nowTick >= lastTick, so the
		// refill delta can never underflow.
		s := b.state.Load()
		nowTick := b.currentTick()

		tokens := uint32(s >> 32)
		lastTick := uint32(s)

		// Elapsed whole ticks each refill exactly one token. The uint32
		// subtraction is wrap-safe for pauses shorter than 2^31 ticks.
		var refill uint32
		if nowTick > lastTick {
			refill = nowTick - lastTick
		}
		avail := uint64(tokens) + uint64(refill)
		if avail > uint64(b.capacity) {
			avail = uint64(b.capacity)
		}
		if avail == 0 {
			return false
		}

		newLast := lastTick
		if refill > 0 {
			// Consume the elapsed ticks. Ticks beyond capacity are
			// discarded, which is correct: a full bucket cannot
			// accumulate more than capacity tokens.
			newLast = nowTick
		}
		if b.state.CompareAndSwap(s, pack(uint32(avail-1), newLast)) {
			return true
		}
		// Lost the race: another goroutine updated the state.
		// Retry against the fresh state.
	}
}

// Available returns a consistent snapshot of the currently available tokens
// (including refills due to elapsed time). It does not mutate state.
func (b *Bucket) Available() uint32 {
	s := b.state.Load()
	nowTick := b.currentTick()
	var refill uint32
	if lastTick := uint32(s); nowTick > lastTick {
		refill = nowTick - lastTick
	}
	avail := uint64(uint32(s>>32)) + uint64(refill)
	if avail > uint64(b.capacity) {
		avail = uint64(b.capacity)
	}
	return uint32(avail)
}

// Capacity returns the maximum number of tokens the bucket can hold.
func (b *Bucket) Capacity() uint32 { return b.capacity }
