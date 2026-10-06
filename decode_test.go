package qstr

import (
	"strings"
	"testing"
)

// alphabet covers every character class the format treats specially.
var alphabet = []string{
	"a", "Z", "0", " ", "u", "A",
	"\"", "\\",
	"\n", "\t", "\r", "\x00", "\x1f", "\x7f",
	"é",      // 2-byte UTF-8
	"中",      // 3-byte UTF-8
	"😀",      // 4-byte UTF-8 (surrogate pair in UTF-16)
	"\ufffd", // replacement char
}

func allShortStrings() []string {
	out := []string{""}
	for _, a := range alphabet {
		out = append(out, a)
	}
	for _, a := range alphabet {
		for _, b := range alphabet {
			out = append(out, a+b)
		}
	}
	for _, a := range alphabet {
		for _, b := range alphabet {
			for _, c := range alphabet {
				out = append(out, a+b+c)
			}
		}
	}
	return out
}

func TestRoundTripExhaustive(t *testing.T) {
	for _, s := range allShortStrings() {
		enc := Encode(s)
		dec, n, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(%q) (from %q): %v", enc, s, err)
		}
		if n != len(enc) {
			t.Fatalf("Decode(%q): consumed %d of %d bytes", enc, n, len(enc))
		}
		if dec != s {
			t.Fatalf("round trip of %q: got %q", s, dec)
		}
		// Trailing bytes after the closing quote are not consumed.
		if _, n2, err := Decode(enc + "zz"); err != nil || n2 != len(enc) {
			t.Fatalf("Decode(%q+zz): n=%d err=%v, want n=%d", enc, n2, err, len(enc))
		}
	}
}

