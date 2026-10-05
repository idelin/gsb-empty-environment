// Package numlit parses numeric literals (decimal and hexadecimal integers,
// decimals with optional exponents) into exact arbitrary-precision values
// without going through floating point.
//
// Results are represented either as a *big.Int (integer literals) or as a
// reduced *big.Rat (literals with a fraction and/or exponent). The sign is
// preserved even for zero values (negative zero), and the byte span of the
// literal in the input is recorded.
//
// A Budget bounds the number of significant digits and the magnitude of the
// decimal exponent expansion so that hostile inputs cannot trigger huge
// memory allocations.
package numlit

import (
	"fmt"
	"math/big"
	"strings"
)

// Budget limits the resources a single parse may consume.
type Budget struct {
	// MaxDigits bounds the number of significant mantissa digits
	// (separators and leading zeros excluded).
	MaxDigits int
	// MaxExponent bounds the absolute value of the effective decimal
	// exponent (explicit exponent minus fraction digits), i.e. the
	// largest power of ten that will be materialized.
	MaxExponent int
}

// DefaultBudget is used when a zero Budget is passed to Parse.
var DefaultBudget = Budget{MaxDigits: 4096, MaxExponent: 4096}

// Kind distinguishes integer results from rational results.
type Kind int

const (
	// Int literals have no fraction and no exponent: 42, 0x1F, -7.
	Int Kind = iota
	// Rat literals have a fraction and/or exponent: 1.5, .5, 1e3, 2.
	Rat
)

// Span is the byte range [Start, End) of the literal in the input.
type Span struct {
	Start, End int
}

// Value is the parsed result.
type Value struct {
	Kind Kind
	// Int holds the value when Kind == Int.
	Int *big.Int
	// Rat holds the value (already reduced) when Kind == Rat.
	Rat *big.Rat
	// Neg preserves the written sign, including for zero values.
	Neg bool
	// Span is the byte range of the literal in the parsed input.
	Span Span
}

// Rational returns the exact numeric value as a rational number.
func (v *Value) Rational() *big.Rat {
	if v.Kind == Rat {
		return new(big.Rat).Set(v.Rat)
	}
	return new(big.Rat).SetInt(v.Int)
}

// Equal reports whether v and w denote the same number, regardless of how
// they were written (e.g. 0x10, 16, 1.6e1 and 16.00 are all equal).
func (v *Value) Equal(w *Value) bool {
	return v.Rational().Cmp(w.Rational()) == 0
}

// IsZero reports whether the numeric value is zero. Use Neg to distinguish
// negative zero from positive zero.
func (v *Value) IsZero() bool {
	return v.Rational().Sign() == 0
}

// Error describes a parse or budget failure.
type Error struct {
	Pos    int    // byte offset in the input
	Msg    string // human-readable description
	Budget bool   // true when a resource budget was exceeded
}

func (e *Error) Error() string {
	return fmt.Sprintf("numlit: %s (offset %d)", e.Msg, e.Pos)
}

// Parse parses s, which must consist of exactly one numeric literal, using
// the given budget. A zero Budget selects DefaultBudget.
func Parse(s string, b Budget) (*Value, error) {
	if b == (Budget{}) {
		b = DefaultBudget
	}
	p := &parser{s: s, b: b}
	v, err := p.parse()
	if err != nil {
		return nil, err
	}
	v.Span = Span{Start: 0, End: len(s)}
	return v, nil
}

type parser struct {
	s string
	i int
	b Budget
}

func (p *parser) errf(pos int, format string, args ...interface{}) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) errBudget(pos int, format string, args ...interface{}) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...), Budget: true}
}

func (p *parser) parse() (*Value, error) {
	v := &Value{}
	if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
		v.Neg = p.s[p.i] == '-'
		p.i++
	}
	if p.i >= len(p.s) {
		return nil, p.errf(p.i, "expected digits")
	}
	if p.s[p.i] == '0' && p.i+1 < len(p.s) && (p.s[p.i+1] == 'x' || p.s[p.i+1] == 'X') {
		return p.parseHex(v)
	}
	return p.parseDecimal(v)
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func isDecDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

