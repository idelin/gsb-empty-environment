package mdoc

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEncodeGoldenVectors(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"zero", uint64(0), "00"},
		{"one", uint64(1), "01"},
		{"ten", uint64(10), "0a"},
		{"uint one byte", uint64(24), "1818"},
		{"uint two bytes", uint64(1000), "1903e8"},
		{"negative one", int64(-1), "20"},
		{"negative ten", int64(-10), "29"},
		{"negative 100", int64(-100), "3863"},
		{"empty bytes", []byte{}, "40"},
		{"bytes", []byte{1, 2, 3}, "43010203"},
		{"empty string", "", "60"},
		{"string", "a", "6161"},
		{"IETF", "IETF", "6449455446"},
		{"empty array", []any{}, "80"},
		{"array", []any{uint64(1), uint64(2), uint64(3)}, "83010203"},
		{"empty map", map[string]any{}, "a0"},
		{"bool true", true, "f5"},
		{"bool false", false, "f4"},
		{"nil", nil, "f6"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.value)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if tc.want != "" {
				if hex.EncodeToString(got) != tc.want {
					t.Fatalf("Encode = %x, want %s", got, tc.want)
				}
			}
		})
	}

	// RFC 8949 Appendix A example: {"a": 1, "b": [2, 3]}
	got, err := Encode(map[string]any{
		"a": uint64(1),
		"b": []any{uint64(2), uint64(3)},
	})
	if err != nil {
		t.Fatalf("Encode nested map: %v", err)
	}
	if want := "a26161016162820203"; hex.EncodeToString(got) != want {
		t.Fatalf("nested map = %x, want %s", got, want)
	}

	// Long string forces a two-byte length head.
	long := make([]byte, 24)
	for i := range long {
		long[i] = 'x'
	}
	encoded, err := Encode(string(long))
	if err != nil {
		t.Fatalf("Encode long string: %v", err)
	}
	if want := "7818" + hex.EncodeToString(long); hex.EncodeToString(encoded) != want {
		t.Fatalf("long string = %x, want %s", encoded, want)
	}

	// A 24-byte bstr uses the one-byte-argument head (major 2, info 24).
	bstr24, err := Encode(make([]byte, 24))
	if err != nil {
		t.Fatalf("Encode 24-byte bstr: %v", err)
	}
	if want := "5818" + hex.EncodeToString(make([]byte, 24)); hex.EncodeToString(bstr24) != want {
		t.Fatalf("24-byte bstr = %x, want %s", bstr24, want)
	}

	// A 23-byte bstr still uses an inline length argument.
	bstr23, err := Encode(make([]byte, 23))
	if err != nil {
		t.Fatalf("Encode 23-byte bstr: %v", err)
	}
	if got := hex.EncodeToString(bstr23); got != "57"+hex.EncodeToString(make([]byte, 23)) {
		t.Fatalf("23-byte bstr = %s", got)
	}
}