func TestDecodeDoesNotReinterpret(t *testing.T) {
	// Escapes resolve exactly once: decoded output must never be
	// re-scanned for further escapes.
	cases := []struct{ wire, want string }{
		{`"\\"`, `\`},
		{`"\""`, `"`},
		{`"\\n"`, `\n`},         // literal backslash+n, not newline
		{`"\\u0041"`, `\u0041`}, // literal escape text, not "A"
		{`"\\\\"`, `\\`},        // two literal backslashes
		{`"\\\""`, `\"`},        // literal backslash+quote
		{`"\u000a"`, "\n"},      // the escape itself decodes once
		{`"\\u000a"`, `\u000a`}, // ... and its quoted form does not
		{`"\u0041"`, "A"},
		{`"\uD83D\uDE00"`, "😀"}, // surrogate pair escape
		{`"a😀b"`, "a😀b"},        // raw 4-byte UTF-8 passes through
	}
	for _, c := range cases {
		got, n, err := Decode(c.wire)
		if err != nil {
			t.Fatalf("Decode(%q): %v", c.wire, err)
		}
		if n != len(c.wire) {
			t.Fatalf("Decode(%q): consumed %d, want %d", c.wire, n, len(c.wire))
		}
		if got != c.want {
			t.Fatalf("Decode(%q) = %q, want %q", c.wire, got, c.want)
		}
	}
	// Round trip of text that itself looks like wire format.
	for _, s := range []string{`\u0041`, `"\uD83D\uDE00"`, `\\`, `"a"`} {
		if dec, _, err := Decode(Encode(s)); err != nil || dec != s {
			t.Fatalf("round trip of %q: dec=%q err=%v", s, dec, err)
		}
	}
}

func TestDecodeErrorOffsets(t *testing.T) {
	cases := []struct {
		wire       string
		start, end int
		msgPart    string
	}{
		{`"a\q"`, 2, 4, "unknown escape"},
		{`"a\u00g0"`, 6, 7, "invalid hex digit"},
		{`"a\u00G0"`, 6, 7, "invalid hex digit"},
		{`"\u12"`, 5, 6, "invalid hex digit"},
		{`"\u12`, 1, 5, `truncated \u escape`},
		{`"\uD800"`, 1, 7, "lone high surrogate"},
		{`"\uDC00"`, 1, 7, "lone low surrogate"},
		{`"\uD800x"`, 1, 7, "lone high surrogate"},
		{`"\uD800\u0041"`, 7, 13, "invalid low surrogate"},
		{`"\uD800\uD800"`, 7, 13, "invalid low surrogate"},
		{"\"a\x01b\"", 2, 3, "unescaped control character"},
		{"\"a\x7fb\"", 2, 3, "unescaped control character"},
		{"\"a\xff\"", 2, 3, "invalid UTF-8"},
		{`"abc`, 4, 4, "unterminated string"},
		{`"ab\`, 3, 4, "truncated escape"},
		{`abc`, 0, 1, "expected opening quote"},
		{``, 0, 0, "expected opening quote"},
	}
	for _, c := range cases {
		_, _, err := Decode(c.wire)
		qe, ok := err.(*Error)
		if !ok {
			t.Fatalf("Decode(%q): err=%v, want *Error", c.wire, err)
		}
		if qe.Start != c.start || qe.End != c.end {
			t.Fatalf("Decode(%q): offsets [%d,%d), want [%d,%d)",
				c.wire, qe.Start, qe.End, c.start, c.end)
		}
		if !strings.Contains(qe.Msg, c.msgPart) {
			t.Fatalf("Decode(%q): msg %q, want substring %q", c.wire, qe.Msg, c.msgPart)
		}
		if qe.Start < 0 || qe.End > len(c.wire) || qe.Start > qe.End {
			t.Fatalf("Decode(%q): offsets [%d,%d) out of bounds", c.wire, qe.Start, qe.End)
		}
	}
}

func TestBadEscapeMutations(t *testing.T) {
	// Every single-hex-digit corruption of a \u escape must fail at the
	// mutated byte.
	bases := []string{`"\u0041"`, `"\uD83D\uDE00"`, `"xy\u00ffz"`}
	for _, base := range bases {
		for i := 0; i < len(base); i++ {
			if !isHex(base[i]) {
				continue
			}
			m := base[:i] + "g" + base[i+1:]
			_, _, err := Decode(m)
			qe, ok := err.(*Error)
			if !ok {
				t.Fatalf("Decode(%q): err=%v, want *Error", m, err)
			}
			if qe.Start != i || qe.End != i+1 {
				t.Fatalf("Decode(%q): offsets [%d,%d), want [%d,%d)",
					m, qe.Start, qe.End, i, i+1)
			}
		}
	}
	// Every proper prefix of a valid record must fail, never panic.
	for _, base := range []string{`"\u0041"`, `"\uD83D\uDE00"`, Encode("a中😀\\\"\x00")} {
		for i := 0; i < len(base); i++ {
			if _, _, err := Decode(base[:i]); err == nil {
				t.Fatalf("Decode(%q) succeeded, want error", base[:i])
			}
		}
	}
	// Inserting a lone surrogate escape inside the quotes must be
	// rejected. Positions 1 and len(enc)-1 always fall on escape
	// boundaries of the encoded form.
	for _, s := range allShortStrings() {
		enc := Encode(s)
		for _, pos := range []int{1, len(enc) - 1} {
			for _, ins := range []string{`\uD800`, `\uDC00`, `\uDBFF`, `\uDFFF`} {
				m := enc[:pos] + ins + enc[pos:]
				if _, _, err := Decode(m); err == nil {
					t.Fatalf("Decode(%q) accepted lone surrogate %q", m, ins)
				}
			}
		}
	}
}

func TestDecodeConsumed(t *testing.T) {
	dec, n, err := Decode(`"a""b"`)
	if err != nil || dec != "a" || n != 3 {
		t.Fatalf("got %q, %d, %v", dec, n, err)
	}
	dec, n, err = Decode(`""`)
	if err != nil || dec != "" || n != 2 {
		t.Fatalf("got %q, %d, %v", dec, n, err)
	}
}
