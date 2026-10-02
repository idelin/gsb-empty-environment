package roaring

import (
	"encoding/binary"
	"math/rand"
	"testing"
	"unsafe"
)

func roundTrip(t *testing.T, bm *Bitmap) *Bitmap {
	t.Helper()
	buf := bm.Serialize()
	if len(buf) != bm.SerializedSize() {
		t.Fatalf("Serialize len = %d, SerializedSize = %d", len(buf), bm.SerializedSize())
	}
	got, err := Deserialize(buf)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if !got.Equals(bm) {
		t.Fatalf("round trip mismatch: got %d values, want %d", got.Cardinality(), bm.Cardinality())
	}
	if got.Cardinality() != bm.Cardinality() {
		t.Fatalf("cardinality mismatch: %d != %d", got.Cardinality(), bm.Cardinality())
	}
	return got
}

func TestSerializeRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(3))

	empty := New()
	roundTrip(t, empty)

	single := New()
	single.Add(42)
	roundTrip(t, single)

	sparse := New()
	for i := 0; i < 500; i++ {
		sparse.Add(r.Uint32())
	}
	roundTrip(t, sparse)

	dense := New()
	dense.AddRange(0, 65536)
	dense.AddRange(1<<20, 1<<20+100000)
	roundTrip(t, dense)

	bitmapHeavy := New()
	for i := 0; i < 20000; i++ {
		bitmapHeavy.Add(uint32(5<<16 + r.Intn(65536)))
	}
	roundTrip(t, bitmapHeavy)

	mixed := buildMixed(r, 30)
	roundTrip(t, mixed)

	// Full key space tail.
	tail := New()
	tail.AddRange(1<<32-100000, 1<<32)
	roundTrip(t, tail)
}

func TestZeroCopyAliasesBuffer(t *testing.T) {
	// One bitmap container at key 3: payload offset = 16 + 8 = 24.
	bm := New()
	for i := 0; i < 5000; i++ {
		bm.Add(uint32(3<<16 + i*13))
	}
	buf := bm.Serialize()
	got, err := Deserialize(buf)
	if err != nil {
		t.Fatal(err)
	}
	bc, ok := got.entries[0].c.(*bitmapContainer)
	if !ok {
		t.Fatalf("expected bitmap container, got %T", got.entries[0].c)
	}
	want := uintptr(unsafe.Pointer(&buf[24]))
	if ptr := uintptr(unsafe.Pointer(&bc.words[0])); ptr != want {
		t.Fatalf("bitmap container not zero-copy: ptr=%x want=%x", ptr, want)
	}
	// Mutating the buffer must be visible through the bitmap (true aliasing).
	binary.LittleEndian.PutUint64(buf[24:], 1) // word 0 -> bit 0 set
	if !got.Contains(3 << 16) {
		t.Fatal("buffer mutation not visible: deserialization copied data")
	}
}

func TestZeroCopyArrayAndRun(t *testing.T) {
	// Array container at key 0.
	a := New()
	for i := 0; i < 100; i++ {
		a.Add(uint32(i * 500))
	}
	buf := a.Serialize()
	got, err := Deserialize(buf)
	if err != nil {
		t.Fatal(err)
	}
	ac, ok := got.entries[0].c.(*arrayContainer)
	if !ok {
		t.Fatalf("expected array container, got %T", got.entries[0].c)
	}
	if ptr, want := uintptr(unsafe.Pointer(&ac.vals[0])), uintptr(unsafe.Pointer(&buf[24])); ptr != want {
		t.Fatalf("array container not zero-copy: ptr=%x want=%x", ptr, want)
	}

	// Run container at key 0: payload = uint32 count + intervals at offset 28.
	rc := New()
	rc.AddRange(10, 1000)
	buf = rc.Serialize()
	got, err = Deserialize(buf)
	if err != nil {
		t.Fatal(err)
	}
	rc2, ok := got.entries[0].c.(*runContainer)
	if !ok {
		t.Fatalf("expected run container, got %T", got.entries[0].c)
	}
	if ptr, want := uintptr(unsafe.Pointer(&rc2.iv[0])), uintptr(unsafe.Pointer(&buf[28])); ptr != want {
		t.Fatalf("run container not zero-copy: ptr=%x want=%x", ptr, want)
	}
}

func TestDeserializeCopyOnWrite(t *testing.T) {
	bm := New()
	bm.AddRange(0, 1000)
	buf := bm.Serialize()
	got, err := Deserialize(buf)
	if err != nil {
		t.Fatal(err)
	}
	before := bm.Serialize()
	// Mutating the zero-copy bitmap must not corrupt the shared buffer.
	got.Add(5000)
	got.AddRange(2000, 3000)
	if !bm.SerializeEqualsBytes(before) {
		t.Fatal("mutation of zero-copy bitmap leaked into source buffer")
	}
	if !got.Contains(5000) || !got.Contains(2500) || !got.Contains(500) {
		t.Fatal("zero-copy bitmap lost data after copy-on-write")
	}
}

// SerializeEqualsBytes reports whether the bitmap serializes to exactly buf.
func (b *Bitmap) SerializeEqualsBytes(buf []byte) bool {
	got := b.Serialize()
	if len(got) != len(buf) {
		return false
	}
	for i := range got {
		if got[i] != buf[i] {
			return false
		}
	}
	return true
}

