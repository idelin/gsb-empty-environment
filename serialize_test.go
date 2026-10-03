package cbitmap

import (
	"encoding/binary"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// buildMixedBitmap creates a bitmap exercising all three container types.
func buildMixedBitmap(t *testing.T) *Bitmap {
	t.Helper()
	bm := New()
	rng := rand.New(rand.NewSource(1234))
	// array containers: sparse ids
	for i := 0; i < 3000; i++ {
		bm.Add(uint32(rng.Intn(1 << 24)))
	}
	// bitmap containers: dense random fills
	for base := uint32(3 << 20); base < 3<<20+8*65536; base += 65536 {
		for i := 0; i < 20000; i++ {
			bm.Add(base + uint32(rng.Intn(65536)))
		}
	}
	// run containers: contiguous ranges
	bm.AddRange(10<<20, 10<<20+100000)
	bm.AddRange(20<<20, 20<<20+65536)
	// boundary values
	bm.Add(0)
	bm.Add(1<<32 - 1)
	return bm
}

func assertSameContents(t *testing.T, a, b *Bitmap, ctx string) {
	t.Helper()
	if a.Cardinality() != b.Cardinality() {
		t.Fatalf("%s: cardinality %d != %d", ctx, a.Cardinality(), b.Cardinality())
	}
	sa, sb := a.Stats(), b.Stats()
	if sa.Containers != sb.Containers {
		t.Fatalf("%s: containers %d != %d", ctx, sa.Containers, sb.Containers)
	}
	ia, ib := a.Iterator(), b.Iterator()
	for {
		va, oka := ia.Next()
		vb, okb := ib.Next()
		if oka != okb {
			t.Fatalf("%s: iteration length mismatch", ctx)
		}
		if !oka {
			break
		}
		if va != vb {
			t.Fatalf("%s: value mismatch %d != %d", ctx, va, vb)
		}
	}
}

func TestRoundTripCopy(t *testing.T) {
	bm := buildMixedBitmap(t)
	buf := bm.MarshalBinary()
	if len(buf) != bm.SerializedSize() {
		t.Fatalf("SerializedSize=%d len=%d", bm.SerializedSize(), len(buf))
	}
	back, err := UnmarshalBinary(buf)
	if err != nil {
		t.Fatal(err)
	}
	assertSameContents(t, bm, back, "copy roundtrip")
	// result must be independent of buf
	for i := range buf {
		buf[i] = 0
	}
	if back.Cardinality() != bm.Cardinality() {
		t.Fatal("copied bitmap must not alias the buffer")
	}
}

func TestRoundTripEmpty(t *testing.T) {
	bm := New()
	buf := bm.MarshalBinary()
	if len(buf) != headerSize {
		t.Fatalf("empty serialized size=%d", len(buf))
	}
	back, err := UnmarshalZeroCopy(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !back.IsEmpty() {
		t.Fatal("expected empty bitmap")
	}
}

func TestZeroCopySharesMemory(t *testing.T) {
	bm := New()
	// force a bitmap container at key 0
	for i := 0; i < 20000; i++ {
		bm.Add(uint32(i * 3 % 65536))
	}
	buf := bm.MarshalBinary()
	zc, err := UnmarshalZeroCopy(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !zc.Frozen() {
		t.Fatal("zero-copy bitmap must be frozen")
	}
	// locate the bitmap container payload via its descriptor
	off := binary.LittleEndian.Uint64(buf[headerSize+12:])
	if buf[headerSize+2] != bitmapContainerID {
		t.Fatalf("expected bitmap container, got type %d", buf[headerSize+2])
	}
	// pick a value not present, flip its bit in the buffer, observe change
	var target uint32
	for v := uint32(0); v < 65536; v++ {
		if !zc.Contains(v) {
			target = v
			break
		}
	}
	byteIdx := int(off) + int(target>>3)
	bit := uint(target & 7)
	buf[byteIdx] |= 1 << bit
	if !zc.Contains(target) {
		t.Fatal("zero-copy bitmap does not share memory with buffer")
	}
	buf[byteIdx] &^= 1 << bit
	if zc.Contains(target) {
		t.Fatal("zero-copy bitmap does not share memory with buffer (clear)")
	}
}

func TestZeroCopyThawIsolatesBuffer(t *testing.T) {
	bm := buildMixedBitmap(t)
	buf := bm.MarshalBinary()
	snapshot := make([]byte, len(buf))
	copy(snapshot, buf)
	zc, err := UnmarshalZeroCopy(buf)
	if err != nil {
		t.Fatal(err)
	}
	cardBefore := zc.Cardinality()
	// mutate the frozen bitmap: must copy-on-write, buffer untouched
	zc.Add(1 << 30)
	zc.AddRange(30<<20, 30<<20+50000)
	if zc.Frozen() {
		t.Fatal("bitmap should have thawed after mutation")
	}
	if zc.Cardinality() <= cardBefore {
		t.Fatal("mutation had no effect")
	}
	for i := range buf {
		if buf[i] != snapshot[i] {
			t.Fatalf("buffer mutated at byte %d", i)
		}
	}
	// reparse the untouched buffer: original contents intact
	again, err := UnmarshalZeroCopy(buf)
	if err != nil {
		t.Fatal(err)
	}
	assertSameContents(t, bm, again, "buffer after thaw")
}

func TestCorruptInputs(t *testing.T) {
	bm := buildMixedBitmap(t)
	good := bm.MarshalBinary()

	cases := []struct {
		name string
		mut  func() []byte
	}{
		{"empty buffer", func() []byte { return nil }},
		{"truncated header", func() []byte { return good[:8] }},
		{"truncated descriptors", func() []byte { return good[:headerSize+3] }},
		{"truncated payload", func() []byte { return good[:len(good)-7] }},
		{"bad magic", func() []byte {
			b := clone(good)
			b[0] ^= 0xff
			return b
		}},
		{"version zero", func() []byte {
			b := clone(good)
			binary.LittleEndian.PutUint16(b[4:], 0)
			return b
		}},
		{"future version", func() []byte {
			b := clone(good)
			binary.LittleEndian.PutUint16(b[4:], formatVersion+1)
			return b
		}},
		{"huge container count", func() []byte {
			b := clone(good)
			binary.LittleEndian.PutUint32(b[8:], 0xffffffff)
			return b
		}},
		{"unknown container type", func() []byte {
			b := clone(good)
			b[headerSize+2] = 99
			return b
		}},
		{"offset beyond buffer", func() []byte {
			b := clone(good)
			binary.LittleEndian.PutUint64(b[headerSize+12:], uint64(len(b))+4096)
			return b
		}},
		{"offset near uint64 max", func() []byte {
			b := clone(good)
			binary.LittleEndian.PutUint64(b[headerSize+12:], 0xfffffffffffffff0)
			return b
		}},
		{"misaligned offset", func() []byte {
			b := clone(good)
			binary.LittleEndian.PutUint64(b[headerSize+12:], 33)
			return b
		}},
		{"zero array cardinality", func() []byte {
			b := clone(good)
			// find an array container descriptor
			n := int(binary.LittleEndian.Uint32(b[8:]))
			for i := 0; i < n; i++ {
				d := b[headerSize+i*descSize:]
				if d[2] == arrayContainerID {
					binary.LittleEndian.PutUint32(d[4:], 0)
					return b
				}
			}
			t.Fatal("no array container found")
			return nil
		}},
		{"bitmap cardinality mismatch", func() []byte {
			b := clone(good)
			n := int(binary.LittleEndian.Uint32(b[8:]))
			for i := 0; i < n; i++ {
				d := b[headerSize+i*descSize:]
				if d[2] == bitmapContainerID {
					c := binary.LittleEndian.Uint32(d[4:])
					binary.LittleEndian.PutUint32(d[4:], c+1)
					return b
				}
			}
			t.Fatal("no bitmap container found")
			return nil
		}},
		{"unsorted array payload", func() []byte {
			b := clone(good)
			n := int(binary.LittleEndian.Uint32(b[8:]))
			for i := 0; i < n; i++ {
				d := b[headerSize+i*descSize:]
				if d[2] == arrayContainerID && binary.LittleEndian.Uint32(d[4:]) >= 2 {
					off := int(binary.LittleEndian.Uint64(d[12:]))
					// swap first two values
					v0 := binary.LittleEndian.Uint16(b[off:])
					v1 := binary.LittleEndian.Uint16(b[off+2:])
					binary.LittleEndian.PutUint16(b[off:], v1)
					binary.LittleEndian.PutUint16(b[off+2:], v0)
					return b
				}
			}
			t.Fatal("no suitable array container found")
			return nil
		}},
		{"overlapping runs", func() []byte {
			b := clone(good)
			n := int(binary.LittleEndian.Uint32(b[8:]))
			for i := 0; i < n; i++ {
				d := b[headerSize+i*descSize:]
				if d[2] == runContainerID {
					// rewrite payload with two overlapping runs and fix meta/card
					off := int(binary.LittleEndian.Uint64(d[12:]))
					binary.LittleEndian.PutUint32(d[8:], 2) // meta = 2 runs
					binary.LittleEndian.PutUint32(d[4:], 30)
					binary.LittleEndian.PutUint16(b[off:], 0)    // start 0
					binary.LittleEndian.PutUint16(b[off+2:], 19) // len 20
					binary.LittleEndian.PutUint16(b[off+4:], 10) // start 10 (overlap)
					binary.LittleEndian.PutUint16(b[off+6:], 9)  // len 10
					return b
				}
			}
			t.Fatal("no run container found")
			return nil
		}},
	}
	for _, tc := range cases {
		for _, zc := range []bool{false, true} {
			buf := tc.mut()
			var err error
			if zc {
				_, err = UnmarshalZeroCopy(buf)
			} else {
				_, err = UnmarshalBinary(buf)
			}
			if err == nil {
				t.Fatalf("%s (zeroCopy=%v): expected error, got nil", tc.name, zc)
			}
		}
	}
}

func TestForwardCompatibility(t *testing.T) {
	bm := buildMixedBitmap(t)
	good := bm.MarshalBinary()

	// unknown flag bits are ignored
	b := clone(good)
	binary.LittleEndian.PutUint16(b[6:], 0xBEEF)
	back, err := UnmarshalZeroCopy(b)
	if err != nil {
		t.Fatalf("unknown flags must be tolerated: %v", err)
	}
	assertSameContents(t, bm, back, "unknown flags")

	// trailing extension bytes are ignored
	ext := append(clone(good), 0xDE, 0xAD, 0xBE, 0xEF)
	back, err = UnmarshalBinary(ext)
	if err != nil {
		t.Fatalf("trailing bytes must be tolerated: %v", err)
	}
	assertSameContents(t, bm, back, "trailing bytes")
}

func TestMisalignedBufferFallsBackToCopy(t *testing.T) {
	bm := buildMixedBitmap(t)
	good := bm.MarshalBinary()
	// place the payload at an odd address
	shifted := make([]byte, len(good)+1)
	copy(shifted[1:], good)
	back, err := UnmarshalZeroCopy(shifted[1:])
	if err != nil {
		t.Fatal(err)
	}
	assertSameContents(t, bm, back, "misaligned fallback")
}

func TestMmapRoundTrip(t *testing.T) {
	bm := buildMixedBitmap(t)
	dir, err := ioutil.TempDir("", "cbitmap")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "bm.bin")
	if err := ioutil.WriteFile(path, bm.MarshalBinary(), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := MmapFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer Munmap(data)
	zc, err := UnmarshalZeroCopy(data)
	if err != nil {
		t.Fatal(err)
	}
	assertSameContents(t, bm, zc, "mmap zero-copy")
	// ops on mmap-backed bitmap
	other := New()
	other.AddRange(10<<20, 10<<20+50000)
	inter := And(zc, other)
	if inter.Cardinality() != 50000 {
		t.Fatalf("intersection cardinality=%d want 50000", inter.Cardinality())
	}
}

func clone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
