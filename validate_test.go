package compactidx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

// recBytes encodes one record directly, for crafting malicious images.
func recBytes(key, val string, link uint64) []byte {
	b := binary.AppendUvarint(nil, uint64(len(key)))
	b = append(b, key...)
	b = binary.AppendUvarint(b, uint64(len(val)))
	b = append(b, val...)
	if link == 0 {
		return append(b, linkNone)
	}
	b = append(b, linkMore)
	var lb [8]byte
	binary.LittleEndian.PutUint64(lb[:], link)
	return append(b, lb[:]...)
}

// dataBase returns the absolute offset of the data region for an image
// whose offset table has n entries.
func dataBase(nTable int) uint64 { return uint64(HeaderLen + nTable*tableEntrySize) }

// craftImage assembles a raw image from an offset table (absolute offsets)
// and raw data-region blobs.
func craftImage(table []uint64, blobs ...[]byte) []byte {
	var data []byte
	for _, b := range blobs {
		data = append(data, b...)
	}
	tableOff := uint64(HeaderLen)
	dataOff := dataBase(len(table))
	hdr := header{
		entryCount: uint32(len(table)),
		tableOff:   tableOff,
		dataOff:    dataOff,
		dataLen:    uint64(len(data)),
	}
	out := make([]byte, dataOff, dataOff+uint64(len(data)))
	hdr.encode(out)
	for i, off := range table {
		binary.LittleEndian.PutUint64(out[tableOff+uint64(i)*tableEntrySize:], off)
	}
	return append(out, data...)
}

func mustBuild(t *testing.T, kvs []KV, chunkSize int) []byte {
	t.Helper()
	img, err := Build(kvs, chunkSize)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return img
}

func mustLoad(t *testing.T, idx *Index, img []byte) {
	t.Helper()
	if err := idx.Load(img); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func sampleKVs(n int) []KV {
	kvs := make([]KV, n)
	for i := range kvs {
		kvs[i] = KV{Key: fmt.Sprintf("k%04d", i), Value: fmt.Sprintf("v%04d", i)}
	}
	return kvs
}

func TestEmptyFileIsNotValidIndex(t *testing.T) {
	for _, img := range [][]byte{nil, {}} {
		idx := NewIndex()
		if err := idx.Load(img); !errors.Is(err, ErrEmptyFile) {
			t.Fatalf("Load(len=%d) = %v, want ErrEmptyFile", len(img), err)
		}
	}
}

func TestEmptyImageIsValidEmptyIndex(t *testing.T) {
	// A properly encoded image with zero entries is valid; an empty file is not.
	img := mustBuild(t, nil, 0)
	idx := NewIndex()
	mustLoad(t, idx, img)
	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer v.Close()
	if _, found, err := v.Get("anything"); err != nil || found {
		t.Fatalf("Get on empty index = found=%v err=%v", found, err)
	}
}

func TestTruncatedHeader(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	idx := NewIndex()
	if err := idx.Load(img[:20]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("Load = %v, want ErrTruncated", err)
	}
}

func TestBadMagic(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	img[0] = 'X'
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("Load = %v, want ErrBadMagic", err)
	}
}

func TestBadVersion(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	binary.LittleEndian.PutUint16(img[4:6], 99)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("Load = %v, want ErrBadVersion", err)
	}
}

func TestMaliciousTableOffsetBeyondFile(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	binary.LittleEndian.PutUint64(img[HeaderLen:], uint64(len(img))+4096)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrOffsetOutOfFile) {
		t.Fatalf("Load = %v, want ErrOffsetOutOfFile", err)
	}
}

func TestDataRegionBeyondFile(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	dataLen := binary.LittleEndian.Uint64(img[32:40])
	binary.LittleEndian.PutUint64(img[32:40], dataLen+(1<<20))
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrOffsetOutOfFile) {
		t.Fatalf("Load = %v, want ErrOffsetOutOfFile", err)
	}
}

func TestIntegerOverflowDataEnd(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	binary.LittleEndian.PutUint64(img[24:32], ^uint64(0)-8) // dataOff near max
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Load = %v, want ErrOverflow", err)
	}
}

func TestIntegerOverflowTableEnd(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	binary.LittleEndian.PutUint64(img[16:24], ^uint64(0)-4) // tableOff near max
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Load = %v, want ErrOverflow", err)
	}
}

func TestDuplicateTableEntryRejected(t *testing.T) {
	base := dataBase(2)
	rec := recBytes("a", "1", 0)
	img := craftImage([]uint64{base, base}, rec)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrCycle) {
		t.Fatalf("Load = %v, want ErrCycle", err)
	}
}

func TestOverlappingRecordsRejected(t *testing.T) {
	base := dataBase(2)
	// Record A's key bytes are chosen so that parsing at base+1 yields a
	// small well-formed record B whose extent lies inside A's extent.
	a := []byte{0x04, 0x01, 'A', 0x01, 'B', 0x00, 0x00}
	img := craftImage([]uint64{base, base + 1}, a)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrRegionOverlap) {
		t.Fatalf("Load = %v, want ErrRegionOverlap", err)
	}
}

func TestSelfCycleRejected(t *testing.T) {
	base := dataBase(1)
	img := craftImage([]uint64{base}, recBytes("a", "x", base))
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrCycle) {
		t.Fatalf("Load = %v, want ErrCycle", err)
	}
}

