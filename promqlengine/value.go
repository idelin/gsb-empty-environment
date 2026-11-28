package promqlengine

import (
	"math"
	"sort"
)

// Prometheus encodes explicit stale markers with a specific quiet NaN payload
// so that they can be told apart from NaNs produced by ordinary arithmetic.
//
// The bit pattern is defined by model.StaleNaN in the Prometheus codebase:
// exponent 0x7ff, quiet bit set, lowest payload bit set, everything else zero.
// A "normal" NaN (for example math.NaN()) carries payload bit 1 set instead
// and must NOT be treated as a stale marker.
const staleNaNBits uint64 = 0x7ff8000000000002

// StaleNaN returns the canonical Prometheus stale-marker value.
func StaleNaN() float64 {
	return math.Float64frombits(staleNaNBits)
}

// IsStaleNaN reports whether v carries the exact stale-marker payload.
//
// The check is bitwise on purpose: math.IsNaN alone would conflate stale
// markers with regular NaNs, while the engine must only truncate on the
// specific payload.
func IsStaleNaN(v float64) bool {
	return math.Float64bits(v) == staleNaNBits
}

// Point is a single timestamped sample. Timestamps are Unix milliseconds.
type Point struct {
	T int64
	V float64
}

// Sample is one element of an instant vector: a label set plus a single
// value stamped at the evaluation time.
type Sample struct {
	Metric Labels
	Point
}

// Series is one element of a range vector / matrix: a label set plus the
// ordered sample points produced by a range selector or subquery.
type Series struct {
	Metric Labels
	Points []Point
}

// Value is the result of evaluating any expression at a single timestamp.
// The concrete type is one of:
//
//   - float64                  (scalar, reserved for literals)
//   - []Sample                 (instant vector)
//   - []Series                 (range vector / matrix)
type Value any

// sortVector orders instant-vector output by label fingerprint, matching the
// deterministic ordering expected from a Prometheus engine.
func sortVector(v []Sample) {
	sort.Slice(v, func(i, j int) bool {
		return v[i].Metric.Fingerprint() < v[j].Metric.Fingerprint()
	})
}

// sortMatrix orders matrix series the same way.
func sortMatrix(m []Series) {
	sort.Slice(m, func(i, j int) bool {
		return m[i].Metric.Fingerprint() < m[j].Metric.Fingerprint()
	})
}

// asVector / asSeries are small type-assertion helpers used by the evaluator.
func asVector(v Value) ([]Sample, bool) {
	vec, ok := v.([]Sample)
	return vec, ok
}

func asMatrix(v Value) ([]Series, bool) {
	mat, ok := v.([]Series)
	return mat, ok
}
