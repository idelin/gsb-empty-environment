package nsec3

import (
	"encoding/base32"
	"strings"
	"testing"
)

func TestBase32HexRoundTrip(t *testing.T) {
	// SHA-1 length (the NSEC3 case) plus generic lengths incl. partial
	// quanta.
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 10, 20, 31, 32, 33, 100} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i*7 + 3)
		}
		enc := Base32HexEncode(data)
		dec, err := Base32HexDecode(enc)
		if err != nil {
			t.Fatalf("n=%d decode %q: %v", n, enc, err)
		}
		if string(dec) != string(data) {
			t.Fatalf("n=%d round trip mismatch", n)
		}
	}
}

func TestBase32HexAgainstStdlib(t *testing.T) {
	// Use the standard library's extended-hex encoder (with padding) as
	// an independent oracle.
	enc := base32.HexEncoding.WithPadding(base32.NoPadding)
	data := []byte{0x00, 0x10, 0x80, 0xff, 0xaa, 0x55, 0x01, 0x9f}
	// RFC 5155 owner labels use the lower-case form of the alphabet.
	want := strings.ToLower(enc.EncodeToString(data))
	if got := Base32HexEncode(data); got != want {
		t.Errorf("encode = %q, want %q", got, want)
	}
	// Upper-case accepted.
	dec, err := Base32HexDecode(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(data) {
		t.Fatalf("decode mismatch: %x", dec)
	}
	upper, err := Base32HexDecode(toUpper(want))
	if err != nil || string(upper) != string(data) {
		t.Fatalf("upper-case decode failed: %v %x", err, upper)
	}
}

func TestBase32HexReject(t *testing.T) {
	for _, in := range []string{"z", "w", "!!!", "0p9m/have"} {
		if _, err := Base32HexDecode(in); err == nil {
			t.Errorf("decode(%q) unexpectedly succeeded", in)
		}
	}
	// Non-zero trailing padding bits.
	if _, err := Base32HexDecode("01"); err == nil {
		t.Error("non-canonical trailing bits accepted")
	}
}

func toUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}
