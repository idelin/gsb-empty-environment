package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func TestNormalPayload(t *testing.T) {
	payload := []byte{
		0xa7, // map(7)
		0x64, 'n', 'a', 'm', 'e', 0x67, 'g', 'a', 't', 'e', 'w', 'a', 'y',
		0x65, 'c', 'o', 'u', 'n', 't', 0x03,
		0x62, 'o', 'k', 0xf5,
		0x65, 'i', 't', 'e', 'm', 's', 0x83, 0x01, 0x02, 0x03,
		0x66, 'n', 'e', 's', 't', 'e', 'd', 0xa1,
		0x61, 'x', 0x20,
		0x64, 'b', 'l', 'o', 'b', 0x42, 0x01, 0x02,
		0x65, 'i', 'n', 'd', 'e', 'f', 0x7f,
		0x62, 'a', 'b', 0x62, 'c', 'd', 0xff,
	}

	got, err := Decode(payload)
	if err != nil {
		t.Fatalf("Decode normal payload: %v", err)
	}
	m, ok := got.(map[any]any)
	if !ok {
		t.Fatalf("got %T, want map[any]any", got)
	}
	if m["name"] != "gateway" {
		t.Fatalf("name = %#v", m["name"])
	}
	if m["count"] != uint64(3) {
		t.Fatalf("count = %#v", m["count"])
	}
	if m["ok"] != true {
		t.Fatalf("ok = %#v", m["ok"])
	}
	if m["indef"] != "abcd" {
		t.Fatalf("indef = %#v", m["indef"])
	}
	items, ok := m["items"].([]any)
	if !ok || len(items) != 3 || items[2] != uint64(3) {
		t.Fatalf("items = %#v", m["items"])
	}
	nested, ok := m["nested"].(map[any]any)
	if !ok || nested["x"] != int64(-1) {
		t.Fatalf("nested = %#v", m["nested"])
	}
	blob, ok := m["blob"].([]byte)
	if !ok || len(blob) != 2 || blob[0] != 1 || blob[1] != 2 {
		t.Fatalf("blob = %#v", m["blob"])
	}
}

func TestTagChainLimit(t *testing.T) {
	chain := func(depth int) []byte {
		out := make([]byte, 0, depth+1)
		for i := 0; i < depth; i++ {
			out = append(out, 0xc0) // tag 0
		}
		return append(out, 0x01)
	}

	limits := Limits{MaxTagDepth: 8}
	if _, err := Decode(chain(8), limits); err != nil {
		t.Fatalf("tag chain at limit should decode: %v", err)
	}
	_, err := Decode(chain(9), limits)
	if !errors.Is(err, ErrTagDepthExceeded) {
		t.Fatalf("tag chain over limit: got %v, want ErrTagDepthExceeded", err)
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("tag chain error should also match ErrLimitExceeded: %v", err)
	}
}

func TestTag24Recursion(t *testing.T) {
	wrap := func(inner []byte) []byte {
		out := []byte{0xd8, 0x18} // tag 24
		switch {
		case len(inner) < 24:
			out = append(out, byte(0x40+len(inner)))
		case len(inner) <= 0xff:
			out = append(out, 0x58, byte(len(inner)))
		default:
			out = append(out, 0x59, byte(len(inner)>>8), byte(len(inner)))
		}
		return append(out, inner...)
	}

	inner := []byte{0xa1, 0x61, 'x', 0x01}
	payload := wrap(inner)
	got, err := Decode(payload)
	if err != nil {
		t.Fatalf("single tag 24: %v", err)
	}
	tag, ok := got.(Tag)
	if !ok || tag.Number != 24 {
		t.Fatalf("got %#v, want Tag{24, ...}", got)
	}
	innerMap, ok := tag.Value.(map[any]any)
	if !ok || innerMap["x"] != uint64(1) {
		t.Fatalf("tag 24 value = %#v", tag.Value)
	}

	for i := 0; i < 3; i++ {
		payload = wrap(payload)
	}
	if _, err := Decode(payload, Limits{MaxTag24Depth: 4}); err != nil {
		t.Fatalf("tag 24 at depth limit should decode: %v", err)
	}
	payload = wrap(payload)
	_, err = Decode(payload, Limits{MaxTag24Depth: 4})
	if !errors.Is(err, ErrTag24DepthExceeded) {
		t.Fatalf("tag 24 over limit: got %v, want ErrTag24DepthExceeded", err)
	}

	bad := wrap(append(inner, 0x01))
	if _, err := Decode(bad); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("tag 24 trailing embedded bytes: got %v, want ErrTrailingData", err)
	}
}

