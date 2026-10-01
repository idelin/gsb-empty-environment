package tsxform

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Ratio is a positive rational number Num/Den kept in lowest terms.
// A zero ratio is never valid: rate 0 is rejected at construction.
type Ratio struct {
	Num int64
	Den int64
}

// ErrZeroRate is returned when a zero scaling rate is requested.
var ErrZeroRate = errors.New("rate must be positive: zero rate rejected")

// NewRatio builds a reduced positive ratio. Zero and negative rates are
// rejected, as is a zero denominator.
func NewRatio(num, den int64) (Ratio, error) {
	if den == 0 {
		return Ratio{}, fmt.Errorf("invalid ratio %d/%d: zero denominator", num, den)
	}
	if den < 0 {
		num, den = -num, -den
	}
	if num == 0 {
		return Ratio{}, ErrZeroRate
	}
	if num < 0 {
		return Ratio{}, fmt.Errorf("rate must be positive, got %d/%d", num, den)
	}
	g := gcd64(num, den)
	return Ratio{Num: num / g, Den: den / g}, nil
}

// MustRatio is NewRatio that panics on error, for tests and constants.
func MustRatio(num, den int64) Ratio {
	r, err := NewRatio(num, den)
	if err != nil {
		panic(err)
	}
	return r
}

// ParseRatio parses "p/q" or a plain integer "p".
func ParseRatio(s string) (Ratio, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, "/")
	if len(parts) > 2 {
		return Ratio{}, fmt.Errorf("invalid ratio %q", s)
	}
	num, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return Ratio{}, fmt.Errorf("invalid ratio %q: %v", s, err)
	}
	den := int64(1)
	if len(parts) == 2 {
		den, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return Ratio{}, fmt.Errorf("invalid ratio %q: %v", s, err)
		}
	}
	return NewRatio(num, den)
}

func (r Ratio) String() string { return fmt.Sprintf("%d/%d", r.Num, r.Den) }

func gcd64(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
