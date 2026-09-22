package fp

import (
	"errors"
	"math/big"
	"strings"
)

// Kind classifies an exact input value.
type Kind uint8

const (
	KindZero Kind = iota
	KindFinite
	KindInf
	KindNaN
)

// NaNInfo describes a decoded NaN.
type NaNInfo struct {
	Neg     bool
	Payload uint64 // trailing-significand payload excluding the quiet bit
	Quiet   bool
}

// Exact is a host-float-free representation of a conversion input.
//
// Finite nonzero values are the rational Num/Den with Den > 0 reduced to lowest
// terms; Neg records the sign (Num is always positive for finite values).
type Exact struct {
	Kind Kind
	Neg  bool
	Num  *big.Int
	Den  *big.Int
	NaN  NaNInfo
}

func zeroExact(neg bool) Exact {
	return Exact{Kind: KindZero, Neg: neg}
}

// ParseError reports a structurally invalid decimal string.
type ParseError struct{ s string }

func (e *ParseError) Error() string { return "fp: invalid decimal string: " + e.s }

const maxDecimalExp = 100000

// ParseDecimal converts a decimal literal to an exact rational value without
// passing through any host floating-point type, so no double rounding can
// occur.
//
// Accepted grammar (surrounding ASCII whitespace is ignored):
//
//	[+-]? ( digits ['.' digits]? | '.' digits ) [e [+-]? digits]?
//	[+-]? (inf | infinity)
//	[+-]? nan [ '(' digits ')' ]   (always quiet; signaling is never parsed)
func ParseDecimal(s string) (Exact, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return Exact{}, &ParseError{s}
	}
	neg := false
	switch t[0] {
	case '+':
		t = t[1:]
	case '-':
		neg = true
		t = t[1:]
	}
	if t == "" {
		return Exact{}, &ParseError{s}
	}
	lower := strings.ToLower(t)
	switch lower {
	case "inf", "infinity":
		return Exact{Kind: KindInf, Neg: neg}, nil
	case "nan":
		return Exact{Kind: KindNaN, Neg: neg, NaN: NaNInfo{Quiet: true}}, nil
	}
	if strings.HasPrefix(lower, "nan(") && strings.HasSuffix(lower, ")") {
		var p big.Int
		body := t[4 : len(t)-1]
		if _, ok := p.SetString(body, 10); !ok || p.Sign() < 0 {
			return Exact{}, &ParseError{s}
		}
		return Exact{Kind: KindNaN, Neg: neg, NaN: NaNInfo{Quiet: true, Payload: p.Uint64()}}, nil
	}

	mant := new(big.Int)
	fracDigits := 0
	seenDigit := false
	dot := false
	sawE := false
	i := 0
loop:
	for ; i < len(t); i++ {
		c := t[i]
		switch {
		case c == '.':
			if dot {
				return Exact{}, &ParseError{s}
			}
			dot = true
		case c >= '0' && c <= '9':
			mant.Mul(mant, big.NewInt(10))
			mant.Add(mant, big.NewInt(int64(c-'0')))
			seenDigit = true
			if dot {
				fracDigits++
			}
		case c == 'e' || c == 'E':
			sawE = true
			i++
			break loop
		default:
			return Exact{}, &ParseError{s}
		}
	}
	if !seenDigit {
		return Exact{}, &ParseError{s}
	}
	exp := 0
	if !sawE {
		if i != len(t) {
			return Exact{}, &ParseError{s}
		}
	} else {
		if i >= len(t) {
			return Exact{}, &ParseError{s}
		}
		var perr error
		exp, perr = parseSignedInt(t[i:])
		if perr != nil || exp < -maxDecimalExp || exp > maxDecimalExp {
			return Exact{}, &ParseError{s}
		}
	}
	if mant.Sign() == 0 {
		return zeroExact(neg), nil
	}
	scale := fracDigits - exp // mant * 10^-scale
	if scale == 0 {
		return Exact{Kind: KindFinite, Neg: neg, Num: mant, Den: big.NewInt(1)}, nil
	}
	ten := big.NewInt(10)
	if scale > 0 {
		den := new(big.Int).Exp(ten, big.NewInt(int64(scale)), nil)
		g := new(big.Int).GCD(nil, nil, mant, den)
		mant.Quo(mant, g)
		den.Quo(den, g)
		return Exact{Kind: KindFinite, Neg: neg, Num: mant, Den: den}, nil
	}
	mant.Mul(mant, new(big.Int).Exp(ten, big.NewInt(int64(-scale)), nil))
	return Exact{Kind: KindFinite, Neg: neg, Num: mant, Den: big.NewInt(1)}, nil
}

func parseSignedInt(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	neg := false
	i := 0
	switch s[0] {
	case '+':
		i = 1
	case '-':
		neg = true
		i = 1
	}
	if i == len(s) {
		return 0, errors.New("sign only")
	}
	v := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("bad digit")
		}
		v = v*10 + int(c-'0')
		if v > maxDecimalExp {
			v = maxDecimalExp
		}
	}
	if neg {
		v = -v
	}
	return v, nil
}
