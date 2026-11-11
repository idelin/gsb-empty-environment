// Package tsblock implements an in-memory Gorilla-style block compressor for
// high-frequency metric samples.
//
// Timestamps are encoded with a variable-length delta-of-delta scheme and float64
// values are encoded with XOR compression that reuses the previous leading/trailing
// zero window. Every sample round-trips bit-for-bit, including NaN payloads and
// negative zero.
package tsblock

// Sample is one timestamped float64 observation.
type Sample struct {
	// T is the sample timestamp. The unit (nanoseconds, seconds, ...) is up to
	// the caller; only strict monotonicity is required.
	T uint64
	// V is the observed value, preserved bit-for-bit through a round trip.
	V float64
}
