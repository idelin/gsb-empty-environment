// Package numlit parses numeric literals into exact arbitrary-precision
// values, without ever going through floating point.
//
// Grammar:
//
//	literal := sign? (hexInt | dec)
//	sign    := '+' | '-'
//	hexInt  := '0' ('x' | 'X') hexDigits
//	dec     := decDigits ('.' decDigits?)? exp?
//	         | '.' decDigits exp?
//	exp     := ('e' | 'E') sign? decDigits
//
// decDigits and hexDigits are sequences of one or more digits in which a
// single '_' separator may appear strictly between two digits. A
// separator at any other position (leading, trailing, doubled, next to
// '.', 'e', a sign, or the "0x" prefix) is a syntax error.
//
// The result is either an arbitrary-precision integer or a reduced
// rational num/den with den > 1. The sign is preserved exactly as
// written, so "-0" and "0" compare equal but have different signs.
// Each Value also records the half-open byte interval [Start, End) of
// the literal in the source string.
//
// Resource usage is bounded by a Budget: the number of significant
// mantissa digits and the magnitude of the effective decimal exponent
// are both limited, and the limits are enforced before any large
// allocation happens, so inputs like "1e999999999999" fail cheaply.
package numlit

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrSyntax is returned (wrapped) when the input is not a well-formed
// numeric literal.
var ErrSyntax = errors.New("numlit: invalid numeric literal")

// ErrBudget is returned (wrapped) when the literal exceeds the resource
// budget. No large allocation is performed in this case.
var ErrBudget = errors.New("numlit: resource budget exceeded")

func syntaxErr(pos int, msg string) error {
	return fmt.Errorf("%w at offset %d: %s", ErrSyntax, pos, msg)
}

func budgetErr(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrBudget, fmt.Sprintf(format, args...))
}

// Budget bounds the resources a single parse may consume.
//
// MaxSignificantDigits limits the number of mantissa digits after
// leading zeros are stripped (hex digits for hexadecimal literals).
//
// MaxExponent limits the magnitude of the effective decimal exponent,
// i.e. the written exponent combined with the fractional shift. It
// bounds both the expansion of "1eN" into an integer and the size of
// the denominator of "1e-N".
//
// A zero or negative field falls back to the corresponding
// DefaultBudget value.
type Budget struct {
	MaxSignificantDigits int
	MaxExponent          int
}

// DefaultBudget is used by Parse.
var DefaultBudget = Budget{MaxSignificantDigits: 10_000, MaxExponent: 100_000}

func (b Budget) withDefaults() Budget {
	if b.MaxSignificantDigits <= 0 {
		b.MaxSignificantDigits = DefaultBudget.MaxSignificantDigits
	}
	if b.MaxExponent <= 0 {
		b.MaxExponent = DefaultBudget.MaxExponent
	}
	return b
}

// Kind reports whether a Value is an integer or a reduced fraction.
type Kind int

const (
	// Int indicates an arbitrary-precision integer (denominator 1).
	Int Kind = iota
	// Rat indicates a reduced rational with denominator greater than 1.
	Rat
)

// Value is the exact result of parsing a numeric literal. The numerator
// and denominator are kept in lowest terms; the denominator is 1 for
// integers. Start and End form the half-open byte interval of the
// literal in the parsed source string.
type Value struct {
	neg  bool
	kind Kind
	num  *big.Int // numerator magnitude
	den  *big.Int // denominator, nil when kind == Int

	Start int
	End   int
}

// Kind reports whether the value is an integer or a fraction.
func (v *Value) Kind() Kind { return v.kind }

// Sign returns the sign exactly as written in the literal: -1 if a '-'
// was present, +1 otherwise. In particular Sign is -1 for "-0".
func (v *Value) Sign() int {
	if v.neg {
		return -1
	}
	return 1
}

// IsZero reports whether the value is numerically zero.
func (v *Value) IsZero() bool { return v.num.Sign() == 0 }

// Num returns a signed copy of the reduced numerator.
func (v *Value) Num() *big.Int {
	n := new(big.Int).Set(v.num)
	if v.neg {
		n.Neg(n)
	}
	return n
}

// Den returns a copy of the reduced denominator (1 for integers).
func (v *Value) Den() *big.Int {
	if v.den == nil {
		return big.NewInt(1)
	}
	return new(big.Int).Set(v.den)
}

// Int returns the exact integer value and true if the value is an
// integer, or nil and false otherwise.
func (v *Value) Int() (*big.Int, bool) {
	if v.kind != Int {
		return nil, false
	}
	return v.Num(), true
}

// Rat returns the value as a reduced *big.Rat.
func (v *Value) Rat() *big.Rat {
	return new(big.Rat).SetFrac(v.Num(), v.Den())
}

// Cmp compares the numeric values of v and o, returning -1, 0 or +1.
// Negative zero compares equal to zero.
func (v *Value) Cmp(o *Value) int { return v.Rat().Cmp(o.Rat()) }

// Equal reports whether v and o are numerically equal, regardless of
// how the literals were written. "-0" equals "0".
func (v *Value) Equal(o *Value) bool { return v.Cmp(o) == 0 }

func (v *Value) String() string {
	if v.kind == Int {
		return v.Num().String()
	}
	return v.Num().String() + "/" + v.Den().String()
}

// Parse parses s, which must consist of exactly one numeric literal,
// using DefaultBudget.
func Parse(s string) (*Value, error) { return ParseWith(s, DefaultBudget) }