func TestInflatedArrayLengthRejectedBeforeAllocation(t *testing.T) {
	payload := []byte{0x9b} // array with 64-bit length
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], 1<<40)
	payload = append(payload, encoded[:]...)

	_, err := Decode(payload)
	if !errors.Is(err, ErrDeclaredLengthExceeded) {
		t.Fatalf("inflated array: got %v, want ErrDeclaredLengthExceeded", err)
	}

	// A map with an inflated entry count is covered by the same length check.
	mapPayload := []byte{0xbb}
	binary.BigEndian.PutUint64(encoded[:], 1<<40)
	mapPayload = append(mapPayload, encoded[:]...)
	_, err = Decode(mapPayload)
	if !errors.Is(err, ErrDeclaredLengthExceeded) {
		t.Fatalf("inflated map: got %v, want ErrDeclaredLengthExceeded", err)
	}
}

func TestDeclaredLengthRatioIsCallerControlled(t *testing.T) {
	payload := []byte{0x82, 0x00, 0x00} // two elements, two bytes remain
	if _, err := Decode(payload); err != nil {
		t.Fatalf("ratio 1 should accept a valid array: %v", err)
	}
	_, err := Decode(payload, Limits{MaxDeclaredLengthRatio: 0.5})
	if !errors.Is(err, ErrDeclaredLengthExceeded) {
		t.Fatalf("stricter ratio: got %v, want ErrDeclaredLengthExceeded", err)
	}
}

func TestIndefiniteStringChunkBudget(t *testing.T) {
	chunks := func(count int) []byte {
		out := []byte{0x7f} // indefinite text string
		for i := 0; i < count; i++ {
			out = append(out, 0x61, byte('a'+i%26))
		}
		return append(out, 0xff)
	}

	if _, err := Decode(chunks(3), Limits{MaxIndefiniteStringChunks: 3}); err != nil {
		t.Fatalf("chunk count at limit should decode: %v", err)
	}
	_, err := Decode(chunks(4), Limits{MaxIndefiniteStringChunks: 3})
	if !errors.Is(err, ErrIndefiniteStringChunksExceeded) {
		t.Fatalf("chunk count over limit: got %v", err)
	}

	// The budget is shared by every indefinite string in the request.
	twoStrings := []byte{0x82}
	twoStrings = append(twoStrings, chunks(3)...)
	twoStrings = append(twoStrings, chunks(3)...)
	_, err = Decode(twoStrings, Limits{MaxIndefiniteStringChunks: 5})
	if !errors.Is(err, ErrIndefiniteStringChunksExceeded) {
		t.Fatalf("shared chunk budget: got %v", err)
	}
}

func TestTag24SharesChunkBudget(t *testing.T) {
	inner := []byte{0x7f}
	for i := 0; i < 4; i++ {
		inner = append(inner, 0x61, 'a')
	}
	inner = append(inner, 0xff)

	// The outer tag 24 byte string itself is indefinite (one chunk); the
	// embedded CBOR text string adds four more chunks.
	payload := []byte{0xd8, 0x18, 0x5f, 0x58, byte(len(inner))}
	payload = append(payload, inner...)
	payload = append(payload, 0xff)

	if _, err := Decode(payload, Limits{MaxIndefiniteStringChunks: 5}); err != nil {
		t.Fatalf("shared chunk count at limit should decode: %v", err)
	}
	_, err := Decode(payload, Limits{MaxIndefiniteStringChunks: 4})
	if !errors.Is(err, ErrIndefiniteStringChunksExceeded) {
		t.Fatalf("tag 24 should share chunk budget: got %v", err)
	}
}

func TestIndefiniteContainers(t *testing.T) {
	got, err := Decode([]byte{0x9f, 0x01, 0x02, 0xff})
	if err != nil {
		t.Fatalf("indefinite array: %v", err)
	}
	values, ok := got.([]any)
	if !ok || len(values) != 2 || values[1] != uint64(2) {
		t.Fatalf("indefinite array = %#v", got)
	}

	got, err = Decode([]byte{0xbf, 0x61, 'a', 0x01, 0xff})
	if err != nil {
		t.Fatalf("indefinite map: %v", err)
	}
	m, ok := got.(map[any]any)
	if !ok || m["a"] != uint64(1) {
		t.Fatalf("indefinite map = %#v", got)
	}
}

