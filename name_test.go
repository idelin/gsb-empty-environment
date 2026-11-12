package netsec

import (
	"bytes"
	"strings"
	"testing"
)

func mustFromLabels(t *testing.T, labels ...[]byte) *Name {
	t.Helper()
	n, err := FromLabels(labels...)
	if err != nil {
		t.Fatalf("FromLabels: %v", err)
	}
	return n
}

func mustParse(t *testing.T, input string) *Name {
	t.Helper()
	n, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q): %v", input, err)
	}
	return n
}

func labelsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Hostile labels: zero bytes, dot bytes, backslash, space, DEL,
// high bytes and invalid UTF-8 must survive a display round trip.
func TestDisplayRoundTripHostileLabels(t *testing.T) {
	cases := [][]byte{
		{0x00},
		{0x00, 0x00, 0x00},
		{'.'},
		{'a', '.', 'b'},
		{'\\'},
		{' ', 0x7f},
		{0xff, 0xfe},
		{0x80},
		{0xc3, 0x28}, // invalid UTF-8: truncated sequence
		{0xe2, 0x82}, // invalid UTF-8: incomplete
		{'x', 0x00, '.', 0xff, '\\', 'y'},
		{0xc3, 0x84}, // valid UTF-8 "Ä", kept as raw bytes
	}
	for _, label := range cases {
		n := mustFromLabels(t, label, []byte("Evidence.COM"))
		display := n.String()
		back, err := Parse(display)
		if err != nil {
			t.Fatalf("label %x: Parse(%q): %v", label, display, err)
		}
		if !labelsEqual(back.Labels(), n.Labels()) {
			t.Fatalf("label %x: round trip gave %x", label, back.Labels())
		}
		if back.Key() != n.Key() {
			t.Fatalf("label %x: key changed across round trip", label)
		}
		if again := back.String(); again != display {
			t.Fatalf("label %x: display not stable: %q vs %q", label, again, display)
		}
	}
}

// A dot byte inside a label must be escaped so the display form never
// turns one label into two.
func TestSeparatorNeverLeaksIntoLabel(t *testing.T) {
	n := mustFromLabels(t, []byte("a.b"))
	display := n.String()
	if display != `a\.b` {
		t.Fatalf("display = %q, want %q", display, `a\.b`)
	}
	back := mustParse(t, display)
	if got := back.Labels(); len(got) != 1 || !bytes.Equal(got[0], []byte("a.b")) {
		t.Fatalf("reparsed labels = %x, want single label a.b", got)
	}
}

func TestCacheKeyUnambiguous(t *testing.T) {
	distinct := []struct {
		name string
		a, b *Name
	}{
		{"dot byte vs separator",
			mustFromLabels(t, []byte("a.b")),
			mustFromLabels(t, []byte("a"), []byte("b"))},
		{"zero byte vs none",
			mustFromLabels(t, []byte("a\x00b")),
			mustFromLabels(t, []byte("ab"))},
		{"zero byte vs dot byte",
			mustFromLabels(t, []byte("a\x00b")),
			mustFromLabels(t, []byte("a.b"))},
		{"non-ASCII case not folded",
			mustFromLabels(t, []byte{0xc3, 0x84}), // Ä
			mustFromLabels(t, []byte{0xc3, 0xa4})},
		{"high bytes not folded",
			mustFromLabels(t, []byte{0xc1}),
			mustFromLabels(t, []byte{0xe1})},
		{"invalid UTF-8 distinct",
			mustFromLabels(t, []byte{0xff}),
			mustFromLabels(t, []byte{0xfe})},
		{"label split ambiguity",
			mustFromLabels(t, []byte("ab"), []byte("c")),
			mustFromLabels(t, []byte("a"), []byte("bc"))},
	}
	for _, tc := range distinct {
		if tc.a.Key() == tc.b.Key() {
			t.Fatalf("%s: keys collide: %q", tc.name, tc.a.Key())
		}
		if tc.a.Equal(tc.b) {
			t.Fatalf("%s: names reported equal", tc.name)
		}
	}
}

func TestASCIICaseFoldingOnly(t *testing.T) {
	upper := mustParse(t, "ExAmPLE.COM")
	lower := mustParse(t, "example.com")
	if upper.Key() != lower.Key() || !upper.Equal(lower) {
		t.Fatal("ASCII case variants must share a key")
	}
	// Original evidence bytes are preserved despite folding.
	if got := upper.Labels(); !bytes.Equal(got[0], []byte("ExAmPLE")) {
		t.Fatalf("original label bytes lost: %x", got[0])
	}
	if upper.String() != "ExAmPLE.COM" {
		t.Fatalf("display altered original case: %q", upper.String())
	}
}

func TestTrailingDotAndRoot(t *testing.T) {
	if mustParse(t, "Example.COM.").Key() != mustParse(t, "example.com").Key() {
		t.Fatal("trailing dot must not change the key")
	}
	for _, in := range []string{"", "."} {
		n := mustParse(t, in)
		if n.String() != "." || len(n.Labels()) != 0 {
			t.Fatalf("root %q: display %q labels %v", in, n.String(), n.Labels())
		}
	}
}

// Length limits apply to decoded bytes: 63 escaped zero bytes form a
// legal 63-byte label even though the input string is 256 bytes long.
func TestLengthLimitsCheckedAfterDecoding(t *testing.T) {
	esc := strings.Repeat(`\000`, MaxLabelLength)
	n := mustParse(t, esc)
	if got := n.Labels(); len(got) != 1 || len(got[0]) != MaxLabelLength {
		t.Fatalf("decoded label length = %d", len(got[0]))
	}
	if _, err := Parse(strings.Repeat(`\000`, MaxLabelLength+1)); err == nil {
		t.Fatal("64-byte decoded label accepted")
	}
	if _, err := FromLabels(make([]byte, MaxLabelLength+1)); err == nil {
		t.Fatal("64-byte raw label accepted")
	}

	// Wire length: 1 + (1+63)*3 + (1+61) = 255 is legal, 256 is not.
	fit := [][]byte{
		make([]byte, 63), make([]byte, 63), make([]byte, 63), make([]byte, 61),
	}
	if _, err := FromLabels(fit...); err != nil {
		t.Fatalf("255-byte wire name rejected: %v", err)
	}
	over := [][]byte{
		make([]byte, 63), make([]byte, 63), make([]byte, 63), make([]byte, 62),
	}
	if _, err := FromLabels(over...); err == nil {
		t.Fatal("256-byte wire name accepted")
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		`a\`,   // trailing backslash
		`\12`,  // truncated decimal escape
		`\1x3`, // malformed decimal escape
		`\999`, // out of range
		"a..b", // empty interior label
		".a",   // empty leading label
		`a\.`,  // escaped dot then nothing: still fine? no—see below
	} {
		if in == `a\.` {
			continue // `a\.` is a legal single label "a."
		}
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) succeeded, want error", in)
		}
	}
	if _, err := Parse(`a\.`); err != nil {
		t.Fatalf(`Parse("a\\.") should yield label "a.": %v`, err)
	}
}

func TestLabelsAreCopied(t *testing.T) {
	src := []byte("host")
	n := mustFromLabels(t, src)
	src[0] = 'X'
	if got := n.Labels()[0]; !bytes.Equal(got, []byte("host")) {
		t.Fatalf("name aliases caller buffer: %q", got)
	}
	got := n.Labels()
	got[0][0] = 'Y'
	if again := n.Labels()[0]; !bytes.Equal(again, []byte("host")) {
		t.Fatalf("Labels exposes internal buffer: %q", again)
	}
}
