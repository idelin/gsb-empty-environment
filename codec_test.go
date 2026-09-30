package fpcodec

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"testing"
)

// samplePatterns covers every special class plus random noise.
func samplePatterns(w Width, n int) []uint64 {
	rng := rand.New(rand.NewPCG(3, 4))
	var specials []uint64
	switch w {
	case Width16:
		specials = []uint64{0x0000, 0x8000, 0x7C00, 0xFC00, 0x7E00, 0x7DFF,
			0x7C01, 0x0001, 0x8001, 0x03FF, 0x0400, 0x7BFF, 0x3555}
	case Width32:
		specials = []uint64{0x00000000, 0x80000000, 0x7F800000, 0xFF800000,
			0x7FC00000, 0x7FC00001, 0x7F800001, 0x00000001, 0x80000001,
			0x007FFFFF, 0x00800000, 0x7F7FFFFF}
	case Width64:
		specials = []uint64{0, 1 << 63, 0x7FF0000000000000, 0xFFF0000000000000,
			0x7FF8000000000000, 0x7FF8DEADBEEFCAFE, 0x7FF0000000000001,
			1, 1<<63 | 1, 0x000FFFFFFFFFFFFF, 0x0010000000000000,
			0x7FEFFFFFFFFFFFFF}
	}
	out := append([]uint64(nil), specials...)
	for len(out) < n {
		out = append(out, maskBits(w, rng.Uint64()))
	}
	return out
}

func TestRoundTripBothEndians(t *testing.T) {
	for _, w := range []Width{Width16, Width32, Width64} {
		vals := samplePatterns(w, 500)
		for _, e := range []Endian{Little, Big} {
			data, err := Marshal(w, e, vals)
			if err != nil {
				t.Fatalf("Marshal(%d, %d): %v", w, e, err)
			}
			gotW, got, err := Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal(%d, %d): %v", w, e, err)
			}
			if gotW != w {
				t.Fatalf("width: got %d want %d", gotW, w)
			}
			if !AllBitsEqual(w, vals, got) {
				t.Fatalf("round trip mismatch: width=%d endian=%d", w, e)
			}
		}
	}
}

func TestEndianSwitchRestoresBits(t *testing.T) {
	for _, w := range []Width{Width16, Width32, Width64} {
		vals := samplePatterns(w, 200)
		le, _ := Marshal(w, Little, vals)
		be, _ := Marshal(w, Big, vals)
		if bytes.Equal(le[headerSize:], be[headerSize:]) {
			t.Fatalf("width %d: little/big payloads unexpectedly identical", w)
		}
		// Decode little-endian stream, re-encode as big-endian, decode again:
		// the bit patterns must survive the endianness switch exactly.
		_, a, err := Unmarshal(le)
		if err != nil {
			t.Fatal(err)
		}
		mid, _ := Marshal(w, Big, a)
		_, b, err := Unmarshal(mid)
		if err != nil {
			t.Fatal(err)
		}
		if !AllBitsEqual(w, vals, b) {
			t.Fatalf("endian switch lost bits: width=%d", w)
		}
		// Both encodings of the same values decode identically.
		_, fromLE, _ := Unmarshal(le)
		_, fromBE, _ := Unmarshal(be)
		if !AllBitsEqual(w, fromLE, fromBE) {
			t.Fatalf("little/big decode mismatch: width=%d", w)
		}
	}
}

