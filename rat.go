package timemap

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Rat is an exact rational number. All timeline arithmetic uses Rat so that
// offsets, sampling phases and compression ratios never accumulate float error.
type Rat struct {
	v *big.Rat
}

// NewRat builds a Rat from numerator and denominator.
func NewRat(num, den int64) Rat {
	return Rat{v: big.NewRat(num, den)}
}

// RatFromInt builds a Rat from an integer.
func RatFromInt(n int64) Rat {
	return Rat{v: big.NewRat(n, 1)}
}

// RatFromBig wraps a big.Rat, copying it.
func RatFromBig(r *big.Rat) Rat {
	return Rat{v: new(big.Rat).Set(r)}
}

// ParseRat accepts "3/2", "1.25", "7" or "-0.5".
func ParseRat(s string) (Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rat{}, fmt.Errorf("empty rational")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return Rat{}, fmt.Errorf("invalid rational %q", s)
	}
	return Rat{v: r}, nil
}

func (r Rat) valid() bool { return r.v != nil }

// Add returns r+o.
func (r Rat) Add(o Rat) Rat { return Rat{v: new(big.Rat).Add(r.v, o.v)} }

// Sub returns r-o.
func (r Rat) Sub(o Rat) Rat { return Rat{v: new(big.Rat).Sub(r.v, o.v)} }

// Mul returns r*o.
func (r Rat) Mul(o Rat) Rat { return Rat{v: new(big.Rat).Mul(r.v, o.v)} }

// Div returns r/o.
func (r Rat) Div(o Rat) Rat { return Rat{v: new(big.Rat).Quo(r.v, o.v)} }

// Neg returns -r.
func (r Rat) Neg() Rat { return Rat{v: new(big.Rat).Neg(r.v)} }

// Cmp returns -1, 0 or +1.
func (r Rat) Cmp(o Rat) int { return r.v.Cmp(o.v) }

// Sign returns -1, 0 or +1.
func (r Rat) Sign() int { return r.v.Sign() }

// IsZero reports whether r == 0.
func (r Rat) IsZero() bool { return r.v.Sign() == 0 }

// Equal reports exact equality.
func (r Rat) Equal(o Rat) bool { return r.v.Cmp(o.v) == 0 }

// Big returns a copy of the underlying big.Rat.
func (r Rat) Big() *big.Rat { return new(big.Rat).Set(r.v) }

// Num returns a copy of the numerator.
func (r Rat) Num() *big.Int { return new(big.Int).Set(r.v.Num()) }

// Den returns a copy of the (positive) denominator.
func (r Rat) Den() *big.Int { return new(big.Int).Set(r.v.Denom()) }

func (r Rat) String() string {
	if !r.valid() {
		return "<nil>"
	}
	return r.v.RatString()
}

// FloatString renders r with n decimal places for human-readable reports.
func (r Rat) FloatString(n int) string {
	if !r.valid() {
		return "<nil>"
	}
	return r.v.FloatString(n)
}

// UnmarshalJSON accepts a JSON string ("3/2", "1.25") or a JSON number.
func (r *Rat) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		parsed, perr := ParseRat(s)
		if perr != nil {
			return perr
		}
		*r = parsed
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return fmt.Errorf("rational must be string or number: %s", string(data))
	}
	parsed, perr := ParseRat(num.String())
	if perr != nil {
		return perr
	}
	*r = parsed
	return nil
}

// MarshalJSON emits the exact rational as a string.
func (r Rat) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

// RoundHalfEven rounds r to the nearest integer, ties going to the even
// neighbour (banker's rounding). All tracks share this single rule so frame
// sampling is reproducible across tracks.
func RoundHalfEven(r Rat) *big.Int {
	floor := new(big.Int)
	rem := new(big.Int)
	// big.Int.DivMod is Euclidean: for positive denominator floor is the
	// mathematical floor and 0 <= rem < den.
	floor.DivMod(r.v.Num(), r.v.Denom(), rem)
	twiceRem := new(big.Int).Lsh(rem, 1)
	cmp := twiceRem.Cmp(r.v.Denom())
	switch {
	case cmp < 0:
		return floor
	case cmp > 0:
		return floor.Add(floor, big.NewInt(1))
	default:
		if floor.Bit(0) == 0 {
			return floor
		}
		return floor.Add(floor, big.NewInt(1))
	}
}
