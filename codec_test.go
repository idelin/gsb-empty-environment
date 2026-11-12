package netsec

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

// --- Reference encoder -----------------------------------------------------

func appendSize(out []byte, n int) []byte {
	switch {
	case n < 254:
		return append(out, byte(n))
	case n <= 0xffff:
		out = append(out, 254)
		return binary.LittleEndian.AppendUint16(out, uint16(n))
	default:
		out = append(out, 255)
		return binary.LittleEndian.AppendUint32(out, uint32(n))
	}
}

func alignEncoder(out []byte) []byte {
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	return out
}

func encodeValue(out []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(out, typeNull)
	case bool:
		if x {
			return append(out, typeTrue)
		}
		return append(out, typeFalse)
	case int32:
		out = append(out, typeInt32)
		return binary.LittleEndian.AppendUint32(out, uint32(x))
	case int64:
		out = append(out, typeInt64)
		out = alignEncoder(out)
		return binary.LittleEndian.AppendUint64(out, uint64(x))
	case float64:
		out = append(out, typeFloat64)
		out = alignEncoder(out)
		return binary.LittleEndian.AppendUint64(out, math.Float64bits(x))
	case string:
		out = append(out, typeString)
		out = appendSize(out, len(x))
		return append(out, x...)
	case []byte:
		out = append(out, typeUint8List)
		out = appendSize(out, len(x))
		return append(out, x...)
	case []any:
		out = append(out, typeList)
		out = appendSize(out, len(x))
		for _, e := range x {
			out = encodeValue(out, e)
		}
		return out
	case *Map:
		out = append(out, typeMap)
		out = appendSize(out, x.Len())
		for i := 0; i < x.Len(); i++ {
			k, val := x.Pair(i)
			out = encodeValue(out, k)
			out = encodeValue(out, val)
		}
		return out
	default:
		panic("unsupported test value")
	}
}

func mustEncode(v any) []byte {
	return encodeValue(nil, v)
}

func assertError(t *testing.T, msg []byte, want error) {
	t.Helper()
	_, err := Decode(msg)
	if !errorsIs(err, want) {
		t.Fatalf("Decode error = %v, want %v", err, want)
	}
}

func errorsIs(err, target error) bool {
	return err == target
}

func deepEqual(a, b any) bool {
	if af, ok := a.(float64); ok {
		bf, isFloat := b.(float64)
		if !isFloat {
			return false
		}
		if math.IsNaN(af) {
			return math.IsNaN(bf)
		}
		return af == bf
	}
	return reflect.DeepEqual(a, b)
}

// nested builds depth single-element lists wrapping a true value.
func nested(depth int) []byte {
	msg := []byte{typeTrue}
	for i := 0; i < depth; i++ {
		msg = append([]byte{typeList, 1}, msg...)
	}
	return msg
}

// appendNestedContainers prefixes alternating container headers carrying one
// element each, used to build deep map/list chains.
func appendNestedContainers(msg []byte, count int, tags ...byte) []byte {
	for i := 0; i < count; i++ {
		tag := tags[i%len(tags)]
		if tag == typeMap {
			msg = append([]byte{typeMap, 1, typeNull}, msg...)
		} else {
			msg = append([]byte{typeList, 1}, msg...)
		}
	}
	return msg
}

// --- Tests -----------------------------------------------------------------

func TestPrimitivesRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"null", nil, nil},
		{"true", true, true},
		{"false", false, false},
		{"int32 negative", int32(-123456), int32(-123456)},
		{"int32 max", int32(math.MaxInt32), int32(math.MaxInt32)},
		{"int64", int64(1) << 40, int64(1) << 40},
		{"int64 negative", int64(-9000000000), int64(-9000000000)},
		{"float", 3.5, float64(3.5)},
		{"float nan", math.NaN(), math.NaN()},
		{"string", "hello \x00 世界", "hello \x00 世界"},
		{"empty string", "", ""},
		{"bytes", []byte{1, 2, 3, 255}, []byte{1, 2, 3, 255}},
		{"empty bytes", []byte{}, []byte{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(mustEncode(tc.in))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !deepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestListAndMapRoundTrip(t *testing.T) {
	m := &Map{}
	m.keys = []any{"a", int32(1), []byte{9}}
	m.values = []any{int64(2), []any{true, nil}, "x"}

	list := []any{
		m,
		[]any{int32(1), int64(2), float64(3)},
		"nested",
	}
	got, err := Decode(mustEncode(list))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !deepEqual(got, list) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, list)
	}

	decodedMap := got.([]any)[0].(*Map)
	if decodedMap.Len() != 3 {
		t.Fatalf("map len = %d", decodedMap.Len())
	}
	if v, ok := decodedMap.Get("a"); !ok || v != int64(2) {
		t.Fatalf("map Get(a) = %#v, %v", v, ok)
	}
	if v, ok := decodedMap.Get([]byte{9}); !ok || v != "x" {
		t.Fatalf("map Get(byte key) = %#v, %v", v, ok)
	}
	if _, ok := decodedMap.Get("missing"); ok {
		t.Fatal("missing key reported present")
	}
}

