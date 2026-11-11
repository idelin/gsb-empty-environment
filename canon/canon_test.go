package canon

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return raw
}

func TestKnownAnswerVectors(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"zero", int64(0), "00"},
		{"one", int64(1), "01"},
		{"ten", int64(10), "0a"},
		{"int23", int64(23), "17"},
		{"int24", int64(24), "1818"},
		{"int25", int64(25), "1819"},
		{"int100", int64(100), "1864"},
		{"uint8max", int64(255), "18ff"},
		{"uint16edge", int64(256), "190100"},
		{"uint16max", int64(65535), "19ffff"},
		{"uint32edge", int64(65536), "1a00010000"},
		{"uint32max", int64(4294967295), "1affffffff"},
		{"uint64edge", uint64(4294967296), "1b0000000100000000"},
		{"uint64max", uint64(18446744073709551615), "1bffffffffffffffff"},
		{"negOne", int64(-1), "20"},
		{"negTen", int64(-10), "29"},
		{"neg100", int64(-100), "3863"},
		{"int64min", int64(-9223372036854775808), "3b7fffffffffffffff"},
		{"emptyBytes", []byte{}, "40"},
		{"emptyText", "", "60"},
		{"text", "IETF", "6449455446"},
		{"bytes", mustHex(t, "01020304"), "4401020304"},
		{"emptyArray", []any{}, "80"},
		{"array", []any{int64(1), []any{int64(2), int64(3)}, []any{int64(4), int64(5)}},
			"8301820203820405"},
		{"emptyMap", NewMap(), "a0"},
		{"mapRFC", NewMap(
			MapEntry{Key: int64(1), Value: int64(2)},
			MapEntry{Key: int64(3), Value: int64(4)},
		), "a201020304"},
		{"mapOrdering", NewMap(
			MapEntry{Key: "long-text-key", Value: int64(1)},
			MapEntry{Key: "a", Value: int64(2)},
			MapEntry{Key: int64(7), Value: int64(3)},
		), "a307036161026d6c6f6e672d746578742d6b657901"},
		{"byteAndTextKey", NewMap(
			MapEntry{Key: "z", Value: int64(1)},
			MapEntry{Key: []byte{0x01}, Value: int64(2)},
		), "a2410102617a01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Encode(tt.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if hex.EncodeToString(got) != tt.want {
				t.Fatalf("Encode = %x, want %s", got, tt.want)
			}
			decoded, err := Decode(mustHex(t, tt.want))
			if err != nil {
				t.Fatalf("Decode(%s): %v", tt.want, err)
			}
			again, err := Encode(decoded)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			if hex.EncodeToString(again) != tt.want {
				t.Fatalf("round trip = %x, want %s", again, tt.want)
			}
		})
	}
}

func TestIndependentEncoderAgrees(t *testing.T) {
	values := []any{
		int64(0), int64(24), int64(-1), int64(-9223372036854775808),
		uint64(18446744073709551615),
		"hello", "café", strings.Repeat("x", 300),
		[]byte{0, 1, 2, 255},
		[]any{},
		[]any{int64(1), "two", []byte{3}, []any{int64(4)}},
		NewMap(),
		NewMap(
			MapEntry{Key: int64(-5), Value: "neg"},
			MapEntry{Key: "zzz", Value: []byte{9, 9}},
			MapEntry{Key: []byte{1, 2, 3}, Value: []any{int64(1), int64(2)}},
			MapEntry{Key: "a", Value: NewMap(
				MapEntry{Key: int64(100), Value: int64(200)},
			)},
		),
	}

	for i, v := range values {
		prod, err := Encode(v)
		if err != nil {
			t.Fatalf("case %d: production Encode: %v", i, err)
		}
		indep, err := indepEncode(v)
		if err != nil {
			t.Fatalf("case %d: independent Encode: %v", i, err)
		}
		if !bytesEqual(prod, indep) {
			t.Fatalf("case %d: production %x != independent %x", i, prod, indep)
		}

		decoded, err := Decode(prod)
		if err != nil {
			t.Fatalf("case %d: Decode: %v", i, err)
		}
		roundTrip, err := indepEncode(decoded)
		if err != nil {
			t.Fatalf("case %d: independent re-encode: %v", i, err)
		}
		if !bytesEqual(roundTrip, prod) {
			t.Fatalf("case %d: round trip %x != original %x", i, roundTrip, prod)
		}
	}
}

