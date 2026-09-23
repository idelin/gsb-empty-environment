package bwt

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func encodeToBytes(t *testing.T, src []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, src); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

func TestFormatRoundTrip(t *testing.T) {
	samples := [][]byte{
		{},
		{0},
		[]byte("banana"),
		bytes.Repeat([]byte{0xAB}, 4096),
		[]byte("the quick brown fox jumps over the lazy dog"),
	}
	for _, s := range samples {
		raw := encodeToBytes(t, s)
		blk, err := Decode(bytes.NewReader(raw), 1<<20)
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		out, err := blk.Inverse()
		if err != nil {
			t.Fatalf("Inverse(%q): %v", s, err)
		}
		if !bytes.Equal(out, s) {
			t.Fatalf("round trip of %q gave %q", s, out)
		}
		if uint64(len(out)) != blk.Length {
			t.Fatalf("decoded length %d, header says %d", len(out), blk.Length)
		}
	}
}

func TestEmptyBlockDefinition(t *testing.T) {
	raw := encodeToBytes(t, nil)
	if len(raw) != headerSize {
		t.Fatalf("empty block is %d bytes, want header only %d", len(raw), headerSize)
	}
	if raw[5]&FlagEmpty == 0 {
		t.Fatal("empty flag not set")
	}
	blk, err := Decode(bytes.NewReader(raw), 0)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out, err := blk.Inverse()
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("empty block decoded to %d bytes", len(out))
	}
}

func TestCorruptPrimaryIndex(t *testing.T) {
	src := []byte("banana")
	raw := encodeToBytes(t, src)
	// Set primary index to length (one past the last valid row).
	binary.LittleEndian.PutUint64(raw[16:24], uint64(len(src)))
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrPrimaryIndex {
		t.Fatalf("err = %v, want ErrPrimaryIndex", err)
	}
	// Huge primary index.
	binary.LittleEndian.PutUint64(raw[16:24], 1<<40)
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrPrimaryIndex {
		t.Fatalf("err = %v, want ErrPrimaryIndex", err)
	}
}

func TestCorruptChecksum(t *testing.T) {
	raw := encodeToBytes(t, []byte("banana"))
	raw[headerSize] ^= 0xFF // flip a payload bit
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrChecksum {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
}

func TestCorruptCounts(t *testing.T) {
	src := []byte("banana")
	raw := encodeToBytes(t, src)
	// Declared length larger than the actual payload: truncated read.
	binary.LittleEndian.PutUint64(raw[8:16], uint64(len(src))+10)
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrTruncated {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	// Declared length smaller: payload left over is parsed as the next
	// block header and fails the magic check.
	raw2 := encodeToBytes(t, src)
	binary.LittleEndian.PutUint64(raw2[8:16], uint64(len(src))-1)
	if _, err := Decode(bytes.NewReader(raw2), 1<<20); err != ErrChecksum {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
}

func TestTruncated(t *testing.T) {
	raw := encodeToBytes(t, []byte("banana"))
	for _, cut := range []int{3, headerSize - 1, headerSize, len(raw) - 1} {
		if _, err := Decode(bytes.NewReader(raw[:cut]), 1<<20); err != ErrTruncated {
			t.Fatalf("cut=%d: err = %v, want ErrTruncated", cut, err)
		}
	}
}

func TestBadMagicAndVersion(t *testing.T) {
	raw := encodeToBytes(t, []byte("banana"))
	raw[0] = 'X'
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrBadMagic {
		t.Fatalf("err = %v, want ErrBadMagic", err)
	}
	raw = encodeToBytes(t, []byte("banana"))
	raw[4] = 99
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrVersion {
		t.Fatalf("err = %v, want ErrVersion", err)
	}
	raw = encodeToBytes(t, []byte("banana"))
	raw[5] |= 0x80
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrFlags {
		t.Fatalf("err = %v, want ErrFlags", err)
	}
}

func TestEmptyFlagMismatch(t *testing.T) {
	raw := encodeToBytes(t, []byte("banana"))
	raw[5] |= FlagEmpty // non-empty block claiming to be empty
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrEmptyMismatch {
		t.Fatalf("err = %v, want ErrEmptyMismatch", err)
	}
	raw = encodeToBytes(t, nil)
	raw[5] &^= FlagEmpty // empty block without the flag
	if _, err := Decode(bytes.NewReader(raw), 1<<20); err != ErrEmptyMismatch {
		t.Fatalf("err = %v, want ErrEmptyMismatch", err)
	}
}

func TestDecodeBudget(t *testing.T) {
	src := make([]byte, 1000)
	raw := encodeToBytes(t, src)
	if _, err := Decode(bytes.NewReader(raw), 999); err != ErrOutputLimit {
		t.Fatalf("err = %v, want ErrOutputLimit", err)
	}
	blk, err := Decode(bytes.NewReader(raw), 1000)
	if err != nil {
		t.Fatalf("budget equal to length: %v", err)
	}
	out, err := blk.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, src) {
		t.Fatal("round trip mismatch at exact budget")
	}
}

func TestConcatenatedBlocks(t *testing.T) {
	var buf bytes.Buffer
	a := []byte("first block")
	b := []byte("second block")
	if err := Encode(&buf, a); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&buf, b); err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(buf.Bytes())
	for i, want := range [][]byte{a, b} {
		blk, err := Decode(r, 1<<20)
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		out, err := blk.Inverse()
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		if !bytes.Equal(out, want) {
			t.Fatalf("block %d = %q, want %q", i, out, want)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("%d unconsumed bytes", r.Len())
	}
}
