package wireint

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"math/rand"
	"testing"
)

func encodeBytes(t *testing.T, v *big.Int, version int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, v, version); err != nil {
		t.Fatalf("Encode(%v, v%d): %v", v, version, err)
	}
	return buf.Bytes()
}

// TestExhaustiveSmallRange round-trips every value in [-70000, 70000],
// which exhausts int8/uint8/int16/uint16 and the small range of every
// wider kind, and checks that each value has exactly one canonical encoding.
func TestExhaustiveSmallRange(t *testing.T) {
	for x := -70000; x <= 70000; x++ {
		v := big.NewInt(int64(x))
		b1 := encodeBytes(t, v, Version2)
		got, err := Decode(bytes.NewReader(b1), MaxVersion)
		if err != nil {
			t.Fatalf("Decode(Encode(%d)): %v", x, err)
		}
		if got.Cmp(v) != 0 {
			t.Fatalf("round-trip %d: got %v", x, got)
		}
		// Canonical uniqueness: re-encoding the decoded value must
		// reproduce the identical byte string.
		b2 := encodeBytes(t, got, Version2)
		if !bytes.Equal(b1, b2) {
			t.Fatalf("value %d has two encodings: %x vs %x", x, b1, b2)
		}
	}
}

// TestExhaustive8BitFields covers the complete range of both 8-bit fields.
func TestExhaustive8BitFields(t *testing.T) {
	for x := 0; x <= 255; x++ {
		var buf bytes.Buffer
		v := big.NewInt(int64(x))
		if err := EncodeField(&buf, Uint8, v, Version1); err != nil {
			t.Fatalf("EncodeField u8 %d: %v", x, err)
		}
		got, err := DecodeField(bytes.NewReader(buf.Bytes()), Uint8, MaxVersion)
		if err != nil || got.Int.Cmp(v) != 0 {
			t.Fatalf("u8 round-trip %d: got %v err %v", x, got.Int, err)
		}
	}
	for x := -128; x <= 127; x++ {
		var buf bytes.Buffer
		v := big.NewInt(int64(x))
		if err := EncodeField(&buf, Int8, v, Version1); err != nil {
			t.Fatalf("EncodeField i8 %d: %v", x, err)
		}
		got, err := DecodeField(bytes.NewReader(buf.Bytes()), Int8, MaxVersion)
		if err != nil || got.Int.Cmp(v) != 0 {
			t.Fatalf("i8 round-trip %d: got %v err %v", x, got.Int, err)
		}
	}
}

// TestExtremes round-trips the boundary values of every supported width.
func TestExtremes(t *testing.T) {
	pow := func(n uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), n) }
	cases := []*big.Int{
		big.NewInt(0), big.NewInt(1), big.NewInt(127), big.NewInt(128),
		big.NewInt(255), big.NewInt(256), big.NewInt(65535), big.NewInt(65536),
		new(big.Int).Sub(pow(31), big.NewInt(1)), pow(31),
		new(big.Int).Sub(pow(32), big.NewInt(1)), pow(32),
		new(big.Int).Sub(pow(63), big.NewInt(1)), pow(63),
		new(big.Int).Sub(pow(64), big.NewInt(1)), // max uint64
		pow(64),                                  // first bigint-only positive
		pow(100),
		big.NewInt(-1), big.NewInt(-128), big.NewInt(-129),
		big.NewInt(-32768), big.NewInt(-32769),
		new(big.Int).Neg(pow(31)), new(big.Int).Neg(pow(31)).Sub(new(big.Int).Neg(pow(31)), big.NewInt(1)),
		new(big.Int).Neg(pow(63)),                                  // min int64
		new(big.Int).Sub(new(big.Int).Neg(pow(63)), big.NewInt(1)), // first bigint-only negative
		new(big.Int).Neg(pow(200)),
	}
	for _, v := range cases {
		b := encodeBytes(t, v, Version2)
		got, err := Decode(bytes.NewReader(b), MaxVersion)
		if err != nil {
			t.Fatalf("Decode(Encode(%v)): %v", v, err)
		}
		if got.Cmp(v) != 0 {
			t.Fatalf("extreme round-trip %v: got %v", v, got)
		}
	}
}

// TestMaxUint64RoundTrip checks the largest 64-bit unsigned value exactly.
func TestMaxUint64RoundTrip(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))
	var buf bytes.Buffer
	if err := EncodeField(&buf, Uint64, max, Version1); err != nil {
		t.Fatalf("EncodeField: %v", err)
	}
	if buf.Len() != 9 { // tag + 8 bytes
		t.Fatalf("max uint64 encoding = %d bytes, want 9", buf.Len())
	}
	got, err := DecodeField(bytes.NewReader(buf.Bytes()), Uint64, MaxVersion)
	if err != nil {
		t.Fatalf("DecodeField: %v", err)
	}
	if got.Int.Cmp(max) != 0 || got.Int.String() != "18446744073709551615" {
		t.Fatalf("max uint64 round-trip: got %v", got.Int)
	}
}