func TestGoMapOrderIsStable(t *testing.T) {
	in := map[string]any{
		"charlie": int64(3),
		"alpha":   int64(1),
		"bravo":   int64(2),
	}
	first, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := Encode(in)
		if err != nil {
			t.Fatal(err)
		}
		if !bytesEqual(first, next) {
			t.Fatalf("unstable map encoding: %x vs %x", first, next)
		}
	}
	want := "a365616c7068610165627261766f0267636861726c696503"
	if hex.EncodeToString(first) != want {
		t.Fatalf("got %x, want %s", first, want)
	}
}

func TestRejectNonMinimal(t *testing.T) {
	// Each input is a valid CBOR value encoded with a longer head than needed.
	inputs := []string{
		"1800",               // 0 in a one-byte uint head
		"180a",               // 10 in a one-byte uint head
		"190001",             // 1 in a two-byte uint head
		"1900ff",             // 255 in a two-byte uint head
		"1a00000001",         // 1 in a four-byte uint head
		"1b0000000000000001", // 1 in an eight-byte uint head
		"3817",               // -24 encoded with an extended head (arg 23 has a shorter form)
		"580141",             // 1-byte byte string using 1-byte length head
		"780161",             // 1-byte text string using 1-byte length head
		"980101",             // 1-element array using 1-byte length head
		"b8010101",           // 1-entry map using 1-byte length head
	}
	for _, in := range inputs {
		raw := mustHex(t, in)
		if _, err := Decode(raw); !errors.Is(err, ErrNonCanonical) {
			t.Fatalf("Decode(%s) err = %v, want ErrNonCanonical", in, err)
		}
	}
}

func TestRejectOutOfOrderAndDuplicateKeys(t *testing.T) {
	// Canonical order is int 1 (01, 1 byte) then "a" (6161, 2 bytes).
	good := mustHex(t, "a20102616103")
	if _, err := Decode(good); err != nil {
		t.Fatalf("good map rejected: %v", err)
	}
	badOrder := mustHex(t, "a26161030102")
	if _, err := Decode(badOrder); !errors.Is(err, ErrKeyOrder) {
		t.Fatalf("out-of-order map err = %v, want ErrKeyOrder", err)
	}

	// Duplicate int keys 1 and 1.
	dup := mustHex(t, "a201020103")
	if _, err := Decode(dup); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate map err = %v, want ErrDuplicateKey", err)
	}

	// Duplicate encoded keys rejected through the encoder as well.
	_, err := Encode(NewMap(
		MapEntry{Key: "k", Value: int64(1)},
		MapEntry{Key: "k", Value: int64(2)},
	))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Encode duplicate err = %v, want ErrDuplicateKey", err)
	}
}

func TestRejectIndefiniteAndUnsupported(t *testing.T) {
	inputs := map[string]error{
		"9f0102ff":           ErrIndefinite,       // indefinite array
		"bf616101ff":         ErrIndefinite,       // indefinite map
		"7f6161ff":           ErrIndefinite,       // indefinite text
		"5f4101ff":           ErrIndefinite,       // indefinite bytes
		"ff":                 ErrIndefinite,       // standalone break
		"f5":                 ErrUnsupportedValue, // true
		"f4":                 ErrUnsupportedValue, // false
		"f6":                 ErrUnsupportedValue, // null
		"f93c00":             ErrUnsupportedValue, // half-float 1.0
		"fa3f800000":         ErrUnsupportedValue, // float32 1.0
		"fb3ff0000000000000": ErrUnsupportedValue, // float64 1.0
		"c000":               ErrUnsupportedValue, // tag 0
		"1c00":               ErrReserved,         // reserved AI 28
		"1d00":               ErrReserved,         // reserved AI 29
		"1e00":               ErrReserved,         // reserved AI 30
	}
	for in, want := range inputs {
		if _, err := Decode(mustHex(t, in)); !errors.Is(err, want) {
			t.Fatalf("Decode(%s) err = %v, want %v", in, err, want)
		}
	}
}

