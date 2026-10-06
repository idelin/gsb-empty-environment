package bitstream

import (
	"bytes"
	"testing"
)

func TestReadBitsMSBOrder(t *testing.T) {
	// data = 10110011 01000001
	r := NewReader([]byte{0xB3, 0x41})
	for _, tc := range []struct {
		n    int
		want uint64
	}{
		{1, 0x1},  // 1
		{3, 0x3},  // 011
		{4, 0x3},  // 0011
		{8, 0x41}, // 01000001
	} {
		got, err := r.ReadBits(tc.n)
		if err != nil || got != tc.want {
			t.Fatalf("ReadBits(%d) = %x, %v; want %x", tc.n, got, err, tc.want)
		}
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", r.Remaining())
	}
}

func TestReadShortReadDoesNotAdvance(t *testing.T) {
	r := NewReader([]byte{0xFF})
	if _, err := r.ReadBits(9); err != ErrNotEnoughBits {
		t.Fatalf("err = %v, want ErrNotEnoughBits", err)
	}
	if r.Pos() != 0 {
		t.Fatalf("pos advanced to %d on failed read", r.Pos())
	}
	if _, err := r.ReadBits(0); err != ErrInvalidBitCount {
		t.Fatalf("err = %v, want ErrInvalidBitCount", err)
	}
	if _, err := r.ReadBits(65); err != ErrInvalidBitCount {
		t.Fatalf("err = %v, want ErrInvalidBitCount", err)
	}
	if r.Pos() != 0 {
		t.Fatalf("pos advanced to %d on invalid read", r.Pos())
	}
	v, err := r.ReadBits(8)
	if err != nil || v != 0xFF {
		t.Fatalf("ReadBits(8) = %x, %v", v, err)
	}
}

func TestReadSignedSignExtension(t *testing.T) {
	for _, tc := range []struct {
		bits uint64
		n    int
		want int64
	}{
		{0x1, 1, -1},
		{0x0, 1, 0},
		{0x7, 3, -1}, // 111
		{0x4, 3, -4}, // 100
		{0x3, 3, 3},  // 011
		{0xFFFFFFFFFFFFFFFF, 64, -1},
		{0x8000000000000000, 64, -9223372036854775808},
		{0x7FFFFFFFFFFFFFFF, 64, 9223372036854775807},
	} {
		w := NewWriter(Unlimited)
		if err := w.WriteBits(tc.bits, tc.n); err != nil {
			t.Fatal(err)
		}
		got, err := NewReader(w.Bytes()).ReadSigned(tc.n)
		if err != nil || got != tc.want {
			t.Fatalf("ReadSigned(%d) of %x = %d, %v; want %d", tc.n, tc.bits, got, err, tc.want)
		}
	}
}

func TestReaderBookmarkReset(t *testing.T) {
	// data = 10100000 00110000
	r := NewReader([]byte{0xA0, 0x30})
	if _, err := r.ReadBits(2); err != nil {
		t.Fatal(err)
	}
	bm := r.Bookmark()
	v, _ := r.ReadBits(3)
	if v != 0x4 { // 100
		t.Fatalf("got %x", v)
	}
	r.Reset(bm)
	v, _ = r.ReadBits(6)
	if v != 0x20 { // 100000
		t.Fatalf("after reset got %x", v)
	}
}

func TestWriteOverwriteKeepsNeighbours(t *testing.T) {
	orig := []byte{0xFF, 0x00}
	w := NewWriterFromBytes(orig, Unlimited)
	// Seek to bit 5 and overwrite 4 bits (0110) spanning the byte boundary.
	bm := w.Bookmark()
	bm.pos = 5
	if err := w.Seek(bm); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0x6, 4); err != nil {
		t.Fatal(err)
	}
	// 11111 011 0 0000000 -> neighbours of bits 5..8 untouched.
	want := []byte{0xFB, 0x00}
	if got := w.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("got %08b, want %08b", got, want)
	}
}

func TestPaddingBitsAreZero(t *testing.T) {
	w := NewWriter(Unlimited)
	if err := w.WriteBits(0x7, 3); err != nil { // 111
		t.Fatal(err)
	}
	got := w.Bytes()
	if len(got) != 1 || got[0] != 0xE0 { // 11100000
		t.Fatalf("got %08b", got)
	}
	if err := w.WriteBits(1, 1); err != nil {
		t.Fatal(err)
	}
	if got := w.Bytes(); got[0] != 0xF0 { // 11110000
		t.Fatalf("got %08b", got)
	}
}

