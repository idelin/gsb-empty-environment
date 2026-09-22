package fp

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

func encode32(vals ...uint32) []byte {
	b := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(b[i*4:], v)
	}
	return b
}

func TestBatchBasic(t *testing.T) {
	src := encode32(0x3f800000, 0x3dcccccd, 0x00000000, 0x80000000, 0x7f800000, 0x7fc00000)
	dst := make([]byte, 2*len(src)/4)
	fl, err := Batch(Binary16, Binary32, dst, src, len(src)/4, ToNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{0x3c00, 0x2e66, 0x0000, 0x8000, 0x7c00, 0x7e00}
	for i, w := range want {
		if got := binary.LittleEndian.Uint16(dst[i*2:]); got != w {
			t.Fatalf("elem %d = %04x want %04x flags=%v", i, got, w, fl[i])
		}
	}
}

// TestBatchOverlap runs every meaningful partial overlap for in-place f32->f16
// and verifies snapshot semantics against a fresh conversion.
func TestBatchOverlap(t *testing.T) {
	const n = 64
	rng := rand.New(rand.NewSource(1))
	orig := make([]uint32, n)
	for i := range orig {
		orig[i] = rng.Uint32()
	}
	base := encode32(orig...)

	expected := make([]byte, 2*n)
	if _, err := Batch(Binary16, Binary32, expected, base, n, ToNearestEven); err != nil {
		t.Fatal(err)
	}

	// Same backing array, with the f32 source starting at every offset that
	// still leaves room for n f16 outputs, including negative offsets.
	for dstOff := -2 * n; dstOff <= 4*n; dstOff++ {
		// Large scratch so either alignment is valid.
		buf := make([]byte, 8*n+16)
		off := 4 * n
		copy(buf[off:], base)
		srcSlice := buf[off : off+4*n]
		dstStart := off + dstOff
		if dstStart < 0 || dstStart+2*n > len(buf) {
			continue
		}
		dstSlice := buf[dstStart : dstStart+2*n]
		if _, err := Batch(Binary16, Binary32, dstSlice, srcSlice, n, ToNearestEven); err != nil {
			t.Fatalf("offset %d: %v", dstOff, err)
		}
		if !bytes.Equal(dstSlice, expected) {
			t.Fatalf("offset %d: overlap result differs from snapshot semantics", dstOff)
		}
	}

	// Equal-size format in-place (f16 -> bf16, both 2 bytes) full overlap.
	half := make([]byte, 2*n)
	wantBF := make([]byte, 2*n)
	// build f16 inputs
	f16vals := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(f16vals[i*2:], uint16(orig[i]))
	}
	copy(half, f16vals)
	if _, err := Batch(BFloat16, Binary16, wantBF, f16vals, n, ToNearestEven); err != nil {
		t.Fatal(err)
	}
	if _, err := Batch(BFloat16, Binary16, half, half, n, ToNearestEven); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(half, wantBF) {
		t.Fatal("in-place equal-width overlap mismatch")
	}
}

// TestBatchErrorsAtomic ensures structural failures leave dst untouched.
func TestBatchErrorsAtomic(t *testing.T) {
	src := encode32(0x3f800000, 0x40000000)
	dst := bytes.Repeat([]byte{0xab}, 3)
	sentinel := append([]byte(nil), dst...)

	if _, err := Batch(Binary16, Binary32, dst, src, 2, ToNearestEven); err == nil {
		t.Fatal("expected short-destination error")
	}
	if !bytes.Equal(dst, sentinel) {
		t.Fatal("destination modified after length error")
	}
	if _, err := Batch(Binary16, Binary32, make([]byte, 8), src[:3], 2, ToNearestEven); err == nil {
		t.Fatal("expected short-source error")
	}
	if _, err := Batch(Binary16, Binary32, nil, src, 1, ToNearestEven); err == nil {
		t.Fatal("expected nil-destination error")
	}
	if _, err := Batch(Binary16, Binary32, make([]byte, 2), src, -1, ToNearestEven); err == nil {
		t.Fatal("expected negative-count error")
	}
}

func BenchmarkBatchF32ToF16(b *testing.B) {
	const n = 1 << 16
	src := make([]byte, 4*n)
	dst := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(src[i*4:], uint32(i)*2654435761)
	}
	b.SetBytes(int64(4 * n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Batch(Binary16, Binary32, dst, src, n, ToNearestEven); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBatchF64ToF32(b *testing.B) {
	const n = 1 << 16
	src := make([]byte, 8*n)
	dst := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint64(src[i*8:], uint64(i)*11400714819323198485)
	}
	b.SetBytes(int64(8 * n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Batch(Binary32, Binary64, dst, src, n, ToNearestEven); err != nil {
			b.Fatal(err)
		}
	}
}
