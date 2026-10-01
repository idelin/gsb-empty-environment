package canon

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"testing"
)

func mustEncode(t *testing.T, v Value) []byte {
	t.Helper()
	enc, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return enc
}

func TestKeyInsertionOrderInvariance(t *testing.T) {
	a := Object(Pair{"x", Int(1)}, Pair{"y", Int(2)}, Pair{"z", Int(3)})
	b := Object(Pair{"z", Int(3)}, Pair{"x", Int(1)}, Pair{"y", Int(2)})
	c := ObjectFromMap(map[string]Value{"x": Int(1), "y": Int(2), "z": Int(3)})
	ea, eb, ec := mustEncode(t, a), mustEncode(t, b), mustEncode(t, c)
	if !bytes.Equal(ea, eb) || !bytes.Equal(ea, ec) {
		t.Fatalf("key insertion order changed encoding:\n%x\n%x\n%x", ea, eb, ec)
	}
}

func TestIntAndStringDiffer(t *testing.T) {
	ei := mustEncode(t, Int(1))
	es := mustEncode(t, String("1"))
	if bytes.Equal(ei, es) {
		t.Fatal("Int(1) and String(\"1\") must have distinct encodings")
	}
	// Also nested inside an object.
	oi := mustEncode(t, Object(Pair{"k", Int(1)}))
	os_ := mustEncode(t, Object(Pair{"k", String("1")}))
	if bytes.Equal(oi, os_) {
		t.Fatal("nested Int(1) and String(\"1\") must differ")
	}
	// Int and Float are distinct types too.
	if bytes.Equal(mustEncode(t, Int(1)), mustEncode(t, Float(1))) {
		t.Fatal("Int(1) and Float(1) must differ")
	}
}

func TestNonFiniteFloatRejected(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Encode(Float(f)); err == nil {
			t.Fatalf("Encode(Float(%v)) succeeded, want explicit rejection", f)
		}
	}
}

func TestNegativeZeroNormalized(t *testing.T) {
	a := mustEncode(t, Float(math.Copysign(0, -1)))
	b := mustEncode(t, Float(0))
	if !bytes.Equal(a, b) {
		t.Fatalf("-0.0 and +0.0 must share one canonical encoding: %x vs %x", a, b)
	}
}

func TestArrayOrderPreserved(t *testing.T) {
	a := mustEncode(t, Array(Int(1), Int(2)))
	b := mustEncode(t, Array(Int(2), Int(1)))
	if bytes.Equal(a, b) {
		t.Fatal("array order must be significant")
	}
}

func TestDuplicateKeysRejected(t *testing.T) {
	if _, err := Encode(Object(Pair{"k", Int(1)}, Pair{"k", Int(2)})); err == nil {
		t.Fatal("duplicate object keys must be rejected")
	}
}

func TestInvalidUTF8Rejected(t *testing.T) {
	if _, err := Encode(String("\xff\xfe")); err == nil {
		t.Fatal("invalid UTF-8 string must be rejected")
	}
	if _, err := Encode(Object(Pair{"\xff", Int(1)})); err == nil {
		t.Fatal("invalid UTF-8 object key must be rejected")
	}
}

func TestRoundTrip(t *testing.T) {
	a := &Node{Label: String("a")}
	b := &Node{Label: String("b")}
	a.Edges = []*Node{b, b} // shared reference
	b.Edges = []*Node{a}    // cycle
	v := Object(
		Pair{"graph", GraphValue(a)},
		Pair{"list", Array(Int(-3), Float(2.5), String("x"), Null(), Bool(true))},
		Pair{"nested", Object(Pair{"in", Array(Object(Pair{"k", String("v")}))})},
	)
	enc := mustEncode(t, v)
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	re := mustEncode(t, dec)
	if !bytes.Equal(enc, re) {
		t.Fatalf("round trip mismatch:\n%x\n%x", enc, re)
	}
}

func TestDecodeRejectsTruncation(t *testing.T) {
	v := Object(Pair{"key", Array(Int(1), String("abc"), Float(1.5))})
	enc := mustEncode(t, v)
	for i := 0; i < len(enc); i++ {
		if _, err := Decode(enc[:i]); err == nil {
			t.Fatalf("truncated input (%d/%d bytes) accepted", i, len(enc))
		}
	}
}

func TestDecodeRejectsHugeDeclaredLength(t *testing.T) {
	// String claiming 1<<40 bytes of payload.
	data := binary.AppendUvarint([]byte{tagString}, 1<<40)
	if _, err := Decode(data); err == nil {
		t.Fatal("huge declared string length must be rejected before allocation")
	}
	// Array claiming 1<<40 elements.
	data = binary.AppendUvarint([]byte{tagArray}, 1<<40)
	if _, err := Decode(data); err == nil {
		t.Fatal("huge declared array count must be rejected before allocation")
	}
	// Graph claiming 1<<40 nodes.
	data = binary.AppendUvarint([]byte{tagGraph}, 1<<40)
	if _, err := Decode(data); err == nil {
		t.Fatal("huge declared node count must be rejected before allocation")
	}
}

func TestDecodeDepthLimit(t *testing.T) {
	var data []byte
	for i := 0; i < maxDecodeDepth+10; i++ {
		data = binary.AppendUvarint(append(data, tagArray), 1)
	}
	data = append(data, tagNull)
	if _, err := Decode(data); err == nil {
		t.Fatal("excessively nested input must be rejected")
	}
}

func TestDecodeRejectsTrailingAndGarbage(t *testing.T) {
	if _, err := Decode([]byte{tagNull, tagNull}); err == nil {
		t.Fatal("trailing bytes must be rejected")
	}
	if _, err := Decode([]byte{0x00}); err == nil {
		t.Fatal("unknown tag must be rejected")
	}
	if _, err := Decode([]byte{tagBool, 0x02}); err == nil {
		t.Fatal("invalid bool byte must be rejected")
	}
	// Non-finite float on the wire.
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], math.Float64bits(math.NaN()))
	if _, err := Decode(append([]byte{tagFloat}, raw[:]...)); err == nil {
		t.Fatal("non-finite float on the wire must be rejected")
	}
}

func TestDigestMatchesEncoding(t *testing.T) {
	v := Object(Pair{"msg", String("hello")})
	d, err := Digest(v)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	want := sha256.Sum256(mustEncode(t, v))
	if d != want {
		t.Fatal("Digest must equal SHA-256 of the canonical encoding")
	}
}