// chunkReader feeds data in irregular chunks to prove Decode does not
// depend on read granularity.
type chunkReader struct {
	data  []byte
	sizes []int
	pos   int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := c.sizes[0]
	c.sizes = append(c.sizes[1:], n) // cycle
	if n > len(p) {
		n = len(p)
	}
	if rem := len(c.data) - c.pos; n > rem {
		n = rem
	}
	copy(p, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

func TestArbitraryChunkedDecode(t *testing.T) {
	for _, w := range []Width{Width16, Width32, Width64} {
		vals := samplePatterns(w, 1000)
		for _, e := range []Endian{Little, Big} {
			data, _ := Marshal(w, e, vals)
			// Chunk sizes: 1, 2, 3, 5, 7, 11, 13, 64 — cycling.
			r := &chunkReader{data: data, sizes: []int{1, 2, 3, 5, 7, 11, 13, 64}}
			gotW, got, err := Decode(r)
			if err != nil {
				t.Fatalf("Decode chunked: %v", err)
			}
			if gotW != w || !AllBitsEqual(w, vals, got) {
				t.Fatalf("chunked decode mismatch: width=%d endian=%d", w, e)
			}
		}
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	vals := samplePatterns(Width32, 4)
	good, _ := Marshal(Width32, Little, vals)

	// Truncated payload must be rejected.
	if _, _, err := Unmarshal(good[:len(good)-1]); !errors.Is(err, ErrLengthMismatch) {
		t.Errorf("truncated: got %v, want ErrLengthMismatch", err)
	}
	// Trailing garbage must be rejected.
	if _, _, err := Unmarshal(append(good, 0xAA)); !errors.Is(err, ErrLengthMismatch) {
		t.Errorf("trailing: got %v, want ErrLengthMismatch", err)
	}
	// A forged count far beyond the buffer must be rejected before any
	// allocation is attempted.
	forged := append([]byte(nil), good[:headerSize]...)
	forged[6], forged[7], forged[8], forged[9] = 0, 0, 1, 0 // count = 256
	if _, _, err := Unmarshal(forged); !errors.Is(err, ErrLengthMismatch) {
		t.Errorf("forged count: got %v, want ErrLengthMismatch", err)
	}
	// Count above MaxElements is rejected outright.
	huge := append([]byte(nil), good[:headerSize]...)
	huge[6], huge[7], huge[8], huge[9] = 0xFF, 0xFF, 0xFF, 0xFF
	if _, _, err := Unmarshal(huge); !errors.Is(err, ErrTooMany) {
		t.Errorf("huge count: got %v, want ErrTooMany", err)
	}
	if _, _, err := Decode(bytes.NewReader(huge)); !errors.Is(err, ErrTooMany) {
		t.Errorf("huge count (stream): got %v, want ErrTooMany", err)
	}
	// Truncated stream.
	if _, _, err := Decode(bytes.NewReader(good[:len(good)-3])); err == nil {
		t.Errorf("truncated stream: want error")
	}
	// Bad magic / version / reserved / width / endianness.
	corrupt := func(i int, v byte) []byte {
		b := append([]byte(nil), good...)
		b[i] = v
		return b
	}
	if _, _, err := Unmarshal(corrupt(0, 'X')); !errors.Is(err, ErrBadMagic) {
		t.Errorf("magic: got %v", err)
	}
	if _, _, err := Unmarshal(corrupt(2, 99)); !errors.Is(err, ErrBadVersion) {
		t.Errorf("version: got %v", err)
	}
	if _, _, err := Unmarshal(corrupt(3, 8)); !errors.Is(err, ErrUnsupportedWidth) {
		t.Errorf("width: got %v", err)
	}
	if _, _, err := Unmarshal(corrupt(4, 7)); !errors.Is(err, ErrBadEndian) {
		t.Errorf("endian: got %v", err)
	}
	if _, _, err := Unmarshal(corrupt(5, 1)); !errors.Is(err, ErrReserved) {
		t.Errorf("reserved: got %v", err)
	}
}

func TestNaNNeverComparedWithEquality(t *testing.T) {
	// Demonstrate why comparisons must be bit-based: host float == lies.
	nanBits := uint64(0x7FF8DEADBEEFCAFE)
	f := math.Float64frombits(nanBits)
	if f == f {
		t.Fatal("host NaN compared equal to itself — test environment broken")
	}
	// Round trip through the codec, then compare with BitsEqual.
	vals := []uint64{nanBits, 0x7FF8000000000000, 1 << 63}
	data, _ := Marshal(Width64, Big, vals)
	_, got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range vals {
		if !BitsEqual(Width64, vals[i], got[i]) {
			t.Fatalf("NaN payload lost: %#x -> %#x", vals[i], got[i])
		}
	}
}

func TestNegativeZeroPreserved(t *testing.T) {
	for _, w := range []Width{Width16, Width32, Width64} {
		negZero := uint64(1) << (uint(w) - 1)
		data, _ := Marshal(w, Little, []uint64{negZero, 0})
		_, got, err := Unmarshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if !BitsEqual(w, got[0], negZero) || !BitsEqual(w, got[1], 0) {
			t.Fatalf("width %d: zero signs lost: %#x %#x", w, got[0], got[1])
		}
		// Conversion must keep the sign of zero too.
		for _, to := range []Width{Width16, Width32, Width64} {
			out, _, err := Convert(negZero, w, to, DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if !BitsEqual(to, out, uint64(1)<<(uint(to)-1)) {
				t.Fatalf("convert %d->%d: -0 became %#x", w, to, out)
			}
		}
	}
}

func TestEmptyStream(t *testing.T) {
	data, err := Marshal(Width16, Big, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, got, err := Unmarshal(data)
	if err != nil || w != Width16 || len(got) != 0 {
		t.Fatalf("empty round trip: w=%d n=%d err=%v", w, len(got), err)
	}
}