// scanDigits consumes a run of digits in which '_' separators are legal only
// between two digits of the run. It returns the digits with separators
// removed.
func (p *parser) scanDigits(isDigit func(byte) bool) (string, error) {
	var sb strings.Builder
	prevDigit := false
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case isDigit(c):
			sb.WriteByte(c)
			prevDigit = true
			p.i++
		case c == '_':
			if !prevDigit || p.i+1 >= len(p.s) || !isDigit(p.s[p.i+1]) {
				return "", p.errf(p.i, "separator '_' must appear between two digits")
			}
			prevDigit = false
			p.i++
		default:
			return sb.String(), nil
		}
	}
	return sb.String(), nil
}

// checkDigits enforces the significant-digit budget.
func (p *parser) checkDigits(pos int, digits string) error {
	sig := len(digits)
	for _, c := range []byte(digits) {
		if c != '0' {
			break
		}
		sig--
	}
	if sig > p.b.MaxDigits {
		return p.errBudget(pos, "too many significant digits: %d exceeds budget %d", sig, p.b.MaxDigits)
	}
	return nil
}

func (p *parser) parseHex(v *Value) (*Value, error) {
	p.i += 2 // consume 0x
	pos := p.i
	digits, err := p.scanDigits(isHexDigit)
	if err != nil {
		return nil, err
	}
	if len(digits) == 0 {
		return nil, p.errf(pos, "expected hexadecimal digits after 0x")
	}
	if p.i < len(p.s) {
		return nil, p.errf(p.i, "unexpected character %q after hexadecimal integer", p.s[p.i])
	}
	if err := p.checkDigits(pos, digits); err != nil {
		return nil, err
	}
	n, ok := new(big.Int).SetString(digits, 16)
	if !ok {
		return nil, p.errf(pos, "invalid hexadecimal digits")
	}
	if v.Neg {
		n.Neg(n)
	}
	v.Kind = Int
	v.Int = n
	return v, nil
}

func (p *parser) parseDecimal(v *Value) (*Value, error) {
	pos := p.i
	intDigits, err := p.scanDigits(isDecDigit)
	if err != nil {
		return nil, err
	}

	fracDigits := ""
	hasFrac := false
	if p.i < len(p.s) && p.s[p.i] == '.' {
		hasFrac = true
		p.i++
		fracDigits, err = p.scanDigits(isDecDigit)
		if err != nil {
			return nil, err
		}
	}
	if len(intDigits)+len(fracDigits) == 0 {
		return nil, p.errf(pos, "expected digits")
	}

	hasExp := false
	expNeg := false
	expDigits := ""
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		hasExp = true
		expPos := p.i
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			expNeg = p.s[p.i] == '-'
			p.i++
		}
		expDigits, err = p.scanDigits(isDecDigit)
		if err != nil {
			return nil, err
		}
		if len(expDigits) == 0 {
			return nil, p.errf(expPos, "expected exponent digits")
		}
	}
	if p.i < len(p.s) {
		return nil, p.errf(p.i, "unexpected character %q", p.s[p.i])
	}

	mantissa := intDigits + fracDigits
	if err := p.checkDigits(pos, mantissa); err != nil {
		return nil, err
	}

	// Effective decimal exponent: explicit exponent minus fraction digits.
	// Checked against the budget before any power of ten is materialized.
	expCap := int64(p.b.MaxExponent) + int64(len(fracDigits)) + 1
	if expCap < 0 || expCap > 1<<62 {
		expCap = 1 << 62
	}
	expVal := int64(0)
	for _, c := range []byte(expDigits) {
		expVal = expVal*10 + int64(c-'0')
		if expVal > expCap {
			return nil, p.errBudget(pos, "exponent exceeds budget %d", p.b.MaxExponent)
		}
	}
	if expNeg {
		expVal = -expVal
	}
	effExp := expVal - int64(len(fracDigits))
	if effExp > int64(p.b.MaxExponent) || effExp < -int64(p.b.MaxExponent) {
		return nil, p.errBudget(pos, "effective exponent %d exceeds budget %d", effExp, p.b.MaxExponent)
	}

	num, _ := new(big.Int).SetString(mantissa, 10)
	if num == nil {
		num = new(big.Int)
	}

	if !hasFrac && !hasExp {
		if v.Neg {
			num.Neg(num)
		}
		v.Kind = Int
		v.Int = num
		return v, nil
	}

	r := new(big.Rat)
	if effExp >= 0 {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(effExp), nil)
		r.SetInt(num.Mul(num, pow))
	} else {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(-effExp), nil)
		r.SetFrac(num, pow) // SetFrac reduces to lowest terms
	}
	if v.Neg {
		r.Neg(r)
	}
	v.Kind = Rat
	v.Rat = r
	return v, nil
}
