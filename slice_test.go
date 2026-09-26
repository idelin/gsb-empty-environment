package blockstore

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func pattern(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*31 + 7)
	}
	return p
}

func TestCrossBlockRead(t *testing.T) {
	st := NewStore(8, 0)
	data := pattern(8*10 + 3) // 83 bytes, 11 blocks
	s, err := st.NewSlice(data)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release()

	if got := s.Bytes(); !bytes.Equal(got, data) {
		t.Fatal("full read mismatch")
	}
	// Read every odd window crossing block boundaries.
	for off := 0; off < len(data); off++ {
		for n := 0; n <= len(data)-off && n <= 25; n++ {
			got, err := s.ReadRange(off, n)
			if err != nil {
				t.Fatalf("ReadRange(%d,%d): %v", off, n, err)
			}
			if !bytes.Equal(got, data[off:off+n]) {
				t.Fatalf("ReadRange(%d,%d) mismatch", off, n)
			}
		}
	}
	// io.ReaderAt semantics.
	buf := make([]byte, 10)
	n, err := s.ReadAt(buf, 5)
	if n != 10 || err != nil || !bytes.Equal(buf, data[5:15]) {
		t.Fatalf("ReadAt mid: n=%d err=%v", n, err)
	}
	n, err = s.ReadAt(buf, int64(len(data)-4))
	if n != 4 || err != io.EOF || !bytes.Equal(buf[:4], data[len(data)-4:]) {
		t.Fatalf("ReadAt tail: n=%d err=%v", n, err)
	}
	if _, err = s.ReadAt(buf, int64(len(data))); err != io.EOF {
		t.Fatalf("ReadAt at end: err=%v", err)
	}
}

func TestEmptySlice(t *testing.T) {
	st := NewStore(16, 0)
	s, err := st.NewSlice(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release()

	if s.Len() != 0 {
		t.Fatal("empty slice length")
	}
	if _, err := s.ReadAt(make([]byte, 4), 0); err != io.EOF {
		t.Fatalf("empty ReadAt err=%v", err)
	}
	if _, err := s.ReadRange(0, 1); err != ErrOutOfRange {
		t.Fatalf("empty ReadRange err=%v", err)
	}
	if _, err := s.Uint32(0, binary.BigEndian); err != ErrOutOfRange {
		t.Fatalf("empty Uint32 err=%v", err)
	}
	if got := s.Bytes(); got != nil {
		t.Fatalf("empty Bytes=%v", got)
	}
	// Growing an empty slice works.
	s2, err := s.Insert(0, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()
	if string(s2.Bytes()) != "hello" {
		t.Fatalf("got %q", s2.Bytes())
	}
	// Zero-length edits are no-ops on empty content.
	s3, err := s.Delete(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Release()
	if s3.Len() != 0 {
		t.Fatal("delete-0 changed length")
	}
}

func TestEndiannessAcrossBlocks(t *testing.T) {
	st := NewStore(8, 0)
	buf := make([]byte, 64)
	binary.LittleEndian.PutUint32(buf[6:], 0xDEADBEEF) // straddles block 0/1
	binary.BigEndian.PutUint32(buf[13:], 0x01020304)   // straddles block 1/2
	binary.LittleEndian.PutUint64(buf[28:], 0x0102030405060708)
	binary.BigEndian.PutUint16(buf[39:], 0xABCD) // straddles block 4/5
	s, err := st.NewSlice(buf)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release()

	if v, err := s.Uint32(6, binary.LittleEndian); err != nil || v != 0xDEADBEEF {
		t.Fatalf("LE uint32: %08x err=%v", v, err)
	}
	if v, err := s.Uint32(13, binary.BigEndian); err != nil || v != 0x01020304 {
		t.Fatalf("BE uint32: %08x err=%v", v, err)
	}
	if v, err := s.Uint64(28, binary.LittleEndian); err != nil || v != 0x0102030405060708 {
		t.Fatalf("LE uint64: %016x err=%v", v, err)
	}
	if v, err := s.Uint16(39, binary.BigEndian); err != nil || v != 0xABCD {
		t.Fatalf("BE uint16: %04x err=%v", v, err)
	}
	// Same bytes decoded by the reference implementation on a flat buffer.
	if binary.BigEndian.Uint16(buf[39:]) != 0xABCD {
		t.Fatal("test buffer wrong")
	}
	if _, err := s.Uint64(60, binary.BigEndian); err != ErrOutOfRange {
		t.Fatalf("overflowing read err=%v", err)
	}
}

func TestIndexEncodingLayout(t *testing.T) {
	// Round-trip, including non-monotonic ids produced by reordered edits.
	for _, exts := range [][]uint64{
		nil,
		{0},
		{3, 4, 5, 6},
		{9, 2, 7, 0, 100},
	} {
		enc := encodeIndex(12345, exts)
		ln, got, err := decodeIndex(enc)
		if err != nil {
			t.Fatal(err)
		}
		if ln != 12345 || len(got) != len(exts) {
			t.Fatalf("round trip mismatch: ln=%d exts=%v", ln, got)
		}
		for i := range exts {
			if got[i] != exts[i] {
				t.Fatalf("round trip mismatch at %d: %v vs %v", i, got, exts)
			}
		}
	}
	// Truncated/corrupt encodings are rejected.
	if _, _, err := decodeIndex([]byte{1, 200}); err == nil {
		t.Fatal("truncated index accepted")
	}
	if _, _, err := decodeIndex([]byte{99, 0, 0}); err == nil {
		t.Fatal("bad version accepted")
	}

	// A long append-only chain costs ~1 byte per block, not 8.
	st := NewStore(64, 0)
	s, err := st.NewSlice(make([]byte, 64*1000))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release()
	naive := 8 * 1000
	if s.IndexBytes() >= naive {
		t.Fatalf("encoded index %d bytes, naive array %d bytes", s.IndexBytes(), naive)
	}
	t.Logf("encoded index: %d bytes for 1000 blocks (naive fixed-width: %d)", s.IndexBytes(), naive)
}
