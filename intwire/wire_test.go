package intwire

import (
	"bytes"
	"math/big"
	"math/rand"
	"testing"
)

var versions = []Version{V1, V2}

func mustU(t *testing.T, w int, u uint64) Value {
	t.Helper()
	v, err := NewUnsigned(w, u)
	if err != nil {
		t.Fatalf("NewUnsigned(%d,%d): %v", w, u, err)
	}
	return v
}

func mustS(t *testing.T, w int, i int64) Value {
	t.Helper()
	v, err := NewSigned(w, i)
	if err != nil {
		t.Fatalf("NewSigned(%d,%d): %v", w, i, err)
	}
	return v
}

func roundTrip(t *testing.T, ver Version, v Value) Value {
	t.Helper()
	buf, err := Append(nil, ver, v)
	if err != nil {
		t.Fatalf("Append v%d %v: %v", ver, v.Decimal(), err)
	}
	d := NewDecoder(buf)
	got, err := d.Decode()
	if err != nil {
		t.Fatalf("Decode v%d %v: %v", ver, v.Decimal(), err)
	}
	if !d.Done() {
		t.Fatalf("v%d %v: %d trailing bytes", ver, v.Decimal(), len(buf)-d.Offset())
	}
	if !got.Equal(v) {
		t.Fatalf("v%d round trip: got %v want %v", ver, got.Decimal(), v.Decimal())
	}
	// Canonical uniqueness: re-encoding the decoded value reproduces the
	// exact same byte string.
	buf2, err := Append(nil, ver, got)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(buf, buf2) {
		t.Fatalf("v%d %v: encoding not canonical: %x vs %x", ver, v.Decimal(), buf, buf2)
	}
	return got
}

// Every supported width: exhaustive small range around zero plus the
// width's extreme values, for both protocol versions.
func TestFixedWidthsSmallRangeAndExtremes(t *testing.T) {
	const small = 1 << 12
	for _, w := range SupportedWidths {
		// clamp the exhaustive small range to what the width can hold
		lim := int64(small)
		if max := int64(1)<<(uint(w)-1) - 1; max < lim {
			lim = max
		}
		for _, ver := range versions {
			// exhaustive small range around zero
			for i := -lim; i <= lim; i++ {
				roundTrip(t, ver, mustS(t, w, i))
				if i >= 0 {
					roundTrip(t, ver, mustU(t, w, uint64(i)))
				}
			}
			// extremes
			umax := uint64(1)<<uint(w) - 1
			if w == 64 {
				umax = ^uint64(0)
			}
			smin := -(int64(1) << (uint(w) - 1))
			smax := int64(1)<<(uint(w)-1) - 1
			for _, u := range []uint64{0, 1, umax - 1, umax} {
				roundTrip(t, ver, mustU(t, w, u))
			}
			for _, i := range []int64{smin, smin + 1, -1, 0, 1, smax - 1, smax} {
				roundTrip(t, ver, mustS(t, w, i))
			}
		}
	}
}

// Widths 8 and 16 are cheap enough to exhaust completely.
func TestExhaustive8And16Bit(t *testing.T) {
	for _, ver := range versions {
		for u := 0; u <= 0xff; u++ {
			roundTrip(t, ver, mustU(t, 8, uint64(u)))
		}
		for i := -0x80; i <= 0x7f; i++ {
			roundTrip(t, ver, mustS(t, 8, int64(i)))
		}
		for u := 0; u <= 0xffff; u++ {
			roundTrip(t, ver, mustU(t, 16, uint64(u)))
		}
		for i := -0x8000; i <= 0x7fff; i++ {
			roundTrip(t, ver, mustS(t, 16, int64(i)))
		}
	}
}

func TestUint64MaxRoundTrip(t *testing.T) {
	for _, ver := range versions {
		got := roundTrip(t, ver, mustU(t, 64, ^uint64(0)))
		if got.U != ^uint64(0) {
			t.Fatalf("u64 max corrupted: %d", got.U)
		}
	}
}

func TestNegativeIntoUnsignedRejected(t *testing.T) {
	// Construction-time: overflow past width.
	if _, err := NewUnsigned(8, 256); err == nil {
		t.Fatal("u8 accepted 256")
	}
	// Decode-time: varint payload exceeding the declared width.
	for _, ver := range versions {
		good, err := Append(nil, ver, mustU(t, 64, 300))
		if err != nil {
			t.Fatal(err)
		}
		// Re-tag the u64 payload as u8.
		bad := bytes.Clone(good)
		if ver == V1 {
			bad[1] = byte(0<<6 | 1) // kind unsigned, width code 1 => u8
		} else {
			bad[2] = 8
		}
		if _, err := NewDecoder(bad).Decode(); err == nil {
			t.Fatalf("v%d: u8 field accepted 300", ver)
		}
	}
	// JSON bridge: negative string into an unsigned field.
	var v Value
	if err := v.UnmarshalJSON([]byte(`{"k":"u","w":64,"v":"-1"}`)); err == nil {
		t.Fatal("unsigned JSON field accepted -1")
	}
}

// Random arbitrary-precision values cross-checked against math/big, which
// serves as the independent reference integer implementation.
func TestBigRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, ver := range versions {
		for i := 0; i < 500; i++ {
			bits := uint(rng.Intn(600) + 1)
			ref := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), bits))
			if rng.Intn(2) == 0 {
				ref.Neg(ref)
			}
			v := NewBig(ref)
			buf, err := Append(nil, ver, v)
			if err != nil {
				t.Fatal(err)
			}
			got, err := NewDecoder(buf).Decode()
			if err != nil {
				t.Fatal(err)
			}
			if got.B.Cmp(ref) != 0 {
				t.Fatalf("v%d: got %s want %s", ver, got.B, ref)
			}
		}
	}
}