// ParseWith is Parse with an explicit resource budget.
func ParseWith(s string, b Budget) (*Value, error) {
	v, err := ParsePrefix(s, 0, b)
	if err != nil {
		return nil, err
	}
	if v.End != len(s) {
		return nil, syntaxErr(v.End, "unexpected characters after numeric literal")
	}
	return v, nil
}

// ParsePrefix parses the numeric literal starting at byte offset start
// and returns its value. The literal ends at the first character that
// cannot be part of it; v.End is the offset of that character.
func ParsePrefix(s string, start int, b Budget) (*Value, error) {
	if start < 0 || start > len(s) {
		return nil, fmt.Errorf("numlit: start offset %d out of range", start)
	}
	b = b.withDefaults()
	i := start
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
		return parseHex(s, start, i+2, neg, b)
	}
	return parseDec(s, start, i, neg, b)
}

func isDecDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDecDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// scanDigits scans one or more digits starting at i. A '_' is consumed
// only when it sits strictly between two digits; any other '_' after a
// digit is a syntax error.
func scanDigits(s string, i int, hex bool) ([]byte, int, error) {
	valid := func(c byte) bool { return isDecDigit(c) || (hex && isHexDigit(c)) }
	var digits []byte
	for i < len(s) && valid(s[i]) {
		digits = append(digits, s[i])
		i++
		if i < len(s) && s[i] == '_' {
			if i+1 >= len(s) || !valid(s[i+1]) {
				return nil, i, syntaxErr(i, "digit separator '_' must appear between two digits")
			}
			i++
		}
	}
	return digits, i, nil
}

func parseHex(s string, start, i int, neg bool, b Budget) (*Value, error) {
	digits, next, err := scanDigits(s, i, true)
	if err != nil {
		return nil, err
	}
	if len(digits) == 0 {
		return nil, syntaxErr(i, "expected hexadecimal digit after 0x prefix")
	}
	sig := stripLeadingZeros(digits)
	if len(sig) > b.MaxSignificantDigits {
		return nil, budgetErr("hexadecimal literal has %d significant digits, limit is %d",
			len(sig), b.MaxSignificantDigits)
	}
	num := new(big.Int)
	if len(sig) > 0 {
		num.SetString(string(sig), 16)
	}
	return &Value{neg: neg, kind: Int, num: num, Start: start, End: next}, nil
}

// expSaturation caps the parsed exponent; any exponent this large is
// far beyond any sane budget, and capping avoids int64 overflow.
const expSaturation = int64(1) << 62

func parseExpCapped(digits []byte) int64 {
	var v int64
	for _, d := range digits {
		v = v*10 + int64(d-'0')
		if v >= expSaturation {
			return expSaturation
		}
	}
	return v
}

func stripLeadingZeros(digits []byte) []byte {
	for len(digits) > 0 && digits[0] == '0' {
		digits = digits[1:]
	}
	return digits
}

func parseDec(s string, start, i int, neg bool, b Budget) (*Value, error) {
	intDigits, i, err := scanDigits(s, i, false)
	if err != nil {
		return nil, err
	}
	var fracDigits []byte
	if i < len(s) && s[i] == '.' {
		i++
		fracDigits, i, err = scanDigits(s, i, false)
		if err != nil {
			return nil, err
		}
	}
	if len(intDigits) == 0 && len(fracDigits) == 0 {
		return nil, syntaxErr(start, "expected a digit")
	}

	expNeg := false
	var expVal int64
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		en := false
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			en = s[j] == '-'
			j++
		}
		ed, next, err := scanDigits(s, j, false)
		if err != nil {
			return nil, err
		}
		if len(ed) > 0 {
			expNeg = en
			expVal = parseExpCapped(ed)
			i = next
		}
	}

	mant := make([]byte, 0, len(intDigits)+len(fracDigits))
	mant = append(mant, intDigits...)
	mant = append(mant, fracDigits...)
	mant = stripLeadingZeros(mant)

	// A zero mantissa never triggers expansion, whatever the exponent.
	if len(mant) == 0 {
		return &Value{neg: neg, kind: Int, num: new(big.Int), Start: start, End: i}, nil
	}

	exp10 := expVal
	if expNeg {
		exp10 = -exp10
	}
	exp10 -= int64(len(fracDigits))
	if exp10 > int64(b.MaxExponent) || exp10 < -int64(b.MaxExponent) {
		return nil, budgetErr("effective decimal exponent %d exceeds limit %d",
			exp10, b.MaxExponent)
	}
	if len(mant) > b.MaxSignificantDigits {
		return nil, budgetErr("mantissa has %d significant digits, limit is %d",
			len(mant), b.MaxSignificantDigits)
	}

	num := new(big.Int)
	num.SetString(string(mant), 10)

	if exp10 >= 0 {
		if exp10 > 0 {
			pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(exp10), nil)
			num.Mul(num, pow)
		}
		return &Value{neg: neg, kind: Int, num: num, Start: start, End: i}, nil
	}

	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(-exp10), nil)
	g := new(big.Int).GCD(nil, nil, num, den)
	num.Quo(num, g)
	den.Quo(den, g)
	if den.Cmp(big.NewInt(1)) == 0 {
		return &Value{neg: neg, kind: Int, num: num, Start: start, End: i}, nil
	}
	return &Value{neg: neg, kind: Rat, num: num, den: den, Start: start, End: i}, nil
}
