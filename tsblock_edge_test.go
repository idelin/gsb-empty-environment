package tsblock

import (
	"math"
	"math/rand"
	"testing"
)

func TestExtremeTimestamps(t *testing.T) {
	// Exhaust the full uint64 timestamp range, including wrap-around-sized
	// deltas that only the 64-bit bucket can represent.
	samples := []Sample{
		{T: 0, V: 1},
		{T: 1, V: 2},
		{T: 1 << 40, V: 3},
		{T: (1 << 63), V: 4},
		{T: (1<<64 - 1), V: 5},
	}
	_, got := encodeAll(t, samples, DefaultMaxBlockSize)
	assertRoundTrip(t, samples, got)
}

func TestFullWordXorEscape(t *testing.T) {
	// Consecutive values whose XOR has no leading or trailing zero force the
	// full-word escape descriptor (lead=0, meaningful=64).
	samples := []Sample{
		{T: 1, V: math.Float64frombits(0x0000000000000000)},
		{T: 2, V: math.Float64frombits(0xffffffffffffffff)},
		{T: 3, V: math.Float64frombits(0x0000000000000001)},
		{T: 4, V: math.Float64frombits(0xfffffffffffffffe)},
		{T: 5, V: math.Float64frombits(0x8000000000000001)},
	}
	_, got := encodeAll(t, samples, DefaultMaxBlockSize)
	assertRoundTrip(t, samples, got)
}

func TestRandomBitPatterns(t *testing.T) {
	// Independent random float bits stress every XOR window path, while
	// remaining strictly bit-exact on the round trip.
	rng := rand.New(rand.NewSource(0xabcdef))
	const n = 10000
	samples := make([]Sample, n)
	ts := uint64(1_000_000)
	for i := range samples {
		ts += uint64(1 + rng.Intn(1000))
		samples[i] = Sample{T: ts, V: math.Float64frombits(rng.Uint64())}
	}
	frames, got := encodeAll(t, samples, DefaultMaxBlockSize)
	assertRoundTrip(t, samples, got)
	compressionRatio(t, "random-bits", samples, frames)
}

func TestConstantValueAndClock(t *testing.T) {
	// Best case for the codec: identical values and a fixed tick.
	const n = 5000
	samples := make([]Sample, n)
	for i := range samples {
		samples[i] = Sample{T: uint64(1000 + i*100), V: 42.5}
	}
	frames, got := encodeAll(t, samples, DefaultMaxBlockSize)
	assertRoundTrip(t, samples, got)
	ratio := compressionRatio(t, "constant", samples, frames)
	if ratio > 0.1 {
		t.Fatalf("constant stream ratio %.3f unexpectedly high", ratio)
	}
}
