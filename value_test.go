package canon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func mustEncode(t *testing.T, v Value) []byte {
	t.Helper()
	b, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode(%v): %v", v, err)
	}
	return b
}

func TestKeyInsertionOrderIrrelevant(t *testing.T) {
	a, err := NewObject(KV{"x", Int(1)}, KV{"y", Int(2)}, KV{"z", Int(3)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewObject(KV{"z", Int(3)}, KV{"x", Int(1)}, KV{"y", Int(2)})
	if err != nil {
		t.Fatal(err)
	}
	if ea, eb := mustEncode(t, a), mustEncode(t, b); !bytes.Equal(ea, eb) {
		t.Fatalf("insertion order leaked into encoding:\n%x\n%x", ea, eb)
	}
}

func TestDuplicateKeyRejected(t *testing.T) {
	if _, err := NewObject(KV{"a", Int(1)}, KV{"a", Int(2)}); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestIntAndStringDiffer(t *testing.T) {
	ei := mustEncode(t, Int(1))
	es := mustEncode(t, String("1"))
	if bytes.Equal(ei, es) {
		t.Fatal("Int(1) and String(\"1\") must have different encodings")
	}
	// And every other type at the "same" logical value must differ too.
	vals := []Value{Int(1), String("1"), Bytes{1}, Float(1), Bool(true), Array{Int(1)}}
	seen := map[string]int{}
	for i, v := range vals {
		e := mustEncode(t, v)
		if j, dup := seen[string(e)]; dup {
			t.Fatalf("values %d and %d share an encoding", i, j)
		}
		seen[string(e)] = i
	}
}

func TestNonFiniteFloatRejected(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Encode(Float(f)); !errors.Is(err, ErrNonFiniteFloat) {
			t.Fatalf("Encode(Float(%v)) = %v, want ErrNonFiniteFloat", f, err)
		}
	}
}

func TestNegativeZeroNormalized(t *testing.T) {
	a := mustEncode(t, Float(math.Copysign(0, -1)))
	b := mustEncode(t, Float(0))
	if !bytes.Equal(a, b) {
		t.Fatalf("-0.0 and +0.0 must encode identically: %x vs %x", a, b)
	}
}

func TestArrayOrderPreserved(t *testing.T) {
	a := mustEncode(t, Array{Int(1), Int(2)})
	b := mustEncode(t, Array{Int(2), Int(1)})
	if bytes.Equal(a, b) {
		t.Fatal("array order must be significant")
	}
}

func TestRoundTrip(t *testing.T) {
	nested, err := NewObject(
		KV{"list", Array{Int(-1), Float(2.5), String("x"), Null{}, Bool(false)}},
		KV{"empty", Array{}},
		KV{"obj", Object{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	values := []Value{
		Null{}, Bool(true), Bool(false),
		Int(0), Int(1), Int(-1), Int(math.MaxInt64), Int(math.MinInt64),
		Float(0), Float(-0.0), Float(3.141592653589793), Float(-1e300),
		String(""), String("hello 世界"),
		Bytes{}, Bytes{0, 1, 255},
		Array{Int(1), Array{Int(2)}},
		nested,
	}
	for _, v := range values {
		enc := mustEncode(t, v)
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(%x): %v", enc, err)
		}
		re := mustEncode(t, dec)
		if !bytes.Equal(enc, re) {
			t.Fatalf("round trip mismatch for %v:\n%x\n%x", v, enc, re)
		}
	}
}

func TestDecodeRejectsOversizedLength(t *testing.T) {
	// String claiming 1<<40 payload bytes with an empty payload.
	data := append([]byte{tagString}, binary.AppendUvarint(nil, 1<<40)...)
	if _, err := Decode(data); err == nil {
		t.Fatal("oversized string length accepted")
	}
	// Array claiming 1<<40 elements.
	data = append([]byte{tagArray}, binary.AppendUvarint(nil, 1<<40)...)
	if _, err := Decode(data); err == nil {
		t.Fatal("oversized array count accepted")
	}
	// Object claiming 1<<40 entries.
	data = append([]byte{tagObject}, binary.AppendUvarint(nil, 1<<40)...)
	if _, err := Decode(data); err == nil {
		t.Fatal("oversized object count accepted")
	}
}

func TestDecodeRejectsTruncation(t *testing.T) {
	full := mustEncode(t, Array{String("hello"), Int(42), Bytes{1, 2, 3}})
	for i := 0; i < len(full); i++ {
		if _, err := Decode(full[:i]); err == nil {
			t.Fatalf("truncated input (%d of %d bytes) accepted", i, len(full))
		}
	}
	if _, err := Decode(append(full, 0x00)); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("trailing garbage: got %v", err)
	}
}

func TestDecodeRejectsNonCanonicalVarint(t *testing.T) {
	// Int(0) with a non-minimal two-byte varint.
	if _, err := Decode([]byte{tagInt, 0x80, 0x00}); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-minimal varint: got %v", err)
	}
	// -0.0 on the wire is non-canonical.
	negZero := append([]byte{tagFloat}, 0x80, 0, 0, 0, 0, 0, 0, 0)
	if _, err := Decode(negZero); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("-0.0: got %v", err)
	}
	// NaN on the wire.
	nan := append([]byte{tagFloat}, 0x7f, 0xf8, 0, 0, 0, 0, 0, 0)
	if _, err := Decode(nan); err == nil {
		t.Fatal("NaN accepted on decode")
	}
}

func TestDecodeRejectsUnsortedOrDuplicateKeys(t *testing.T) {
	obj := func(keys ...string) []byte {
		data := []byte{tagObject}
		data = binary.AppendUvarint(data, uint64(len(keys)))
		for _, k := range keys {
			data = append(data, tagString, byte(len(k)))
			data = append(data, k...)
			data = append(data, tagNull)
		}
		return data
	}
	if _, err := Decode(obj("b", "a")); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("unsorted keys: got %v", err)
	}
	if _, err := Decode(obj("a", "a")); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("duplicate keys: got %v", err)
	}
	if _, err := Decode(obj("a", "b")); err != nil {
		t.Fatalf("sorted keys rejected: %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	data := bytes.Repeat([]byte{tagArray, 0x01}, MaxDepth+1)
	data = append(data, tagNull)
	if _, err := Decode(data); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("deep nesting: got %v", err)
	}
}

func TestInvalidUTF8Rejected(t *testing.T) {
	if _, err := Encode(String("\xff\xfe")); err == nil {
		t.Fatal("invalid UTF-8 string encoded")
	}
	if _, err := Decode([]byte{tagString, 0x01, 0xff}); err == nil {
		t.Fatal("invalid UTF-8 string decoded")
	}
}

func TestDigestStable(t *testing.T) {
	a, _ := NewObject(KV{"k", Int(1)})
	b, _ := NewObject(KV{"k", Int(1)})
	da, err := Digest(a)
	if err != nil {
		t.Fatal(err)
	}
	db, err := Digest(b)
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatal("equal values must have equal digests")
	}
	if _, err := Digest(Float(math.NaN())); !errors.Is(err, ErrNonFiniteFloat) {
		t.Fatalf("Digest(NaN) = %v", err)
	}
}
