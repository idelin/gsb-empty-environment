package wireint

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
)

// JSON bridge: integers cross JSON as tagged strings "iw:<kind>:<decimal>"
// so no value ever passes through a 53-bit float. Raw JSON number lexemes
// (including exponent forms like 1e100) are also accepted on input; they are
// parsed from the lexeme text and are NEVER converted to a host float first.

var (
	ErrLexeme  = errors.New("wireint: malformed integer lexeme")
	ErrQuota   = errors.New("wireint: integer exceeds bit-width quota")
	ErrNotInt  = errors.New("wireint: lexeme is not an integer")
	ErrTag     = errors.New("wireint: malformed tagged integer string")
	ErrKindTag = errors.New("wireint: tag kind does not match field")
)

// maxLexemeLen caps the raw text of any JSON number/tagged decimal. The
// length is validated before any allocation or conversion.
const maxLexemeLen = 8192

// EncodeJSONStream writes values as a JSON array of tagged strings,
// streaming element by element.
func EncodeJSONStream(w io.Writer, vals []Value) error {
	bw := bufio.NewWriter(w)
	if err := bw.WriteByte('['); err != nil {
		return err
	}
	for i, v := range vals {
		if i > 0 {
			if err := bw.WriteByte(','); err != nil {
				return err
			}
		}
		b, err := json.Marshal(v.Tag()) // tagged string: no float involved
		if err != nil {
			return err
		}
		if _, err := bw.Write(b); err != nil {
			return err
		}
	}
	if err := bw.WriteByte(']'); err != nil {
		return err
	}
	return bw.Flush()
}

