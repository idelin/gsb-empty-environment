// Package counter reasons about unsigned, fixed-width counters that may wrap
// around multiple times between two samples, and that may be reset by a
// restart (tracked via a generation marker).
package counter

import (
	"errors"
	"fmt"
	"math"
)

// Sample is a single observation of a counter.
type Sample struct {
	// Time is the observation time in seconds (any monotonic epoch).
	Time float64
	// Value is the raw counter reading; only the low Width bits are
	// meaningful.
	Value uint64
	// Generation identifies the counter incarnation. A change between
	// adjacent samples means the counter was restarted (reset), so no
	// increment can be inferred across the boundary.
	Generation uint64
}

// Interval describes every increment between two same-generation samples
// that is consistent with a maximum growth rate.
//
// The admissible increments are Min, Min+M, Min+2M, ..., Max where M is the
// counter modulus 2^width: the counter may have wrapped anywhere from
// MinWraps to MaxWraps times. Use Values to enumerate them explicitly.
type Interval struct {
	Min      uint64 // smallest admissible increment (zero extra wraps)
	Max      uint64 // largest admissible increment (MaxWraps extra wraps)
	MinWraps uint64 // minimum number of full wraparounds
	MaxWraps uint64 // maximum number of full wraparounds
	Modulus  uint64 // counter modulus 2^width (0 means 2^64)
}

// Values enumerates every admissible increment: Min + k*Modulus for
// k in [MinWraps, MaxWraps]. For a 64-bit counter (Modulus == 0) the
// modulus does not fit in uint64, so Values returns the increments for
// wrap counts below 2^64 only if they are representable; in practice the
// rate limit keeps the count tiny.
func (iv Interval) Values() []uint64 {
	if iv.MaxWraps > iv.MinWraps && iv.MaxWraps-iv.MinWraps > 1<<20 {
		// Guard against pathological allocations; callers can still
		// derive values from Min/Max/Modulus.
		return nil
	}
	var out []uint64
	for k := iv.MinWraps; k <= iv.MaxWraps; k++ {
		out = append(out, iv.Min+k*iv.Modulus)
		if k == math.MaxUint64 {
			break
		}
	}
	return out
}

var (
	// ErrUnexplainable is returned when no number of wraparounds can
	// reconcile the two readings with the maximum growth rate.
	ErrUnexplainable = errors.New("counter: jump cannot be explained by any number of wraparounds")
	// ErrNonPositiveDuration is returned when samples are not strictly
	// ordered in time.
	ErrNonPositiveDuration = errors.New("counter: sample times must be strictly increasing")
	// ErrInvalidWidth is returned for bit widths outside [1, 64].
	ErrInvalidWidth = errors.New("counter: width must be between 1 and 64 bits")
)

// ErrRestart is returned (as a wrapped error value comparable with ==)
// when the generation marker changed, i.e. the counter was restarted and
// the increment across the boundary is unknown.
var ErrRestart = errors.New("counter: generation changed, counter restarted")

// Delta computes every increment between prev and curr that is consistent
// with a counter of the given bit width growing at most maxRate units per
// second.
//
//   - If the generation markers differ, the counter restarted and the
//     increment is unknowable: returns ErrRestart.
//   - Otherwise the raw modular difference base = (curr-prev) mod 2^width
//     is the zero-wrap increment; any base + k*2^width that stays within
//     maxRate*dt is also admissible, because the counter may have wrapped
//     k extra times. All of them are returned as an Interval.
//   - If even the zero-wrap increment exceeds the rate budget, no number
//     of wraps can explain the jump (wraps only make increments larger):
//     returns ErrUnexplainable.
func Delta(prev, curr Sample, width uint, maxRate float64) (Interval, error) {
	if width < 1 || width > 64 {
		return Interval{}, ErrInvalidWidth
	}
	if curr.Generation != prev.Generation {
		return Interval{}, ErrRestart
	}
	dt := curr.Time - prev.Time
	if !(dt > 0) {
		return Interval{}, ErrNonPositiveDuration
	}
	if maxRate < 0 {
		return Interval{}, fmt.Errorf("counter: negative max rate %v", maxRate)
	}

	modulus := uint64(1) << width // 0 when width == 64, i.e. modulus 2^64
	mask := modulus - 1           // all-ones when width == 64

	base := (curr.Value - prev.Value) & mask
	maxInc := uint64(maxRate * dt) // floor; negative rates rejected above

	if base > maxInc {
		return Interval{}, ErrUnexplainable
	}

	iv := Interval{Min: base, Max: base, MinWraps: 0, MaxWraps: 0, Modulus: modulus}
	if width < 64 {
		maxWraps := (maxInc - base) / modulus
		iv.Max = base + maxWraps*modulus
		iv.MaxWraps = maxWraps
	} else {
		// Modulus is 2^64; a second wrap needs an increment >= 2^64,
		// which no uint64 budget can reach. Only the zero-wrap value fits.
	}
	return iv, nil
}