// TestNegativeIntoUnsignedRejected: negatives must never enter unsigned fields.
func TestNegativeIntoUnsignedRejected(t *testing.T) {
	for _, f := range []Field{Uint8, Uint16, Uint32, Uint64} {
		var buf bytes.Buffer
		if err := EncodeField(&buf, f, big.NewInt(-1), Version2); !errors.Is(err, ErrRange) {
			t.Fatalf("EncodeField(%s, -1) = %v, want ErrRange", f.Kind, err)
		}
		wire := encodeBytes(t, big.NewInt(-1), Version2) // canonical int8 form
		if _, err := DecodeField(bytes.NewReader(wire), f, MaxVersion); !errors.Is(err, ErrRange) {
			t.Fatalf("DecodeField(%s, -1) = %v, want ErrRange", f.Kind, err)
		}
	}
}

// TestNonCanonicalRejected builds non-canonical encodings by hand and
// requires rejection, including non-canonical sign extension.
func TestNonCanonicalRejected(t *testing.T) {
	tag := func(version int, k Kind) byte { return 0x80 | byte(version)<<4 | byte(k) }
	cases := map[string][]byte{
		"inline value as uint8":      {tag(2, KindUint8), 5},
		"inline value as int8":       {tag(2, KindInt8), 0x7F},
		"uint8 value as uint16":      {tag(2, KindUint16), 0x00, 0xC8},
		"uint16 value as uint32":     {tag(2, KindUint32), 0x00, 0x00, 0xFF, 0xFF},
		"uint32 value as uint64":     {tag(2, KindUint64), 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF},
		"positive in signed kind":    {tag(2, KindInt16), 0x01, 0x2C}, // 300 as i16
		"sign extension -1 as int16": {tag(2, KindInt16), 0xFF, 0xFF},
		"sign extension -1 as int32": {tag(2, KindInt32), 0xFF, 0xFF, 0xFF, 0xFF},
		"sign extension -1 as int64": {tag(2, KindInt64), 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		"sign extension -300 as i32": {tag(2, KindInt32), 0xFF, 0xFF, 0xFE, 0xD4},
		"bigint for small value":     {tag(2, KindBig), 0x02, 0x00, 0x03, 0xE8},             // 1000
		"bigint for uint64 range":    {tag(2, KindBig), 0x08, 0x00, 1, 2, 3, 4, 5, 6, 7, 8}, // fits u64
		"bigint leading zero":        {tag(2, KindBig), 0x02, 0x00, 0x00, 0x01},
		"bigint bad sign byte":       {tag(2, KindBig), 0x09, 0x02, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		"bigint negative zero":       {tag(2, KindBig), 0x01, 0x01, 0x00},
		"bigint zero length":         {tag(2, KindBig), 0x00},
		"bigint under version 1":     {tag(1, KindBig), 0x09, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		"version 0 tag":              {0x80 | byte(KindUint8), 0xFF},
		"version 3 tag":              {0x80 | 3<<4 | byte(KindUint8), 0xFF},
	}
	for name, b := range cases {
		if _, err := Decode(bytes.NewReader(b), MaxVersion); err == nil {
			t.Fatalf("%s: accepted non-canonical/invalid encoding %x", name, b)
		}
	}
}

// TestNonCanonicalDoesNotConsumeNextField: after a non-canonical field is
// rejected, the stream must be positioned exactly at the next field.
func TestNonCanonicalDoesNotConsumeNextField(t *testing.T) {
	var buf bytes.Buffer
	// Field 1: -1 sign-extended into int64 (non-canonical), 9 bytes total.
	buf.WriteByte(0x80 | byte(Version2)<<4 | byte(KindInt64))
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	// Field 2: a valid bigint with a leading-zero magnitude, fully delimited.
	buf.WriteByte(0x80 | byte(Version2)<<4 | byte(KindBig))
	buf.Write([]byte{0x09, 0x00, 0x00, 1, 2, 3, 4, 5, 6, 7, 8})
	// Field 3: valid inline value 42.
	buf.WriteByte(42)

	r := bytes.NewReader(buf.Bytes())
	if _, err := Decode(r, MaxVersion); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("field 1: got %v, want ErrNonCanonical", err)
	}
	if _, err := Decode(r, MaxVersion); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("field 2: got %v, want ErrNonCanonical", err)
	}
	v, err := Decode(r, MaxVersion)
	if err != nil || v.Int64() != 42 {
		t.Fatalf("field 3 after rejections: got %v, err %v", v, err)
	}
	if r.Len() != 0 {
		t.Fatalf("%d unconsumed bytes remain", r.Len())
	}
}

// TestLengthValidatedBeforeAllocation: an absurd bigint length prefix must
// be rejected by the cap, without reading or allocating that much.
func TestLengthValidatedBeforeAllocation(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x80 | byte(Version2)<<4 | byte(KindBig))
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}) // uvarint = 0xFFFFFFFF > cap
	if _, err := Decode(bytes.NewReader(buf.Bytes()), MaxVersion); !errors.Is(err, ErrLength) {
		t.Fatalf("got %v, want ErrLength", err)
	}
	// Exactly at the cap the length is accepted; decoding then fails
	// only because the magnitude bytes are missing.
	buf.Reset()
	buf.WriteByte(0x80 | byte(Version2)<<4 | byte(KindBig))
	var uv [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(uv[:], MaxBigIntBytes)
	buf.Write(uv[:n])
	if _, err := Decode(bytes.NewReader(buf.Bytes()), MaxVersion); err != io.EOF {
		t.Fatalf("at-cap length: got %v, want EOF (magnitude missing)", err)
	}
}