// DecodeJSONStream streams a JSON array and decodes exactly len(fields)
// elements, each against its Field constraint. Elements may be tagged
// strings ("iw:u64:...") or raw JSON numbers; numbers arrive as json.Number
// lexemes (encoding/json never builds a float here). Decoding only interprets
// bytes as numbers; it executes no host code from the input.
func DecodeJSONStream(r io.Reader, fields []Field) ([]Value, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber() // numbers arrive as raw lexemes, never float64
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("%w: expected '['", ErrTag)
	}
	out := make([]Value, 0, len(fields))
	for _, f := range fields {
		if !dec.More() {
			return nil, io.ErrUnexpectedEOF
		}
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v Value
		switch t := tok.(type) {
		case string:
			v, err = ParseTagged(t, f)
		case json.Number:
			v, err = parseNumberLexeme(t.String(), f)
		default:
			return nil, fmt.Errorf("%w: unexpected JSON token %v", ErrTag, tok)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	tok, err = dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != ']' {
		return nil, fmt.Errorf("%w: expected ']'", ErrTag)
	}
	return out, nil
}

// ParseTagged decodes a canonical tagged string "iw:<kind>:<decimal>"
// against field f. The tag kind must match the field kind, the decimal must
// be canonical (no "+", no leading zeros, no "-0"), and the value must
// satisfy the field's bit-width constraint.
func ParseTagged(s string, f Field) (Value, error) {
	if len(s) > maxLexemeLen+16 {
		return Value{}, fmt.Errorf("%w: tag too long", ErrLexeme)
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 || parts[0] != "iw" {
		return Value{}, fmt.Errorf("%w: %q", ErrTag, s)
	}
	kind, ok := ParseKind(parts[1])
	if !ok {
		return Value{}, fmt.Errorf("%w: unknown kind %q", ErrTag, parts[1])
	}
	if kind != f.Kind {
		return Value{}, fmt.Errorf("%w: tag %s, field %s", ErrKindTag, kind, fieldName(f))
	}
	dec := parts[2]
	if len(dec) == 0 || len(dec) > maxLexemeLen {
		return Value{}, fmt.Errorf("%w: decimal length %d", ErrLexeme, len(dec))
	}
	v, ok := new(big.Int).SetString(dec, 10)
	if !ok {
		return Value{}, fmt.Errorf("%w: %q", ErrLexeme, dec)
	}
	if v.String() != dec {
		return Value{}, fmt.Errorf("%w: non-canonical decimal %q", ErrNonCanonical, dec)
	}
	if !f.Contains(v) {
		return Value{}, fmt.Errorf("%w: %s not in %s", ErrRange, v, fieldName(f))
	}
	return Value{Raw: dec, Int: v, Field: f}, nil
}

// parseNumberLexeme parses a raw JSON number lexeme against field f.
func parseNumberLexeme(lex string, f Field) (Value, error) {
	v, err := ParseIntegerLexeme(lex, f.maxBits())
	if err != nil {
		return Value{}, err
	}
	if !f.Contains(v) {
		return Value{}, fmt.Errorf("%w: %s not in %s", ErrRange, lex, fieldName(f))
	}
	return Value{Raw: lex, Int: v, Field: f}, nil
}

// ParseIntegerLexeme converts a JSON number lexeme to an exact integer
// without ever building a host float. It first decides whether the lexeme
// denotes an integer at all (exponent vs. fraction), then checks the
// significant-digit count against the bit quota maxBits BEFORE constructing
// any big.Int, so lexemes like 1e999999999 are rejected by quota instead of
// materializing an astronomical number.
func ParseIntegerLexeme(s string, maxBits int) (*big.Int, error) {
	if len(s) == 0 || len(s) > maxLexemeLen {
		return nil, fmt.Errorf("%w: length %d", ErrLexeme, len(s))
	}
	i := 0
	neg := false
	if s[i] == '+' || s[i] == '-' {
		neg = s[i] == '-'
		i++
	}
	intStart := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	intPart := s[intStart:i]
	fracPart := ""
	if i < len(s) && s[i] == '.' {
		i++
		fs := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		fracPart = s[fs:i]
		if len(fracPart) == 0 {
			return nil, fmt.Errorf("%w: empty fraction", ErrLexeme)
		}
	}
	if len(intPart) == 0 && len(fracPart) == 0 {
		return nil, fmt.Errorf("%w: no digits", ErrLexeme)
	}
	exp := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		eneg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			eneg = s[i] == '-'
			i++
		}
		es := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if es == i {
			return nil, fmt.Errorf("%w: empty exponent", ErrLexeme)
		}
		estr := s[es:i]
		if len(estr) > 6 {
			// |exp| >= 1e6: far beyond any quota. Zero mantissa is still 0;
			// a negative exponent of that size can never be an integer.
			if allZeroDigits(intPart + fracPart) {
				return big.NewInt(0), nil
			}
			if eneg {
				return nil, fmt.Errorf("%w: 10^-%s", ErrNotInt, estr)
			}
			return nil, fmt.Errorf("%w: 10^%s", ErrQuota, estr)
		}
		e, err := strconv.Atoi(estr)
		if err != nil {
			return nil, fmt.Errorf("%w: exponent", ErrLexeme)
		}
		if eneg {
			e = -e
		}
		exp = e
	}
	if i != len(s) {
		return nil, fmt.Errorf("%w: trailing %q", ErrLexeme, s[i:])
	}

	digits := strings.TrimLeft(intPart+fracPart, "0")
	if digits == "" {
		return big.NewInt(0), nil // canonical zero; sign of "-0" is dropped
	}
	shift := exp - len(fracPart)
	if shift < 0 {
		// The decimal point moves left into the digits: integer only if the
		// shifted-away digits are all zero (e.g. 1.000e3 -> 1000).
		need := -shift
		if need > len(digits) || !allZeroDigits(digits[len(digits)-need:]) {
			return nil, fmt.Errorf("%w: %q", ErrNotInt, s)
		}
		digits = digits[:len(digits)-need]
		shift = 0
	}
	// Quota check BEFORE big.Int conversion: the result has
	// len(digits)+shift significant decimal digits, and 10^(d-1) needs more
	// than maxBits bits once d exceeds floor(maxBits*log10(2))+1.
	sig := len(digits) + shift
	if limit := maxBits*30103/100000 + 2; sig > limit {
		return nil, fmt.Errorf("%w: ~%d significant digits for %d bits", ErrQuota, sig, maxBits)
	}
	v, ok := new(big.Int).SetString(digits+strings.Repeat("0", shift), 10)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrLexeme, s)
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func allZeroDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}