func TestCapacityExceededLeavesWriterUnchanged(t *testing.T) {
	w := NewWriter(8)
	if err := w.WriteBits(0xAB, 8); err != nil {
		t.Fatal(err)
	}
	before := w.Bytes()
	if err := w.WriteBits(1, 1); err != ErrCapacityExceeded {
		t.Fatalf("err = %v, want ErrCapacityExceeded", err)
	}
	if !bytes.Equal(w.Bytes(), before) || w.Pos() != 8 {
		t.Fatalf("writer mutated on capacity failure")
	}
}

func TestNestedRegionsBackfill(t *testing.T) {
	w := NewWriter(Unlimited)
	outer, err := w.BeginRegion(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0xA, 4); err != nil {
		t.Fatal(err)
	}
	inner, err := w.BeginRegion(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0x5, 3); err != nil { // 101
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0xFF, 8); err != nil {
		t.Fatal(err)
	}
	if err := outer.Close(); err != nil {
		t.Fatal(err)
	}
	// outer content = 4 + (4 field + 3 content) + 8 = 19
	r := NewReader(w.Bytes())
	olen, _ := r.ReadBits(8)
	if olen != 19 {
		t.Fatalf("outer length = %d, want 19", olen)
	}
	if v, _ := r.ReadBits(4); v != 0xA {
		t.Fatalf("payload = %x", v)
	}
	ilen, _ := r.ReadBits(4)
	if ilen != 3 {
		t.Fatalf("inner length = %d, want 3", ilen)
	}
	if v, _ := r.ReadBits(3); v != 0x5 {
		t.Fatalf("inner payload = %x", v)
	}
	if v, _ := r.ReadBits(8); v != 0xFF {
		t.Fatalf("tail = %x", v)
	}
}

func TestRegionAbortRestoresOriginalBits(t *testing.T) {
	orig := []byte{0xAA, 0x55}
	w := NewWriterFromBytes(orig, Unlimited)
	bm := w.Bookmark()
	bm.pos = 3
	if err := w.Seek(bm); err != nil {
		t.Fatal(err)
	}
	reg, err := w.BeginRegion(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0xFFFF, 16); err != nil {
		t.Fatal(err)
	}
	reg.Abort()
	if !bytes.Equal(w.Bytes(), orig) {
		t.Fatalf("after abort got %08b, want %08b", w.Bytes(), orig)
	}
	if w.Pos() != 3 {
		t.Fatalf("cursor = %d, want 3", w.Pos())
	}
	// Old bookmarks are stale after rollback.
	if err := w.Seek(bm); err != ErrStaleBookmark {
		t.Fatalf("seek with old bookmark: err = %v, want ErrStaleBookmark", err)
	}
	// Fresh bookmarks work.
	if err := w.Seek(w.Bookmark()); err != nil {
		t.Fatal(err)
	}
}

func TestNestedAbortKeepsOuterRegion(t *testing.T) {
	w := NewWriter(Unlimited)
	outer, _ := w.BeginRegion(8)
	if err := w.WriteBits(0x3, 2); err != nil { // 11
		t.Fatal(err)
	}
	inner, _ := w.BeginRegion(4)
	if err := w.WriteBits(0xFF, 8); err != nil {
		t.Fatal(err)
	}
	inner.Abort() // rolls back only the inner region
	if err := w.WriteBits(0x0, 1); err != nil {
		t.Fatal(err)
	}
	if err := outer.Close(); err != nil {
		t.Fatal(err)
	}
	// outer content = 2 + 1 = 3
	r := NewReader(w.Bytes())
	olen, _ := r.ReadBits(8)
	if olen != 3 {
		t.Fatalf("outer length = %d, want 3", olen)
	}
	if v, _ := r.ReadBits(3); v != 0x6 { // 110
		t.Fatalf("payload = %x, want 6", v)
	}
}

func TestRegionLengthOverflow(t *testing.T) {
	w := NewWriter(Unlimited)
	reg, _ := w.BeginRegion(2)
	if err := w.WriteBits(0, 4); err != nil {
		t.Fatal(err)
	}
	if err := reg.Close(); err != ErrLengthOverflow {
		t.Fatalf("err = %v, want ErrLengthOverflow", err)
	}
}

func TestRoundTripVariousWidths(t *testing.T) {
	w := NewWriter(Unlimited)
	vals := []struct {
		v uint64
		n int
	}{{0x1, 1}, {0x2, 3}, {0x7, 5}, {0xAB, 8}, {0x1AB, 9}, {0x123456789ABCDEF0, 64}, {0x1, 17}}
	for _, tc := range vals {
		if err := w.WriteBits(tc.v, tc.n); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(w.Bytes())
	for _, tc := range vals {
		got, err := r.ReadBits(tc.n)
		if err != nil || got != tc.v {
			t.Fatalf("round trip n=%d: got %x, %v; want %x", tc.n, got, err, tc.v)
		}
	}
}
