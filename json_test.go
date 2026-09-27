package wireint

import (
	"bytes"
	"errors"
	"math/big"
	"strings"
	"testing"
)

func mustBig(t *testing.T, s string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad test literal %q", s)
	}
	return v
}

// TestTaggedStringRoundTrip: values cross JSON as tagged strings, so nothing
// passes through a 53-bit float.
func TestTaggedStringRoundTrip(t *testing.T) {
	fields := []Field{Uint8, Int16, Uint64, Int64, Big(300)}
	vals := []Value{
		{Int: big.NewInt(200), Field: fields[0]},
		{Int: big.NewInt(-12345), Field: fields[1]},
		{Int: mustBig(t, "18446744073709551615"), Field: fields[2]},
		{Int: big.NewInt(-9223372036854775808), Field: fields[3]},
		{Int: mustBig(t, "1606938044258990275541962092341162602522202993782792835301376"), Field: fields[4]},
	}
	var buf bytes.Buffer
	if err := EncodeJSONStream(&buf, vals); err != nil {
		t.Fatalf("EncodeJSONStream: %v", err)
	}
	if !strings.Contains(buf.String(), `"iw:u64:18446744073709551615"`) {
		t.Fatalf("max uint64 not carried as tagged string: %s", buf.String())
	}
	got, err := DecodeJSONStream(bytes.NewReader(buf.Bytes()), fields)
	if err != nil {
		t.Fatalf("DecodeJSONStream: %v", err)
	}
	for i, v := range got {
		if v.Int.Cmp(vals[i].Int) != 0 {
			t.Fatalf("element %d: got %v want %v", i, v.Int, vals[i].Int)
		}
		if v.Raw != vals[i].Int.String() {
			t.Fatalf("element %d: raw lexeme %q, want canonical %q", i, v.Raw, vals[i].Int.String())
		}
		if v.Field != fields[i] {
			t.Fatalf("element %d: constraint lost: %+v", i, v.Field)
		}
	}
}

// TestBeyond53BitsExact: 2^53+1 must survive the bridge exactly, both as a
// tagged string and as a raw JSON number lexeme.
func TestBeyond53BitsExact(t *testing.T) {
	fields := []Field{Uint64, Uint64}
	in := `["iw:u64:9007199254740993", 9007199254740993]`
	got, err := DecodeJSONStream(strings.NewReader(in), fields)
	if err != nil {
		t.Fatalf("DecodeJSONStream: %v", err)
	}
	want := mustBig(t, "9007199254740993") // 2^53 + 1
	for i, v := range got {
		if v.Int.Cmp(want) != 0 {
			t.Fatalf("element %d: got %v, want exactly 2^53+1", i, v.Int)
		}
	}
	if got[1].Raw != "9007199254740993" {
		t.Fatalf("raw lexeme not preserved: %q", got[1].Raw)
	}
}

// TestHugeExponentInteger: 1e100 is an integer and must be decoded exactly
// from its lexeme, never via a host float.
func TestHugeExponentInteger(t *testing.T) {
	fields := []Field{Big(400)}
	got, err := DecodeJSONStream(strings.NewReader(`[1e100]`), fields)
	if err != nil {
		t.Fatalf("DecodeJSONStream: %v", err)
	}
	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(100), nil)
	if got[0].Int.Cmp(want) != 0 {
		t.Fatalf("1e100 decoded as %v", got[0].Int)
	}
	if got[0].Raw != "1e100" {
		t.Fatalf("raw lexeme not preserved: %q", got[0].Raw)
	}
	// Shifted-point integer forms are integers too.
	v, err := ParseIntegerLexeme("1.000e3", 64)
	if err != nil || v.Int64() != 1000 {
		t.Fatalf("1.000e3: got %v err %v", v, err)
	}
	v, err = ParseIntegerLexeme("-2.5e1", 64)
	if err != nil || v.Int64() != -25 {
		t.Fatalf("-2.5e1: got %v err %v", v, err)
	}
}

// TestHugeExponentQuota: astronomically large exponents are rejected by the
// bit quota BEFORE any big.Int is materialized (and never via float).
func TestHugeExponentQuota(t *testing.T) {
	for _, lex := range []string{"1e999999999", "1e1000000", "9e99999", "1.1e5000"} {
		if _, err := ParseIntegerLexeme(lex, 256); !errors.Is(err, ErrQuota) {
			t.Fatalf("%s: got %v, want ErrQuota", lex, err)
		}
	}
	// Zero mantissa collapses to 0 regardless of exponent size.
	v, err := ParseIntegerLexeme("0e999999999", 64)
	if err != nil || v.Sign() != 0 {
		t.Fatalf("0e999999999: got %v err %v", v, err)
	}
	// Field-level: quota derives from the field's bit width.
	fields := []Field{Big(256)}
	if _, err := DecodeJSONStream(strings.NewReader(`[1e100]`), fields); !errors.Is(err, ErrQuota) {
		t.Fatalf("1e100 into big(256): got %v, want ErrQuota", err)
	}
}