func TestRejectMalformedInput(t *testing.T) {
	inputs := map[string]error{
		"":       ErrEmpty,
		"0102":   ErrTrailingData,
		"01ff":   ErrTrailingData,
		"41":     ErrTruncated,   // byte string claims one byte
		"61":     ErrTruncated,   // text claims one byte
		"8201":   ErrTruncated,   // array claims two items
		"a20102": ErrTruncated,   // map claims two pairs
		"61ff":   ErrInvalidUTF8, // lone continuation byte
		"62c328": ErrInvalidUTF8, // invalid two-byte sequence with minimal length head
	}
	for in, want := range inputs {
		if _, err := Decode(mustHex(t, in)); !errors.Is(err, want) {
			t.Fatalf("Decode(%q) err = %v, want %v", in, err, want)
		}
	}
}

func TestResourceBudgets(t *testing.T) {
	encoded, err := Encode([]any{int64(1), int64(2), int64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWithLimits(encoded, Limits{MaxItems: 2}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("MaxItems err = %v", err)
	}

	deep, err := Encode([]any{[]any{[]any{int64(1)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWithLimits(deep, Limits{MaxDepth: 1}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("MaxDepth err = %v", err)
	}

	text, err := Encode(strings.Repeat("a", 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWithLimits(text, Limits{MaxStringLen: 5}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("MaxStringLen err = %v", err)
	}

	big, err := Encode([]any{int64(1), int64(2), int64(3), int64(4)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWithLimits(big, Limits{MaxContainerLen: 2}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("MaxContainerLen err = %v", err)
	}

	raw, err := Encode([]byte{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWithLimits(raw, Limits{MaxBytes: 1}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("MaxBytes err = %v", err)
	}

	// Default limits accept normal messages.
	if _, err := Decode(encoded); err != nil {
		t.Fatalf("default limits rejected valid message: %v", err)
	}
}

func TestUnsupportedGoValues(t *testing.T) {
	unsupported := []any{nil, true, 3.14, float32(1), struct{ X int }{}}
	for _, v := range unsupported {
		if _, err := Encode(v); err == nil {
			t.Fatalf("Encode(%T) unexpectedly succeeded", v)
		}
	}

	if _, err := Encode(map[bool]any{true: int64(1)}); err == nil {
		t.Fatal("Encode with bool key unexpectedly succeeded")
	}

	if _, err := Encode("bad\xffutf8"); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8 err = %v, want ErrInvalidUTF8", err)
	}
}

func TestUniqueEncodingProperty(t *testing.T) {
	// For a representative message, re-encoding the decoded object must always
	// reproduce the accepted bytes; there is no second acceptable encoding.
	message := NewMap(
		MapEntry{Key: "id", Value: int64(42)},
		MapEntry{Key: "payload", Value: []byte{0xde, 0xad, 0xbe, 0xef}},
		MapEntry{Key: "nested", Value: []any{
			int64(-7), "text", NewMap(MapEntry{Key: int64(1), Value: "one"}),
		}},
	)
	canonical, err := Encode(message)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := Decode(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Encode(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if !bytesEqual(again, canonical) {
			t.Fatalf("encoding not unique on iteration %d", i)
		}
	}

	// Appending any extra byte must invalidate the message.
	tampered := append(append([]byte{}, canonical...), 0x00)
	if _, err := Decode(tampered); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("tampered message err = %v, want ErrTrailingData", err)
	}
}

func TestLargeUnsignedIntegersRoundTrip(t *testing.T) {
	values := []uint64{
		uint64(mathMaxInt64) + 1,
		1<<64 - 1,
	}
	for _, u := range values {
		raw, err := Encode(u)
		if err != nil {
			t.Fatalf("Encode(%d): %v", u, err)
		}
		decoded, err := Decode(raw)
		if err != nil {
			t.Fatalf("Decode(%x): %v", raw, err)
		}
		got, ok := decoded.(uint64)
		if !ok || got != u {
			t.Fatalf("round trip = %v (%T), want %d", decoded, decoded, u)
		}

		// Large unsigned integers are legal map keys too.
		m := NewMap(MapEntry{Key: u, Value: "big"})
		mraw, err := Encode(m)
		if err != nil {
			t.Fatalf("Encode map with uint64 key: %v", err)
		}
		mdec, err := Decode(mraw)
		if err != nil {
			t.Fatalf("Decode map with uint64 key: %v", err)
		}
		m2 := mdec.(Map)
		if key := m2.Entry(0).Key; key != u {
			t.Fatalf("map key = %v (%T), want %d", key, key, u)
		}
	}
}

const mathMaxInt64 = int64(1<<63 - 1)