func TestLyingLengths(t *testing.T) {
	t.Run("uint8 list claims too many", func(t *testing.T) {
		// 300 declared bytes (under the element budget) but only 2 follow.
		msg := []byte{typeUint8List, 254, 0x2c, 0x01, 1, 2}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("string claims more bytes than present", func(t *testing.T) {
		msg := []byte{typeString, 10, 'a', 'b'}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("list claims more elements than present", func(t *testing.T) {
		msg := []byte{typeList, 5, typeTrue, typeFalse}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("map claims more pairs than present", func(t *testing.T) {
		msg := []byte{typeMap, 3, typeTrue, typeFalse}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("map value missing", func(t *testing.T) {
		msg := []byte{typeMap, 1, typeTrue}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("prefix uint16 truncated", func(t *testing.T) {
		msg := []byte{typeString, 254, 0x00}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("prefix uint32 truncated", func(t *testing.T) {
		msg := []byte{typeUint8List, 255, 0x00, 0x00}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("huge uint32 length on 32-bit guard", func(t *testing.T) {
		msg := []byte{typeUint8List, 255, 0xff, 0xff, 0xff, 0xff}
		assertError(t, msg, ErrTruncated)
	})
	t.Run("empty input", func(t *testing.T) {
		assertError(t, nil, ErrTruncated)
	})
}

func TestTruncationNeverPanics(t *testing.T) {
	full := mustEncode([]any{
		int64(7),
		float64(1.25),
		"abc",
		[]byte{1, 2},
		&Map{keys: []any{"k"}, values: []any{int32(1)}},
	})
	for i := 0; i < len(full); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix %d: %v", i, r)
				}
			}()
			if _, err := Decode(full[:i]); err == nil {
				t.Fatalf("prefix %d unexpectedly decoded", i)
			}
		}()
	}
}

func TestDepthLimit(t *testing.T) {
	if _, err := Decode(nested(16)); err != nil {
		t.Fatalf("depth 16 rejected: %v", err)
	}
	assertError(t, nested(17), ErrDepth)

	msg := []byte{typeTrue, typeFalse}
	msg = appendNestedContainers(msg, 17, typeMap, typeList)
	assertError(t, msg, ErrDepth)
}

func TestAlignmentPadding(t *testing.T) {
	t.Run("valid int64 padding", func(t *testing.T) {
		msg := []byte{typeInt64, 0, 0, 0, 0, 0, 0, 0}
		msg = binary.LittleEndian.AppendUint64(msg, 42)
		got, err := Decode(msg)
		if err != nil || got != int64(42) {
			t.Fatalf("got %#v, err %v", got, err)
		}
	})
	t.Run("non-zero int64 padding", func(t *testing.T) {
		msg := []byte{typeInt64, 0, 0, 0, 0, 0, 0, 1}
		msg = binary.LittleEndian.AppendUint64(msg, 42)
		assertError(t, msg, ErrPadding)
	})
	t.Run("non-zero float padding after string", func(t *testing.T) {
		// List header (2 bytes) plus string leave the cursor at 5; the float
		// tag moves it to 6, requiring 2 padding bytes to reach the boundary 8.
		msg := []byte{typeList, 2, typeString, 1, 'x', typeFloat64, 0, 9, 0, 0}
		msg = binary.LittleEndian.AppendUint64(msg, math.Float64bits(1))
		assertError(t, msg, ErrPadding)
	})
	t.Run("valid float padding after string", func(t *testing.T) {
		msg := []byte{typeList, 2, typeString, 1, 'x', typeFloat64, 0, 0}
		msg = binary.LittleEndian.AppendUint64(msg, math.Float64bits(1))
		if _, err := Decode(msg); err != nil {
			t.Fatalf("valid padding rejected: %v", err)
		}
	})
	t.Run("padding itself truncated", func(t *testing.T) {
		assertError(t, []byte{typeInt64, 0, 0}, ErrTruncated)
	})
}

func TestDuplicateKeys(t *testing.T) {
	t.Run("duplicate scalar key", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{"k", "k"}
		m.values = []any{int32(1), int32(2)}
		assertError(t, mustEncode(m), ErrDuplicateKey)
	})
	t.Run("duplicate same-width int keys", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{int32(5), int32(5)}
		m.values = []any{int32(1), int32(2)}
		assertError(t, mustEncode(m), ErrDuplicateKey)
	})
	t.Run("int keys of different widths stay distinct", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{int32(5), int64(5)}
		m.values = []any{int32(1), int32(2)}
		if _, err := Decode(mustEncode(m)); err != nil {
			t.Fatalf("typed keys should differ: %v", err)
		}
	})
	t.Run("duplicate nan keys", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{math.NaN(), math.NaN()}
		m.values = []any{int32(1), int32(2)}
		assertError(t, mustEncode(m), ErrDuplicateKey)
	})
	t.Run("duplicate byte slice keys", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{[]byte{1, 2}, []byte{1, 2}}
		m.values = []any{int32(1), int32(2)}
		assertError(t, mustEncode(m), ErrDuplicateKey)
	})
	t.Run("duplicate nested list keys", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{[]any{int32(1)}, []any{int32(1)}}
		m.values = []any{int32(1), int32(2)}
		assertError(t, mustEncode(m), ErrDuplicateKey)
	})
	t.Run("distinct keys accepted", func(t *testing.T) {
		m := &Map{}
		m.keys = []any{"a", int32(1), int64(1), []byte{1}, true, nil}
		m.values = []any{int32(1), int32(2), int32(3), int32(4), int32(5), int32(6)}
		if _, err := Decode(mustEncode(m)); err != nil {
			t.Fatalf("distinct keys rejected: %v", err)
		}
	})
}

func TestTrailingBytes(t *testing.T) {
	assertError(t, append(mustEncode(true), 0), ErrTrailing)
	msg := append(mustEncode(int32(1)), mustEncode(int32(2))...)
	assertError(t, msg, ErrTrailing)
}

func TestElementLimit(t *testing.T) {
	t.Run("exactly max elements accepted", func(t *testing.T) {
		msg := []byte{typeList, 255}
		entries := maxElements - 1 // list tag itself also counts as a value
		msg = binary.LittleEndian.AppendUint32(msg, uint32(entries))
		for i := 0; i < entries; i++ {
			msg = append(msg, typeTrue)
		}
		if _, err := Decode(msg); err != nil {
			t.Fatalf("limit boundary rejected: %v", err)
		}
	})
	t.Run("one over limit rejected", func(t *testing.T) {
		msg := []byte{typeList, 255}
		entries := maxElements // tag plus entries totals 100001
		msg = binary.LittleEndian.AppendUint32(msg, uint32(entries))
		for i := 0; i < entries; i++ {
			msg = append(msg, typeTrue)
		}
		assertError(t, msg, ErrTooMany)
	})
	t.Run("byte list counts its bytes", func(t *testing.T) {
		msg := []byte{typeUint8List, 255}
		msg = binary.LittleEndian.AppendUint32(msg, maxElements)
		msg = append(msg, 1, 2, 3)
		assertError(t, msg, ErrTooMany)
	})
	t.Run("cumulative across containers", func(t *testing.T) {
		// Two lists each holding half the budget; their totals still exceed it.
		msg := []byte{typeList, 2, typeList, 255}
		msg = binary.LittleEndian.AppendUint32(msg, maxElements/2)
		for i := 0; i < maxElements/2; i++ {
			msg = append(msg, typeTrue)
		}
		msg = append(msg, typeList, 255)
		msg = binary.LittleEndian.AppendUint32(msg, maxElements/2+1)
		for i := 0; i < maxElements/2+1; i++ {
			msg = append(msg, typeTrue)
		}
		assertError(t, msg, ErrTooMany)
	})
}

func TestUnknownTypeAndSizePrefixes(t *testing.T) {
	t.Run("unknown type", func(t *testing.T) {
		assertError(t, []byte{99}, ErrUnknownType)
	})
	t.Run("reserved tag six rejected", func(t *testing.T) {
		assertError(t, []byte{6}, ErrUnknownType)
	})
	t.Run("size prefix variants round trip", func(t *testing.T) {
		for _, n := range []int{0, 1, 253, 254, 255, 256, 65535, 65536, 99999} {
			data := make([]byte, n)
			got, err := Decode(mustEncode(data))
			if err != nil {
				t.Fatalf("n=%d: %v", n, err)
			}
			if len(got.([]byte)) != n {
				t.Fatalf("n=%d: got len %d", n, len(got.([]byte)))
			}
		}
	})
}

func TestNoInputAliasing(t *testing.T) {
	src := mustEncode([]byte{10, 20, 30})
	got, err := Decode(src)
	if err != nil {
		t.Fatal(err)
	}
	decoded := got.([]byte)
	decoded[0] = 99
	if src[len(src)-3] != 10 {
		t.Fatal("decoded bytes alias the input buffer")
	}
}

func TestMapAccessorsAndKeyEquivalence(t *testing.T) {
	m := &Map{}
	m.keys = []any{float64(1.5), int32(7), "s"}
	m.values = []any{int32(1), int32(2), int32(3)}

	got, err := Decode(mustEncode(m))
	if err != nil {
		t.Fatal(err)
	}
	dm := got.(*Map)

	keys := dm.Keys()
	values := dm.Values()
	if len(keys) != 3 || len(values) != 3 {
		t.Fatalf("accessor lengths = %d/%d", len(keys), len(values))
	}
	k, v := dm.Pair(1)
	if k != int32(7) || v != int32(2) {
		t.Fatalf("Pair(1) = %#v, %#v", k, v)
	}

	// Mutating the returned copies must not affect the underlying map.
	keys[0] = "tampered"
	values[0] = "tampered"
	if got2, ok := dm.Get(float64(1.5)); !ok || got2 != int32(1) {
		t.Fatalf("accessor copies alias internal storage: %#v %v", got2, ok)
	}

	// Float and integer keys never compare equal even with the same numeric
	// magnitude, exercising the mixed-type branch of keyEqual.
	if _, ok := dm.Get(int64(1)); ok {
		t.Fatal("float64 key matched an int64 lookup")
	}

	// Pair on an out-of-range index panics.
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Pair out of range did not panic")
			}
		}()
		_, _ = dm.Pair(99)
	}()
}
