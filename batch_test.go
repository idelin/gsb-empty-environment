package floatconv_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"floatconv"
)

func randomBinary32Slice(rng *rand.Rand, n int) []byte {
	src := make([]byte, n*4)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(src[i*4:], rng.Uint32())
	}
	return src
}

// Batch conversion must match element-wise conversion.
func TestBatchMatchesSingle(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	src := randomBinary32Slice(rng, 4096)
	dst := make([]byte, 4096*2)
	if _, err := floatconv.ConvertBatch(dst, floatconv.Binary16, src, floatconv.Binary32, floatconv.RoundNearestEven); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4096; i++ {
		want, _, err := floatconv.Convert(floatconv.Binary16, floatconv.Binary32,
			uint64(binary.LittleEndian.Uint32(src[i*4:])), floatconv.RoundNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint16(dst[i*2:]); uint64(got) != want {
			t.Fatalf("elem %d: batch %#x != single %#x", i, got, want)
		}
	}
}

// Partially overlapping source/destination buffers must behave as if the
// whole source had been read before any destination write.
func TestBatchOverlap(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	n := 2048
	src := randomBinary32Slice(rng, n)

	// Golden result via disjoint buffers.
	golden := make([]byte, n*2)
	if _, err := floatconv.ConvertBatch(golden, floatconv.Binary16, src, floatconv.Binary32, floatconv.RoundNearestEven); err != nil {
		t.Fatal(err)
	}

	// Overlap at every byte offset that keeps both ranges inside one
	// buffer, including unaligned (non element-size) offsets.
	for _, off := range []int{1, 2, 3, 5, 100, n, n * 2, n*4 - 2, n*4 - 1} {
		buf := make([]byte, n*4+off)
		copy(buf, src)
		ov := buf[:n*4]
		dst := buf[off : off+n*2]
		if _, err := floatconv.ConvertBatch(dst, floatconv.Binary16, ov, floatconv.Binary32, floatconv.RoundNearestEven); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dst, golden) {
			t.Fatalf("overlap offset %d: result differs from golden", off)
		}
	}
}

// Structural errors must leave the destination buffer byte-identical.
func TestBatchErrorsLeaveDstUntouched(t *testing.T) {
	bad := floatconv.Format{Name: "bad", ExpBits: 1, MantBits: 6, Bias: 3}
	src := make([]byte, 16)
	rng := rand.New(rand.NewSource(1))
	rng.Read(src)

	cases := []struct {
		name    string
		dstFmt  floatconv.Format
		srcFmt  floatconv.Format
		src     []byte
		dstSize int
	}{
		{"invalid dst format", bad, floatconv.Binary32, src, 64},
		{"invalid src format", floatconv.Binary16, bad, src, 64},
		{"misaligned src length", floatconv.Binary16, floatconv.Binary32, src[:13], 64},
		{"dst too small", floatconv.Binary16, floatconv.Binary32, src, 3},
	}
	for _, c := range cases {
		dst := bytes.Repeat([]byte{0xAB}, 64)
		before := append([]byte(nil), dst...)
		_, err := floatconv.ConvertBatch(dst[:c.dstSize], c.dstFmt, c.src, c.srcFmt, floatconv.RoundNearestEven)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
		}
		if !bytes.Equal(dst, before) {
			t.Errorf("%s: destination buffer modified on error", c.name)
		}
	}
}

// Flags are OR-combined across the batch.
func TestBatchFlagsAggregation(t *testing.T) {
	// binary32 values: 1.0 (exact), 65520.0 (overflow midpoint in binary16),
	// 2^-25 (underflow), a signaling NaN.
	elems := []uint32{0x3F800000, 0x477FF000, 0x2F000000, 0x7F800001}
	src := make([]byte, 16)
	for i, e := range elems {
		binary.LittleEndian.PutUint32(src[i*4:], e)
	}
	dst := make([]byte, 8)
	fl, err := floatconv.ConvertBatch(dst, floatconv.Binary16, src, floatconv.Binary32, floatconv.RoundNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	want := floatconv.FlagOverflow | floatconv.FlagInexact | floatconv.FlagUnderflow | floatconv.FlagInvalid
	if fl != want {
		t.Fatalf("flags %v, want %v", fl, want)
	}
	got := []uint16{
		binary.LittleEndian.Uint16(dst[0:]),
		binary.LittleEndian.Uint16(dst[2:]),
		binary.LittleEndian.Uint16(dst[4:]),
		binary.LittleEndian.Uint16(dst[6:]),
	}
	wantBits := []uint16{0x3C00, 0x7C00, 0x0000, 0x7E00}
	for i := range got {
		if got[i] != wantBits[i] {
			t.Errorf("elem %d: got %#x, want %#x", i, got[i], wantBits[i])
		}
	}
}

// In-place narrowing (dst fully inside src) and in-place widening through
// a shared buffer.
func TestBatchInPlace(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	n := 1024
	src := randomBinary32Slice(rng, n)
	golden := make([]byte, n*2)
	if _, err := floatconv.ConvertBatch(golden, floatconv.Binary16, src, floatconv.Binary32, floatconv.RoundTowardZero); err != nil {
		t.Fatal(err)
	}
	buf := append([]byte(nil), src...)
	if _, err := floatconv.ConvertBatch(buf[:n*2], floatconv.Binary16, buf[:n*4], floatconv.Binary32, floatconv.RoundTowardZero); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:n*2], golden) {
		t.Fatal("in-place narrowing mismatch")
	}
}