// TestNonIntegerLexemesRejected: fractions that are not integers must not
// enter integer fields.
func TestNonIntegerLexemesRejected(t *testing.T) {
	for _, lex := range []string{"1.5", "1e-3", "-0.25", "3.14159", "1e-9999999", "2.5e0"} {
		if _, err := ParseIntegerLexeme(lex, 256); !errors.Is(err, ErrNotInt) {
			t.Fatalf("%s: got %v, want ErrNotInt", lex, err)
		}
	}
}

// TestLexemeLengthCheckedFirst: an over-long lexeme is rejected by the
// length check before any allocation or conversion.
func TestLexemeLengthCheckedFirst(t *testing.T) {
	long := "1" + strings.Repeat("0", maxLexemeLen) // 8193 chars
	if _, err := ParseIntegerLexeme(long, MaxBigBits); !errors.Is(err, ErrLexeme) {
		t.Fatalf("got %v, want ErrLexeme", err)
	}
}

// TestTaggedStringRejections: malformed tags, kind mismatch, non-canonical
// decimals, and negatives in unsigned fields are all rejected.
func TestTaggedStringRejections(t *testing.T) {
	cases := []struct {
		in    string
		field Field
		want  error
	}{
		{"iw:u8:-1", Uint8, ErrRange},         // negative into unsigned
		{"iw:u8:256", Uint8, ErrRange},        // out of range
		{"iw:u16:200", Uint8, ErrKindTag},     // kind mismatch
		{"iw:u8:007", Uint8, ErrNonCanonical}, // leading zeros
		{"iw:u8:+7", Uint8, ErrNonCanonical},  // plus sign
		{"iw:i8:-0", Int8, ErrNonCanonical},   // negative zero
		{"u8:200", Uint8, ErrTag},             // missing prefix
		{"iw:u9:1", Uint8, ErrTag},            // unknown kind
		{"iw:u64:1.5", Uint64, ErrLexeme},     // fraction in decimal slot
		{"iw:big:1", Big(0), ErrRange},        // undefined big width
	}
	for _, c := range cases {
		if _, err := ParseTagged(c.in, c.field); !errors.Is(err, c.want) {
			t.Fatalf("ParseTagged(%q): got %v, want %v", c.in, err, c.want)
		}
	}
}

// TestJSONStreamNegativeIntoUnsigned: the bridge enforces field signedness.
func TestJSONStreamNegativeIntoUnsigned(t *testing.T) {
	fields := []Field{Uint64}
	if _, err := DecodeJSONStream(strings.NewReader(`["iw:u64:-5"]`), fields); !errors.Is(err, ErrRange) {
		t.Fatalf("tagged: got %v, want ErrRange", err)
	}
	if _, err := DecodeJSONStream(strings.NewReader(`[-5]`), fields); !errors.Is(err, ErrRange) {
		t.Fatalf("lexeme: got %v, want ErrRange", err)
	}
}

// TestJSONStreamStructure: wrong shapes are rejected cleanly.
func TestJSONStreamStructure(t *testing.T) {
	fields := []Field{Uint8}
	for _, in := range []string{`{"a":1}`, `[true]`, `[[]]`, `[]`, `["iw:u8:1"`} {
		if _, err := DecodeJSONStream(strings.NewReader(in), fields); err == nil {
			t.Fatalf("%s: expected error", in)
		}
	}
}

// TestParseIntegerLexemeBasics covers plain forms and exactness.
func TestParseIntegerLexemeBasics(t *testing.T) {
	cases := map[string]string{
		"0":                "0",
		"-0":               "0",
		"42":               "42",
		"-17":              "-17",
		"1e3":              "1000",
		"2E2":              "200",
		"12300e-2":         "123",
		"1.5e1":            "15",
		"9007199254740993": "9007199254740993",
	}
	for in, want := range cases {
		v, err := ParseIntegerLexeme(in, 256)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if v.String() != want {
			t.Fatalf("%s: got %s want %s", in, v, want)
		}
	}
}