func TestDuplicateMapKeysRejected(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name:    "same encoding",
			payload: []byte{0xa2, 0x61, 'a', 0x01, 0x61, 'a', 0x02},
		},
		{
			name:    "different integer encodings",
			payload: []byte{0xa2, 0x01, 0x02, 0x18, 0x01, 0x03},
		},
		{
			name: "different string encodings",
			payload: []byte{
				0xa2,
				0x61, 'a', 0x01,
				0x7f, 0x61, 'a', 0xff, 0x02,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(test.payload)
			if !errors.Is(err, ErrDuplicateMapKey) {
				t.Fatalf("got %v, want ErrDuplicateMapKey", err)
			}
		})
	}

	// Text and byte strings are different key types even when the bytes match.
	distinct := []byte{0xa2, 0x41, 'a', 0x01, 0x61, 'a', 0x02}
	if _, err := Decode(distinct); err != nil {
		t.Fatalf("distinct key types should decode: %v", err)
	}
}

func TestBigNumByteLimit(t *testing.T) {
	large := append([]byte{0xc2, 0x50}, bytes.Repeat([]byte{0x01}, 16)...)
	_, err := Decode(large, Limits{MaxBigNumBytes: 8})
	if !errors.Is(err, ErrBigNumTooLarge) {
		t.Fatalf("large bignum: got %v, want ErrBigNumTooLarge", err)
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("bignum error should also match ErrLimitExceeded: %v", err)
	}

	positive := []byte{0xc2, 0x42, 0x01, 0x02}
	got, err := Decode(positive, Limits{MaxBigNumBytes: 2})
	if err != nil {
		t.Fatalf("small bignum: %v", err)
	}
	number, ok := got.(*big.Int)
	if !ok || number.String() != "258" {
		t.Fatalf("bignum = %#v", got)
	}

	negative := []byte{0xc3, 0x42, 0x01, 0x02}
	got, err = Decode(negative, Limits{MaxBigNumBytes: 2})
	if err != nil {
		t.Fatalf("small negative bignum: %v", err)
	}
	number, ok = got.(*big.Int)
	if !ok || number.String() != "-259" {
		t.Fatalf("negative bignum = %#v", got)
	}

	chunked := []byte{0xc2, 0x5f, 0x42, 0x01, 0x02, 0x41, 0x03, 0xff}
	got, err = Decode(chunked, Limits{MaxBigNumBytes: 3, MaxIndefiniteStringChunks: 2})
	if err != nil {
		t.Fatalf("chunked bignum: %v", err)
	}
	number, ok = got.(*big.Int)
	if !ok || number.String() != "66051" {
		t.Fatalf("chunked bignum = %#v", got)
	}

	tooManyChunks := []byte{0xc2, 0x5f, 0x42, 0x01, 0x02, 0x42, 0x03, 0x04, 0xff}
	_, err = Decode(tooManyChunks, Limits{MaxBigNumBytes: 3, MaxIndefiniteStringChunks: 1})
	if !errors.Is(err, ErrIndefiniteStringChunksExceeded) {
		t.Fatalf("chunked bignum over chunk budget: got %v", err)
	}

	notBytes := []byte{0xc2, 0x61, 'a'}
	if _, err := Decode(notBytes); !errors.Is(err, ErrInvalidCBOR) {
		t.Fatalf("bignum text content: got %v, want ErrInvalidCBOR", err)
	}
}

func TestNestingAndTrailingLimits(t *testing.T) {
	nested := []byte(strings.Repeat("\x81", 12)) // arrays
	nested = append(nested, 0x00)
	_, err := Decode(nested, Limits{MaxDepth: 8})
	if !errors.Is(err, ErrNestingTooDeep) {
		t.Fatalf("nesting: got %v, want ErrNestingTooDeep", err)
	}

	if _, err := Decode([]byte{0x01, 0x02}); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("trailing data: got %v", err)
	}
	if _, err := Decode([]byte{0xff}); !errors.Is(err, ErrInvalidCBOR) {
		t.Fatalf("stray break: got %v", err)
	}
}

func TestDecodeReaderInputBudget(t *testing.T) {
	if _, err := DecodeReader(bytes.NewReader([]byte{0x01}), Limits{MaxInputBytes: 4}); err != nil {
		t.Fatalf("small input: %v", err)
	}
	_, err := DecodeReader(bytes.NewReader([]byte{0x01, 0x02}), Limits{MaxInputBytes: 1})
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("input over budget: got %v, want ErrInputTooLarge", err)
	}
}

func TestDecodeRandomInputDoesNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		data := make([]byte, rng.Intn(64))
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(data); err != nil {
			// Errors are expected; a panic would fail the test.
			continue
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x01})
	f.Add([]byte{0xa1, 0x61, 'a', 0x01})
	f.Add([]byte{0x9b, 0, 0, 0, 1, 0, 0, 0, 0})
	f.Add([]byte{0x7f, 0x60, 0xff})
	f.Add([]byte{0xd8, 0x18, 0x40})
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := Decode(data); err != nil {
			return
		}
	})
}