func TestDeserializeTruncated(t *testing.T) {
	bm := buildMixed(rand.New(rand.NewSource(5)), 10)
	buf := bm.Serialize()
	for n := 0; n < len(buf); n++ {
		if _, err := Deserialize(buf[:n]); err == nil {
			t.Fatalf("truncated buffer of %d/%d bytes decoded without error", n, len(buf))
		}
	}
}

func TestDeserializeCorrupt(t *testing.T) {
	bm := New()
	bm.AddRange(0, 5000)
	for i := 0; i < 100; i++ {
		bm.Add(uint32(2<<16 + i*7))
	}
	good := bm.Serialize()

	mutate := func(fn func([]byte)) []byte {
		buf := append([]byte(nil), good...)
		fn(buf)
		return buf
	}
	cases := map[string][]byte{
		"bad magic": mutate(func(b []byte) { b[0] ^= 0xff }),
		"bad type":  mutate(func(b []byte) { b[16+2] = 99 }),
		"zero cardinality": mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[16+4:], 0)
		}),
		"huge cardinality": mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[16+4:], 70000)
		}),
		"huge container count": mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[8:], 0xffffffff)
		}),
		"count overflow": mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[8:], 1<<30)
		}),
		"unsorted keys": nil, // built below
		"run count zero": mutate(func(b []byte) {
			// first container is a run container (AddRange 0..5000)
			binary.LittleEndian.PutUint32(b[24:], 0)
		}),
		"run count huge": mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[24:], 0xffffffff)
		}),
		"cardinality mismatch": mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[16+4:], 1234)
		}),
	}
	// Unsorted keys: swap the two container headers.
	unsorted := append([]byte(nil), good...)
	copy(unsorted[16:24], good[24:32])
	copy(unsorted[24:32], good[16:24])
	cases["unsorted keys"] = unsorted

	for name, buf := range cases {
		if _, err := Deserialize(buf); err == nil {
			t.Fatalf("%s: expected error, got none", name)
		}
	}
}

func TestDeserializeUnsortedArray(t *testing.T) {
	bm := New()
	bm.Add(10)
	bm.Add(20)
	bm.Add(30)
	buf := bm.Serialize()
	// Corrupt the array payload to be non-increasing.
	binary.LittleEndian.PutUint16(buf[24:], 30)
	binary.LittleEndian.PutUint16(buf[26:], 10)
	if _, err := Deserialize(buf); err == nil {
		t.Fatal("unsorted array payload accepted")
	}
}

func TestDeserializeOverlappingRuns(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100)
	bm.AddRange(200, 300)
	buf := bm.Serialize()
	// Rewrite the second run to overlap the first.
	binary.LittleEndian.PutUint16(buf[28+4:], 50) // start of run 2 -> 50
	if _, err := Deserialize(buf); err == nil {
		t.Fatal("overlapping runs accepted")
	}
}

func TestDeserializeRandomGarbage(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		n := r.Intn(512)
		buf := make([]byte, n)
		r.Read(buf)
		_, _ = Deserialize(buf) // must not panic
	}
}

func TestForwardCompatibility(t *testing.T) {
	bm := New()
	bm.AddRange(0, 1000)
	bm.Add(70000)
	buf := bm.Serialize()

	// Trailing bytes (future extensions) must be ignored.
	extended := append(append([]byte(nil), buf...), make([]byte, 64)...)
	for i := 16; i < len(extended); i++ { // junk in the trailer
		if i >= len(buf) {
			extended[i] = byte(i)
		}
	}
	got, err := Deserialize(extended)
	if err != nil {
		t.Fatalf("trailing bytes rejected: %v", err)
	}
	if !got.Equals(bm) {
		t.Fatal("bitmap changed by trailing bytes")
	}

	// Unknown flags and reserved fields must be ignored.
	flagged := append([]byte(nil), buf...)
	flagged[6] = 0xff
	flagged[7] = 0xff
	binary.LittleEndian.PutUint32(flagged[12:], 0xdeadbeef)
	if _, err := Deserialize(flagged); err != nil {
		t.Fatalf("unknown flags rejected: %v", err)
	}
}

func TestBackwardCompatibility(t *testing.T) {
	bm := New()
	bm.AddRange(0, 1000)
	buf := bm.Serialize()

	// Version 0 (older) must still decode with the v1 reader.
	old := append([]byte(nil), buf...)
	binary.LittleEndian.PutUint16(old[4:], 0)
	got, err := Deserialize(old)
	if err != nil {
		t.Fatalf("older version rejected: %v", err)
	}
	if !got.Equals(bm) {
		t.Fatal("older version decoded differently")
	}

	// Version 2 (newer than supported) must be rejected cleanly.
	newer := append([]byte(nil), buf...)
	binary.LittleEndian.PutUint16(newer[4:], formatVersion+1)
	if _, err := Deserialize(newer); err == nil {
		t.Fatal("newer version accepted")
	}
}

func TestSerializedDeterministic(t *testing.T) {
	r := rand.New(rand.NewSource(13))
	bm := buildMixed(r, 10)
	a := bm.Serialize()
	b := bm.Serialize()
	if len(a) != len(b) {
		t.Fatal("serialized size not deterministic")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("serialization not deterministic")
		}
	}
}
