package chronos

import (
	"errors"
	"fmt"
	"math"
)

// MaxDurationNanos bounds the supported serialization range for durations
// (about +/-146 years). Values outside it are rejected before publication.
const MaxDurationNanos = int64(1) << 62

// ErrOverflow reports a checked integer overflow.
var ErrOverflow = errors.New("chronos: integer overflow")

// Duration is a signed count of nanoseconds aligned to a precision.
// It is a distinct type: it never converts implicitly to or from Instant.
type Duration struct {
	nanos int64
	prec  Precision
}

func NewDuration(nanos int64, p Precision) (Duration, error) {
	if !p.Valid() {
		return Duration{}, fmt.Errorf("chronos: illegal precision label %d", uint8(p))
	}
	if nanos > MaxDurationNanos || nanos < -MaxDurationNanos {
		return Duration{}, fmt.Errorf("chronos: duration %d ns outside supported range +/-%d ns", nanos, MaxDurationNanos)
	}
	if nanos%p.NanosPerUnit() != 0 {
		return Duration{}, fmt.Errorf("chronos: duration %d ns not aligned to precision %s", nanos, p)
	}
	return Duration{nanos: nanos, prec: p}, nil
}

func (d Duration) Nanos() int64         { return d.nanos }
func (d Duration) Precision() Precision { return d.prec }

func addChecked(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

func mulChecked(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	r := a * b
	if r/b != a {
		return 0, ErrOverflow
	}
	return r, nil
}

// Add sums two durations; the result keeps the finer of the two precisions.
func (d Duration) Add(o Duration) (Duration, error) {
	n, err := addChecked(d.nanos, o.nanos)
	if err != nil {
		return Duration{}, err
	}
	p := d.prec
	if o.prec > p {
		p = o.prec
	}
	return NewDuration(n, p)
}

// Mul scales a duration by k with overflow checking.
func (d Duration) Mul(k int64) (Duration, error) {
	n, err := mulChecked(d.nanos, k)
	if err != nil {
		return Duration{}, err
	}
	return NewDuration(n, d.prec)
}