func TestTwoRecordCycleRejected(t *testing.T) {
	base := dataBase(1)
	a := recBytes("a", "x", 0) // placeholder to learn len
	a = recBytes("a", "x", base+uint64(len(recBytes("", "y", 0))+0))
	// A links to B, B links back to A.
	b := recBytes("", "y", base)
	a = recBytes("a", "x", base+uint64(len(a)))
	// recompute with fixed point: len(a) does not depend on link value
	a = recBytes("a", "x", base+uint64(len(a)))
	img := craftImage([]uint64{base}, a, b)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrCycle) {
		t.Fatalf("Load = %v, want ErrCycle", err)
	}
}

func TestContinuationBeyondDataRegionRejected(t *testing.T) {
	base := dataBase(1)
	link := base + uint64(len(recBytes("a", "x", 1))) // == dataEnd, outside the data region
	a := recBytes("a", "x", link)
	img := craftImage([]uint64{base}, a)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrOffsetOutOfFile) {
		t.Fatalf("Load = %v, want ErrOffsetOutOfFile", err)
	}
}

func TestContinuationWithKeyRejected(t *testing.T) {
	base := dataBase(1)
	b := recBytes("b", "y", 0) // continuation must not carry a key
	a := recBytes("a", "x", base+uint64(len(recBytes("a", "x", 1))))
	img := craftImage([]uint64{base}, a, b)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrBadRecord) {
		t.Fatalf("Load = %v, want ErrBadRecord", err)
	}
}

func TestTablePointingAtContinuationRejected(t *testing.T) {
	base := dataBase(1)
	img := craftImage([]uint64{base}, recBytes("", "x", 0))
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrBadRecord) {
		t.Fatalf("Load = %v, want ErrBadRecord", err)
	}
}

func TestUnsortedKeysRejected(t *testing.T) {
	base := dataBase(2)
	a := recBytes("b", "1", 0)
	b := recBytes("a", "2", 0)
	img := craftImage([]uint64{base, base + uint64(len(a))}, a, b)
	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrUnsortedKeys) {
		t.Fatalf("Load = %v, want ErrUnsortedKeys", err)
	}
}

func TestLocalValidityIsNotEnough(t *testing.T) {
	// Record 0 is a fully valid chain on its own, but record 1 contains a
	// self-cycle. Local validation of record 0 passing must not make the
	// image trustworthy: the whole load fails and nothing is installed.
	base := dataBase(2)
	good := recBytes("a", "1", 0)
	badOff := base + uint64(len(good))
	bad := recBytes("b", "2", badOff) // self-cycle
	img := craftImage([]uint64{base, badOff}, good, bad)

	idx := NewIndex()
	if err := idx.Load(img); !errors.Is(err, ErrCycle) {
		t.Fatalf("Load = %v, want ErrCycle", err)
	}
	if _, err := idx.View(); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("View after rejected load = %v, want ErrNotLoaded", err)
	}
}

func TestRoundTripWithChunkedValues(t *testing.T) {
	var kvs []KV
	for i := 0; i < 300; i++ {
		val := fmt.Sprintf("v%04d", i)
		if i%7 == 0 { // force continuation chains
			val = fmt.Sprintf("%s-%0180d", val, i)
		}
		kvs = append(kvs, KV{Key: fmt.Sprintf("k%04d", i), Value: val})
	}
	img := mustBuild(t, kvs, 64)
	idx := NewIndex()
	mustLoad(t, idx, img)
	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer v.Close()
	for _, kv := range kvs {
		got, found, err := v.Get(kv.Key)
		if err != nil || !found {
			t.Fatalf("Get(%q) = found=%v err=%v", kv.Key, found, err)
		}
		if string(got) != kv.Value {
			t.Fatalf("Get(%q) = %q, want %q", kv.Key, got, kv.Value)
		}
	}
	if _, found, err := v.Get("k9999"); err != nil || found {
		t.Fatalf("Get(missing) = found=%v err=%v", found, err)
	}

	c, err := v.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	defer c.Close()
	n := 0
	for c.Next() {
		want := kvs[n]
		if c.Key() != want.Key || string(c.Value()) != want.Value {
			t.Fatalf("entry %d = %q/%q, want %q/%q", n, c.Key(), c.Value(), want.Key, want.Value)
		}
		n++
	}
	if err := c.Err(); err != nil {
		t.Fatalf("cursor err: %v", err)
	}
	if n != len(kvs) {
		t.Fatalf("iterated %d entries, want %d", n, len(kvs))
	}
}

func TestMappingRefusesUnvalidatedReads(t *testing.T) {
	img := mustBuild(t, sampleKVs(4), 0)
	idx := NewIndex()
	mustLoad(t, idx, img)
	m := idx.cur
	if _, err := m.read(uint64(len(img))+100, 1); !errors.Is(err, errUnvalidatedRead) {
		t.Fatalf("read beyond image = %v, want errUnvalidatedRead", err)
	}
	if _, err := m.read(0, uint64(len(img))); !errors.Is(err, errUnvalidatedRead) {
		t.Fatalf("read spanning regions = %v, want errUnvalidatedRead", err)
	}
}
