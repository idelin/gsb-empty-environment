package dnssec

import (
	"bytes"
	"testing"
)

func TestBase32HexUnpadded(t *testing.T) {
	hash := bytes.Repeat([]byte{0xa5}, 20)
	encoded := EncodeBase32Hex(hash)
	if len(encoded) != 32 {
		t.Fatalf("encoded length = %d, want 32", len(encoded))
	}
	if bytes.IndexByte([]byte(encoded), '=') >= 0 {
		t.Fatal("Base32hex output is padded")
	}
	decoded, err := DecodeBase32Hex(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, hash) {
		t.Fatal("Base32hex round trip mismatch")
	}
	lower, err := DecodeBase32Hex("00")
	if err != nil {
		t.Fatal(err)
	}
	if len(lower) != 1 {
		t.Fatalf("00 decoded length = %d", len(lower))
	}
	if _, err := DecodeBase32Hex(encoded + "="); err == nil {
		t.Fatal("padding was accepted")
	}
}

func TestSaltHexPresentation(t *testing.T) {
	salt, err := ParseSaltHex("AABBCCDD")
	if err != nil {
		t.Fatal(err)
	}
	if got := EncodeSaltHex(salt); got != "AABBCCDD" {
		t.Fatalf("salt = %s", got)
	}
	empty, err := ParseSaltHex("-")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty salt: %v, %d", err, len(empty))
	}
}

func TestNSEC3ParameterValidation(t *testing.T) {
	valid := NSEC3{
		HashAlgorithm: NSEC3HashSHA1,
		Flags:         0,
		Iterations:    12,
		Salt:          []byte{0xaa, 0xbb},
		NextHash:      bytes.Repeat([]byte{1}, 20),
	}
	if err := ValidateNSEC3Parameters(valid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		n    NSEC3
	}{
		{"algorithm", NSEC3{HashAlgorithm: 2, NextHash: valid.NextHash}},
		{"flags", NSEC3{HashAlgorithm: 1, Flags: 0x02, NextHash: valid.NextHash}},
		{"iterations", NSEC3{HashAlgorithm: 1, Iterations: DefaultMaxIterations + 1, NextHash: valid.NextHash}},
		{"salt", NSEC3{HashAlgorithm: 1, Salt: bytes.Repeat([]byte{1}, 256), NextHash: valid.NextHash}},
		{"next hash", NSEC3{HashAlgorithm: 1, NextHash: []byte{1}}},
	}
	for _, tc := range cases {
		if err := ValidateNSEC3Parameters(tc.n); err == nil {
			t.Errorf("%s validation unexpectedly succeeded", tc.name)
		}
	}
	if err := ValidateNSEC3Parameters(valid, 11); err == nil {
		t.Fatal("custom iteration limit was ignored")
	}
}

func TestNSEC3CircularCoverage(t *testing.T) {
	low := []byte{0x10}
	high := []byte{0x80}
	if !NSEC3CoversHash(low, high, []byte{0x40}) {
		t.Fatal("ordinary coverage failed")
	}
	for _, endpoint := range [][]byte{low, high} {
		if NSEC3CoversHash(low, high, endpoint) {
			t.Fatalf("exclusive endpoint %x was covered", endpoint)
		}
	}
	wrapOwner := []byte{0xf0}
	wrapNext := []byte{0x10}
	for _, target := range [][]byte{{0x00}, {0x08}, {0xf8}, {0xff}} {
		if !NSEC3CoversHash(wrapOwner, wrapNext, target) {
			t.Fatalf("wrap coverage failed for %x", target)
		}
	}
	for _, target := range [][]byte{{0x20}, {0x80}, {0xf0}, {0x10}} {
		if NSEC3CoversHash(wrapOwner, wrapNext, target) {
			t.Fatalf("wrap interval covered %x", target)
		}
	}
}

func TestNSEC3HashIterations(t *testing.T) {
	name := MustParseName("example.com.")
	salt := []byte{0xaa, 0xbb}
	h0, err := HashNSEC3(name, salt, 0)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashNSEC3(name, salt, 2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(h0, h2) {
		t.Fatal("iteration count did not affect hash")
	}
	if _, err := HashNSEC3WithLimit(name, salt, 251, 250); err == nil {
		t.Fatal("hash iteration limit was ignored")
	}
}

func TestNSEC3TypeBitmapRoundTrip(t *testing.T) {
	types := []uint16{TypeA, TypeNS, TypeSOA, TypeRRSIG, TypeNSEC3, 300}
	bitmap, err := MarshalTypeBitmap(types)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseTypeBitmap(bitmap)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(types) {
		t.Fatalf("decoded %d types, want %d", len(decoded), len(types))
	}
	for i := range types {
		if decoded[i] != types[i] {
			t.Fatalf("type %d = %d, want %d", i, decoded[i], types[i])
		}
	}
	if _, err := ParseTypeBitmap([]byte{0, 1, 0}); err == nil {
		t.Fatal("trailing zero bitmap octet was accepted")
	}
}

func TestParseNSEC3RejectsInvalidParameters(t *testing.T) {
	valid := NSEC3{
		HashAlgorithm: NSEC3HashSHA1,
		Flags:         NSEC3FlagOptOut,
		Iterations:    12,
		Salt:          []byte{0xaa},
		NextHash:      bytes.Repeat([]byte{0x11}, 20),
		Types:         []uint16{TypeA},
	}
	parsed, err := ParseNSEC3(valid.RData())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.HashAlgorithm != 1 || parsed.Iterations != 12 || !bytes.Equal(parsed.NextHash, valid.NextHash) {
		t.Fatalf("parsed NSEC3 mismatch: %+v", parsed)
	}

	badAlg := valid
	badAlg.HashAlgorithm = 2
	if _, err := ParseNSEC3(badAlg.RData()); err == nil {
		t.Fatal("unknown NSEC3 hash algorithm was parsed")
	}
	badFlags := valid
	badFlags.Flags = 0x80
	if _, err := ParseNSEC3(badFlags.RData()); err == nil {
		t.Fatal("unknown NSEC3 flags were parsed")
	}
	badHash := valid
	badHash.NextHash = []byte{1, 2, 3}
	if _, err := ParseNSEC3(badHash.RData()); err == nil {
		t.Fatal("short NSEC3 next hash was parsed")
	}
}
