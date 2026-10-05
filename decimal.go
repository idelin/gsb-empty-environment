// Package decimal provides immutable arbitrary-precision decimal arithmetic.
//
// A Decimal is an exact value mant * 10^exp. All operations copy their
// operands and never mutate a Decimal after construction, so values (and
// expression nodes built from them) can be shared freely without risk of
// one computation polluting another through big.Int reuse.
//
// Exact operations (Add, Sub, Mul, Neg) are methods on Decimal. Rounding
// and budget-aware division live on Context, which returns an Outcome
// carrying both the exact value (when representable) and the value rounded
// to the requested precision.
package decimal

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ErrDivisionByZero is returned by division operations with a zero divisor.
// Division by zero is reported separately and never produces a Decimal.
var ErrDivisionByZero = errors.New("decimal: division by zero")

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
	bigTwo  = big.NewInt(2)
	bigFive = big.NewInt(5)
	bigTen  = big.NewInt(10)
)

// Decimal is an immutable arbitrary-precision decimal: value = mant * 10^exp.
// The zero value is usable and represents 0.
type Decimal struct {
	mant *big.Int
	exp  int64
}

// Zero is the decimal 0.
var Zero = Decimal{mant: new(big.Int), exp: 0}

// makeDecimal normalizes a (mant, exp) pair; zero always has exp 0.
// It takes ownership of m, which must not be shared or mutated afterwards.
func makeDecimal(m *big.Int, e int64) Decimal {
	if m.Sign() == 0 {
		return Zero
	}
	return Decimal{mant: m, exp: e}
}

// mantissa returns the internal mantissa, tolerating the zero value.
func (d Decimal) mantissa() *big.Int {
	if d.mant == nil {
		return bigZero
	}
	return d.mant
}

// New returns mantissa * 10^exp.
func New(mantissa int64, exp int64) Decimal {
	return makeDecimal(big.NewInt(mantissa), exp)
}

// NewFromInt returns m * 10^exp. m is copied.
func NewFromInt(m *big.Int, exp int64) Decimal {
	return makeDecimal(new(big.Int).Set(m), exp)
}

// Mantissa returns a copy of the mantissa m with value = m * 10^Exp().
func (d Decimal) Mantissa() *big.Int {
	return new(big.Int).Set(d.mantissa())
}

// Exp returns the decimal exponent: value = Mantissa() * 10^Exp().
func (d Decimal) Exp() int64 {
	return d.exp
}

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int {
	return d.mantissa().Sign()
}

// IsZero reports whether d == 0.
func (d Decimal) IsZero() bool {
	return d.mantissa().Sign() == 0
}

// Neg returns -d.
func (d Decimal) Neg() Decimal {
	return makeDecimal(new(big.Int).Neg(d.mantissa()), d.exp)
}

// Abs returns |d|.
func (d Decimal) Abs() Decimal {
	return makeDecimal(new(big.Int).Abs(d.mantissa()), d.exp)
}

// Add returns the exact sum d + o, aligning differing scales.
func (d Decimal) Add(o Decimal) Decimal {
	if d.exp == o.exp {
		return makeDecimal(new(big.Int).Add(d.mantissa(), o.mantissa()), d.exp)
	}
	lo, hi := d, o
	if lo.exp > hi.exp {
		lo, hi = hi, lo
	}
	m := new(big.Int).Mul(hi.mantissa(), pow10(hi.exp-lo.exp))
	m.Add(m, lo.mantissa())
	return makeDecimal(m, lo.exp)
}

// Sub returns the exact difference d - o.
func (d Decimal) Sub(o Decimal) Decimal {
	return d.Add(o.Neg())
}

// Mul returns the exact product d * o.
func (d Decimal) Mul(o Decimal) Decimal {
	return makeDecimal(new(big.Int).Mul(d.mantissa(), o.mantissa()), d.exp+o.exp)
}

// Cmp compares d and o, returning -1, 0 or +1.
func (d Decimal) Cmp(o Decimal) int {
	sa, sb := d.mantissa().Sign(), o.mantissa().Sign()
	if sa != sb {
		if sa < sb {
			return -1
		}
		return 1
	}
	if sa == 0 {
		return 0
	}
	switch {
	case d.exp == o.exp:
		return d.mantissa().Cmp(o.mantissa())
	case d.exp > o.exp:
		m := new(big.Int).Mul(d.mantissa(), pow10(d.exp-o.exp))
		return m.Cmp(o.mantissa())
	default:
		m := new(big.Int).Mul(o.mantissa(), pow10(o.exp-d.exp))
		return d.mantissa().Cmp(m)
	}
}

// Rat returns the exact value of d as a *big.Rat.
func (d Decimal) Rat() *big.Rat {
	r := new(big.Rat).SetInt(d.mantissa())
	switch {
	case d.exp > 0:
		r.Mul(r, new(big.Rat).SetInt(pow10(d.exp)))
	case d.exp < 0:
		r.Quo(r, new(big.Rat).SetInt(pow10(-d.exp)))
	}
	return r
}

// pow10 returns 10^n for n >= 0.
func pow10(n int64) *big.Int {
	if n < 0 {
		panic("decimal: pow10 with negative exponent")
	}
	return new(big.Int).Exp(bigTen, big.NewInt(n), nil)
}

// Parse converts a decimal string to a Decimal. Accepted forms include
// "123", "-0.05", "+1.5e3", "2E-7". The result is exact.
func Parse(s string) (Decimal, error) {
	orig := s
	exp := int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		if strings.IndexAny(s[i+1:], "eE") >= 0 {
			return Decimal{}, fmt.Errorf("decimal: invalid number %q", orig)
		}
		e, err := strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil {
			return Decimal{}, fmt.Errorf("decimal: invalid exponent in %q", orig)
		}
		exp = e
		s = s[:i]
	}
	neg := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	var digits strings.Builder
	frac := int64(0)
	seenDot, seenDigit := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '.':
			if seenDot {
				return Decimal{}, fmt.Errorf("decimal: invalid number %q", orig)
			}
			seenDot = true
		case ch >= '0' && ch <= '9':
			digits.WriteByte(ch)
			if seenDot {
				frac++
			}
			seenDigit = true
		default:
			return Decimal{}, fmt.Errorf("decimal: invalid number %q", orig)
		}
	}
	if !seenDigit {
		return Decimal{}, fmt.Errorf("decimal: invalid number %q", orig)
	}
	m, ok := new(big.Int).SetString(digits.String(), 10)
	if !ok {
		return Decimal{}, fmt.Errorf("decimal: invalid number %q", orig)
	}
	if neg {
		m.Neg(m)
	}
	return makeDecimal(m, exp-frac), nil
}

// String returns d in plain decimal notation (no exponent). Zero is "0".
func (d Decimal) String() string {
	m := d.mantissa()
	if m.Sign() == 0 {
		return "0"
	}
	neg := m.Sign() < 0
	digits := new(big.Int).Abs(m).String()
	var sb strings.Builder
	if neg {
		sb.WriteByte('-')
	}
	if d.exp >= 0 {
		sb.WriteString(digits)
		for i := int64(0); i < d.exp; i++ {
			sb.WriteByte('0')
		}
		return sb.String()
	}
	point := int64(len(digits)) + d.exp
	if point > 0 {
		sb.WriteString(digits[:point])
		sb.WriteByte('.')
		sb.WriteString(digits[point:])
		return sb.String()
	}
	sb.WriteString("0.")
	for i := int64(0); i < -point; i++ {
		sb.WriteByte('0')
	}
	sb.WriteString(digits)
	return sb.String()
}
