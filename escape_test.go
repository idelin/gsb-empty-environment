package stresc

import (
	"errors"
	"strings"
	"testing"
)

// alphabet covers quotes, backslashes, control characters, ASCII, and
// multi-byte runes including astral-plane characters.
var alphabet = []string{
	"a", "Z", "0", " ", "~", "`",
	"\"", "\\",
	"\n", "\r", "\t", "\a", "\b", "\f", "\v",
	"\x00", "\x01", "\x1f", "\x7f",
	"é", "ü", "世", "界", "\u0080", "\u07ff", "\u0800", "\uffff",
	"😀", "\U0001F600", "\U00010000",
}

func TestRoundTripShortCombos(t *testing.T) {
	check := func(s string) {
		t.Helper()
		enc := Encode(nil, s)
		dec, n, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(%q) of %q: %v", enc, s, err)
		}
		if n != len(enc) {
			t.Fatalf("Decode(%q): consumed %d of %d bytes", enc, n, len(enc))
		}
		if dec != s {
			t.Fatalf("round trip: %q -> %q -> %q", s, enc, dec)
		}
		// Re-encoding the decoded text must reproduce the identical
		// encoding: decoding must not yield anything that a second
		// pass would turn into new escapes.
		if re := Encode(nil, dec); string(re) != string(enc) {
			t.Fatalf("re-encode of %q: %q != %q", s, re, enc)
		}
	}
	for _, a := range alphabet {
		check(a)
	}
	for _, a := range alphabet {
		for _, b := range alphabet {
			check(a + b)
		}
	}
	for _, a := range alphabet {
		for _, b := range alphabet[:8] {
			check(a + b + a)
		}
	}
}

func TestBadEscapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		off  int
		msg  string
	}{
		{"unknown escape", `"\q"`, 2, "unknown escape"},
		{"unknown escape caret", `"\^"`, 2, "unknown escape"},
		{"bad hex first", `"\xzz"`, 3, "invalid hex digit"},
		{"bad hex second", `"\x1z"`, 4, "invalid hex digit"},
		{"bad hex quote", `"\x1"`, 4, "invalid hex digit"},
		{"bad unicode hex", `"\u12z4"`, 5, "invalid hex digit"},
		{"bad unicode hex first", `"\uzzzz"`, 3, "invalid hex digit"},
		{"non-ascii x 80", `"\x80"`, 1, "non-ASCII"},
		{"non-ascii x ff", `"\xff"`, 1, "non-ASCII"},
		{"lone high surrogate", `"\uD800"`, 1, "lone high surrogate"},
		{"lone high then bmp", `"\uD800\u0041"`, 1, "lone high surrogate"},
		{"lone high then escape", `"\uD800\n"`, 1, "lone high surrogate"},
		{"isolated low surrogate", `"\uDC00"`, 1, "isolated low surrogate"},
		{"pair then lone", `"\uD83D\uDE00\uD800"`, 13, "lone high surrogate"},
		{"raw control", "\"a\x01b\"", 2, "unescaped control"},
		{"raw del", "\"a\x7fb\"", 2, "unescaped control"},
		{"raw newline", "\"a\nb\"", 2, "unescaped control"},
		{"invalid utf8", "\"a\xff\"", 2, "invalid UTF-8"},
		{"utf8 surrogate lo", "\"\xed\xa0\x80\"", 1, "isolated surrogate"},
		{"utf8 surrogate hi", "\"\xed\xbf\xbf\"", 1, "isolated surrogate"},
		{"unterminated", `"abc`, 4, "unexpected end"},
		{"trailing backslash", `"a\`, 3, "unexpected end"},
		{"truncated x", `"\x1`, 4, "unexpected end"},
		{"truncated u", `"\u12`, 5, "unexpected end"},
		{"truncated surrogate pair", `"\uD83D`, 7, "unexpected end"},
		{"not quoted", `abc`, 0, "expected opening quote"},
		{"empty", ``, 0, "unexpected end"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode([]byte(tc.in))
			if err == nil {
				t.Fatalf("Decode(%q): expected error", tc.in)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Decode(%q): error is %T, want *Error", tc.in, err)
			}
			if e.Offset != tc.off {
				t.Fatalf("Decode(%q): offset %d, want %d", tc.in, e.Offset, tc.off)
			}
			if !strings.Contains(e.Msg, tc.msg) {
				t.Fatalf("Decode(%q): msg %q, want substring %q", tc.in, e.Msg, tc.msg)
			}
		})
	}
}

// TestBadEscapeMutations mutates valid encodings at every byte position
// and checks that failures carry in-range offsets while successes stay
// stable under re-decoding.
func TestBadEscapeMutations(t *testing.T) {
	seeds := []string{
		"hello world", "quote\"back\\slash", "ctrl\x00\x1f\x7f",
		"unicode é世😀", "pair \U0001F600 end",
	}
	var inputs [][]byte
	for _, s := range seeds {
		inputs = append(inputs, Encode(nil, s))
	}
	// Valid encodings that use decoder-only escape forms.
	inputs = append(inputs,
		[]byte(`"\u0041\u00e9"`),
		[]byte(`"\uD83D\uDE00"`),
		[]byte(`"\x41\x42"`),
	)
	mutators := []byte{'\\', '"', 'x', 'u', 'g', 'z', 'D', '8', '0', 'f', 0x00, 0x01, 0x7f, 0x80, 0xff}
	check := func(m []byte) {
		t.Helper()
		dec, _, err := Decode(m)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Decode(%q): error is %T, want *Error", m, err)
			}
			if e.Offset < 0 || e.Offset > len(m) {
				t.Fatalf("Decode(%q): offset %d out of range", m, e.Offset)
			}
			return
		}
		// A successful decode must re-encode to text that decodes to
		// the same value: a second decode must not produce new escapes.
		re := Encode(nil, dec)
		dec2, n2, err2 := Decode(re)
		if err2 != nil || dec2 != dec || n2 != len(re) {
			t.Fatalf("re-decode of %q (from %q): %q, %d, %v", re, m, dec2, n2, err2)
		}
	}
	for _, in := range inputs {
		for i := range in {
			for _, b := range mutators {
				m := append([]byte(nil), in...)
				m[i] = b
				check(m)
			}
		}
		for i := 0; i <= len(in); i++ {
			m := append([]byte(nil), in[:i]...)
			m = append(m, '\\')
			m = append(m, in[i:]...)
			check(m)
		}
		for i := 0; i < len(in); i++ {
			check(in[:i])
		}
	}
}

// TestNoDoubleDecodeEscapes pins the canonical-form invariant on
// tricky inputs: decode(encode(x)) == x and a re-encoded decode never
// gains or loses escapes.
func TestNoDoubleDecodeEscapes(t *testing.T) {
	inputs := []string{
		`"already quoted"`, `\backslash\`, "mixed \" \\ \n \x00 é😀",
		`"\u0041 looks like an escape"`, `\x41 literal`,
	}
	for _, s := range inputs {
		enc := Encode(nil, s)
		dec, _, err := Decode(enc)
		if err != nil || dec != s {
			t.Fatalf("round trip of %q: %q, %v", s, dec, err)
		}
		if re := Encode(nil, dec); string(re) != string(enc) {
			t.Fatalf("re-encode of %q: %q != %q", s, re, enc)
		}
	}
}
