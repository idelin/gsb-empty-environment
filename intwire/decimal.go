package intwire

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// MaxDecimalDigits is the quota for a bridged decimal integer: the exact
// value may not exceed this many decimal digits. It is enforced BEFORE any
// big arithmetic so hostile exponents (e.g. 1e999999999) cannot trigger
// huge allocations.
const MaxDecimalDigits = 200_000

var (
	ErrSyntax     = errors.New("intwire: malformed numeric lexeme")
	ErrNotInteger = errors.New("intwire: lexeme does not denote an integer")
	ErrQuota      = errors.New("intwire: integer exceeds decimal digit quota")
)

// ParseIntegerLexeme converts a JSON numeric (or plain decimal) lexeme such
// as "123", "-4.5e1" or "1e400" into an exact integer Value.
//
// It never converts through a host float. Integer-ness and the digit quota
// are decided by scanning the lexeme first; only then is exact big
// arithmetic used to materialize the value. The original lexeme is kept in
// Value.Raw.
func ParseIntegerLexeme(s string) (Value, error) {
	neg, mant, effExp, err := scanLexeme(s)
	if err != nil {
		return Value{}, err
	}
	// mant holds the significant mantissa digits (leading zeros stripped).
	if len(mant) == 0 { // mantissa is all zeros: value is exactly 0
		return Value{Kind: KindBig, B: big.NewInt(0), Raw: s}, nil
	}
	if effExp >= 0 {
		// Integer by construction; enforce quota before materializing.
		if int64(len(mant))+effExp > MaxDecimalDigits {
			return Value{}, fmt.Errorf("%w: ~%d digits", ErrQuota, int64(len(mant))+effExp)
		}
	} else {
		// Integer only if the mantissa has enough trailing zeros.
		need := -effExp
		if need > int64(trailingZeros(mant)) {
			return Value{}, fmt.Errorf("%w: %q", ErrNotInteger, s)
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return Value{}, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	if !r.IsInt() {
		return Value{}, fmt.Errorf("%w: %q", ErrNotInteger, s)
	}
	n := r.Num()
	if len(n.Text(10)) > MaxDecimalDigits {
		return Value{}, ErrQuota
	}
	_ = neg // sign is carried by the lexeme handed to big.Rat
	return Value{Kind: KindBig, B: n, Raw: s}, nil
}

// scanLexeme validates the lexeme syntax and returns the sign, the
// significant mantissa digits (leading zeros removed) and the effective
// decimal exponent (explicit exponent minus fractional digit count).
// No floating point is involved.
func scanLexeme(s string) (neg bool, mant string, effExp int64, err error) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	intStart := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	intDigits := s[intStart:i]
	fracDigits := ""
	if i < len(s) && s[i] == '.' {
		i++
		fs := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		fracDigits = s[fs:i]
	}
	if len(intDigits) == 0 && len(fracDigits) == 0 {
		return false, "", 0, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	var exp int64
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		eneg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			eneg = s[i] == '-'
			i++
		}
		es := i
		for i < len(s) && isDigit(s[i]) {
			// Cap accumulation: anything past the quota is a rejection,
			// never a big allocation.
			if exp < MaxDecimalDigits*4 {
				exp = exp*10 + int64(s[i]-'0')
			}
			i++
		}
		if i == es {
			return false, "", 0, fmt.Errorf("%w: %q", ErrSyntax, s)
		}
		if eneg {
			exp = -exp
		}
	}
	if i != len(s) {
		return false, "", 0, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	mant = strings.TrimLeft(intDigits+fracDigits, "0")
	return neg, mant, exp - int64(len(fracDigits)), nil
}

func trailingZeros(d string) int {
	n := 0
	for i := len(d) - 1; i >= 0 && d[i] == '0'; i-- {
		n++
	}
	return n
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