func TestEncodeMapCanonicalOrder(t *testing.T) {
	value := map[string]any{
		"z": uint64(1),
		"a": uint64(2),
		"m": uint64(3),
	}
	first, err := Encode(value)
	if err != nil {
		t.Fatalf("first Encode: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := Encode(value)
		if err != nil {
			t.Fatalf("repeat Encode: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("non-deterministic map encoding: %x vs %x", first, again)
		}
	}
	// Keys "a" (0x6161), "m" (0x616d), "z" (0x617a) sort bytewise.
	if want := "a3616102616d03617a01"; hex.EncodeToString(first) != want {
		t.Fatalf("map encoding = %x, want %s", first, want)
	}

	// Integer-keyed maps use the same bytewise key ordering.
	numbered, err := Encode(map[int64]any{
		100:  uint64(1),
		1:    uint64(2),
		1000: uint64(3),
	})
	if err != nil {
		t.Fatalf("integer map Encode: %v", err)
	}
	if want := "a301021864011903e803"; hex.EncodeToString(numbered) != want {
		t.Fatalf("integer map encoding = %x, want %s", numbered, want)
	}
}

func TestEncodeTagAndRawCBOR(t *testing.T) {
	// tag 24 wraps a bstr containing the encoded integer 7.
	encoded, err := Encode(Tag{ID: 24, Content: []byte{0x07}})
	if err != nil {
		t.Fatalf("Encode tag: %v", err)
	}
	if want := "d8184107"; hex.EncodeToString(encoded) != want {
		t.Fatalf("tag encoding = %x, want %s", encoded, want)
	}

	inner, err := Encode(uint64(42))
	if err != nil {
		t.Fatalf("Encode inner: %v", err)
	}
	outer, err := Encode([]any{"st", RawCBOR(inner)})
	if err != nil {
		t.Fatalf("Encode RawCBOR: %v", err)
	}
	if want := "82627374182a"; hex.EncodeToString(outer) != want {
		t.Fatalf("RawCBOR encoding = %x, want %s", outer, want)
	}
}

func TestValidCBOR(t *testing.T) {
	cases := []struct {
		hex  string
		want bool
	}{
		{"00", true},
		{"83010203", true},
		{"a26161016162820203", true},
		{"d8184107", true},
		{"9f0102ff", true},   // indefinite array
		{"bf616101ff", true}, // indefinite map
		{"", false},
		{"1f", false},   // reserved argument
		{"0001", false}, // trailing bytes
		{"1903", false}, // truncated argument
		{"8201", false}, // truncated array
		{"7f", false},   // indefinite string
	}
	for _, tc := range cases {
		data, err := hex.DecodeString(tc.hex)
		if err != nil {
			t.Fatalf("decode %s: %v", tc.hex, err)
		}
		if got := ValidCBOR(data); got != tc.want {
			t.Fatalf("ValidCBOR(%s) = %v, want %v", tc.hex, got, tc.want)
		}
	}
}

func TestIssuerItemDigestKnownAnswer(t *testing.T) {
	item := IssuerSignedItem{
		DigestID:          0,
		Random:            []byte("0123456789abcdef"),
		ElementIdentifier: "family_name",
		ElementValue:      "Smith",
	}
	digest, err := issuerItemDigest(func(algorithm string, data []byte) ([]byte, error) {
		if algorithm != "SHA-256" {
			t.Fatalf("algorithm = %q, want SHA-256", algorithm)
		}
		sum := sha256.Sum256(data)
		return sum[:], nil
	}, "SHA-256", item)
	if err != nil {
		t.Fatalf("issuerItemDigest: %v", err)
	}

	// Independently rebuild the expected bytes without the production encoder
	// helpers used by issuerItemDigest.
	head := func(major byte, n int) []byte {
		return encodeHex(t, hex.EncodeToString([]byte{major<<5 | byte(n)}))
	}
	text := func(s string) []byte {
		return encodeHex(t, hex.EncodeToString(head(3, len(s))), hex.EncodeToString([]byte(s)))
	}
	plainParts := [][]byte{
		head(4, 8), // IssuerSignedItem is an 8-element array
		text("digestID"),
		encodeHex(t, "00"),
		text("random"),
		encodeHex(t, "50"+hex.EncodeToString(item.Random)),
		text("elementIdentifier"),
		text(item.ElementIdentifier),
		text("elementValue"),
		text(item.ElementValue.(string)),
	}
	var plain []byte
	for _, part := range plainParts {
		plain = append(plain, part...)
	}
	if len(plain) != 84 {
		t.Fatalf("plain length = %d, want 84", len(plain))
	}
	// An 84-byte bstr uses the one-byte-argument head 0x58 0x54 (major type 2,
	// additional info 24, argument 84); a bare 0x54 would only encode length
	// 20, since arguments below 24 are inline.
	tagged := encodeHex(t, "d818", "5854", hex.EncodeToString(plain))
	sum := sha256.Sum256(tagged)
	if hex.EncodeToString(digest) != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %x, want %x", digest, sum)
	}
}

func encodeHex(t *testing.T, parts ...string) []byte {
	t.Helper()
	var out []byte
	for _, part := range parts {
		decoded, err := hex.DecodeString(part)
		if err != nil {
			t.Fatalf("bad hex %q: %v", part, err)
		}
		out = append(out, decoded...)
	}
	return out
}