// TestBigIntRandomAgainstMathBig: random arbitrary-precision values
// round-trip and agree with independent math/big arithmetic.
func TestBigIntRandomAgainstMathBig(t *testing.T) {
	rng := rand.New(rand.NewSource(20260928))
	for i := 0; i < 500; i++ {
		bitLen := 1 + rng.Intn(1200)
		v := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), uint(bitLen)))
		if rng.Intn(2) == 0 {
			v.Neg(v)
		}
		b := encodeBytes(t, v, Version2)
		got, err := Decode(bytes.NewReader(b), MaxVersion)
		if err != nil {
			t.Fatalf("iter %d (%d bits): %v", i, bitLen, err)
		}
		if got.Cmp(v) != 0 || got.String() != v.String() {
			t.Fatalf("iter %d: round-trip mismatch for %d-bit value", i, bitLen)
		}
	}
	// Independent arithmetic check: dec(enc(a)) + dec(enc(b)) == a + b,
	// with the sum computed by math/big alone.
	for i := 0; i < 100; i++ {
		a := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 300))
		b := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 300))
		a.Neg(a)
		da, _ := Decode(bytes.NewReader(encodeBytes(t, a, Version2)), MaxVersion)
		db, _ := Decode(bytes.NewReader(encodeBytes(t, b, Version2)), MaxVersion)
		if new(big.Int).Add(da, db).Cmp(new(big.Int).Add(a, b)) != 0 {
			t.Fatalf("iter %d: decoded arithmetic disagrees with math/big", i)
		}
	}
}

// TestVersionNegotiation: V1 streams reject bigint, V1 decoders reject V2 tags.
func TestVersionNegotiation(t *testing.T) {
	bigv := new(big.Int).Lsh(big.NewInt(1), 100)
	var buf bytes.Buffer
	if err := Encode(&buf, bigv, Version1); !errors.Is(err, ErrVersion) {
		t.Fatalf("Encode bigint at V1 = %v, want ErrVersion", err)
	}
	wire := encodeBytes(t, bigv, Version2)
	if _, err := Decode(bytes.NewReader(wire), Version1); !errors.Is(err, ErrVersion) {
		t.Fatalf("Decode V2 tag with maxVersion=1 = %v, want ErrVersion", err)
	}
	// Fixed-width kinds are identical across versions.
	small := big.NewInt(-12345)
	if got, err := Decode(bytes.NewReader(encodeBytes(t, small, Version1)), Version1); err != nil || got.Cmp(small) != 0 {
		t.Fatalf("V1 fixed-width round-trip: got %v err %v", got, err)
	}
}

// TestStreamOfMixedFields decodes a heterogeneous field sequence.
func TestStreamOfMixedFields(t *testing.T) {
	fields := []Field{Uint8, Int16, Uint64, Big(256)}
	vals := []*big.Int{
		big.NewInt(200),
		big.NewInt(-30000),
		new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1)),
		new(big.Int).Lsh(big.NewInt(1), 200),
	}
	var buf bytes.Buffer
	for i, f := range fields {
		if err := EncodeField(&buf, f, vals[i], Version2); err != nil {
			t.Fatalf("EncodeField %d: %v", i, err)
		}
	}
	r := bytes.NewReader(buf.Bytes())
	for i, f := range fields {
		got, err := DecodeField(r, f, MaxVersion)
		if err != nil {
			t.Fatalf("DecodeField %d: %v", i, err)
		}
		if got.Int.Cmp(vals[i]) != 0 {
			t.Fatalf("field %d: got %v want %v", i, got.Int, vals[i])
		}
	}
	if r.Len() != 0 {
		t.Fatalf("trailing %d bytes", r.Len())
	}
}