// Non-canonical encodings must be rejected AND must not consume input:
// the following field must still decode from the same decoder position.
func TestNonCanonicalRejectedWithoutConsuming(t *testing.T) {
	next, err := Append(nil, V1, mustU(t, 8, 0x2a))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"v1 varint trailing zero group": {byte(V1), 0x01, 0x80, 0x00},       // u8 = 0 non-minimal
		"v1 varint value 1 padded":      {byte(V1), 0x01, 0x81, 0x00},       // u8 = 1 non-minimal
		"v1 big positive sign-extended": {byte(V1), 0x80, 0x02, 0x00, 0x7f}, // +127 with redundant 0x00
		"v1 big negative sign-extended": {byte(V1), 0x80, 0x02, 0xff, 0x80}, // -128 with redundant 0xff
		"v1 big zero length":            {byte(V1), 0x80, 0x00},             // zero must be one 0x00 byte
		"v2 big negative zero":          {byte(V2), 2, 0, 0x01, 0x00},
		"v2 big leading zero magnitude": {byte(V2), 2, 0, 0x00, 0x02, 0x00, 0x05},
		"v2 big bad sign byte":          {byte(V2), 2, 0, 0x07, 0x01, 0x05},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			stream := append(bytes.Clone(bad), next...)
			d := NewDecoder(stream)
			if _, err := d.Decode(); err == nil {
				t.Fatal("non-canonical encoding accepted")
			}
			if d.Offset() != 0 {
				t.Fatalf("decoder consumed %d bytes on error", d.Offset())
			}
			// Skip the bad field manually; the next field must be intact.
			d2 := NewDecoder(stream[len(bad):])
			v, err := d2.Decode()
			if err != nil || v.U != 0x2a {
				t.Fatalf("next field corrupted: %v %v", v, err)
			}
		})
	}
}

func TestVarintOverflowRejected(t *testing.T) {
	// 10th byte contributing more than bit 63.
	bad := []byte{byte(V1), 0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}
	if _, err := NewDecoder(bad).Decode(); err != ErrOverflow {
		t.Fatalf("got %v want ErrOverflow", err)
	}
}

// Declared lengths are validated against the limit and the remaining input
// BEFORE any allocation.
func TestLengthValidatedBeforeAllocation(t *testing.T) {
	for _, ver := range versions {
		var hdr []byte
		if ver == V1 {
			hdr = []byte{byte(V1), 0x80}
		} else {
			hdr = []byte{byte(V2), 2, 0, 0x00}
		}
		// Absurd declared length, no payload at all.
		bad := appendUvarint(bytes.Clone(hdr), MaxBigBytes+1)
		if _, err := NewDecoder(bad).Decode(); err != ErrLength {
			t.Fatalf("v%d oversize: got %v want ErrLength", ver, err)
		}
		// Plausible length but truncated payload.
		bad = appendUvarint(bytes.Clone(hdr), 100)
		bad = append(bad, 0x01, 0x02)
		if _, err := NewDecoder(bad).Decode(); err != ErrTruncated {
			t.Fatalf("v%d truncated: got %v want ErrTruncated", ver, err)
		}
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	if _, err := NewDecoder([]byte{0x7f, 0x00}).Decode(); err != ErrVersion {
		t.Fatalf("got %v want ErrVersion", err)
	}
	if _, err := Append(nil, Version(9), mustU(t, 8, 1)); err != ErrVersion {
		t.Fatalf("got %v want ErrVersion", err)
	}
}

// The same value has exactly one canonical encoding: alternative byte
// strings for it are rejected, and decode(encode(v)) re-encodes identically.
func TestCanonicalUniqueness(t *testing.T) {
	v := mustU(t, 16, 1)
	canon, err := Append(nil, V1, v)
	if err != nil {
		t.Fatal(err)
	}
	alt := []byte{byte(V1), 0x02, 0x81, 0x00} // same value, padded varint
	if bytes.Equal(canon, alt) {
		t.Fatal("test bug: alt equals canonical")
	}
	if _, err := NewDecoder(alt).Decode(); err != ErrNonCanonical {
		t.Fatalf("alt accepted: %v", err)
	}
}

// Multi-version interop: a mixed stream of v1 and v2 values decodes in
// order via the version byte.
func TestMixedVersionStream(t *testing.T) {
	vals := []struct {
		ver Version
		v   Value
	}{
		{V1, mustU(t, 8, 200)},
		{V2, mustS(t, 32, -123456)},
		{V1, NewBig(big.NewInt(-987654321))},
		{V2, mustU(t, 64, ^uint64(0))},
	}
	var stream []byte
	for _, e := range vals {
		var err error
		stream, err = Append(stream, e.ver, e.v)
		if err != nil {
			t.Fatal(err)
		}
	}
	d := NewDecoder(stream)
	for i, e := range vals {
		got, err := d.Decode()
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		if !got.Equal(e.v) {
			t.Fatalf("field %d: got %v want %v", i, got.Decimal(), e.v.Decimal())
		}
	}
	if !d.Done() {
		t.Fatal("trailing bytes")
	}
}
