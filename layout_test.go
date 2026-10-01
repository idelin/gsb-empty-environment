package cidx

import (
	"encoding/binary"
	"math"
	"testing"
)

func buildIndex(t *testing.T, kv ...[2]string) []byte {
	t.Helper()
	var b Builder
	for _, p := range kv {
		if err := b.Add([]byte(p[0]), []byte(p[1])); err != nil {
			t.Fatalf("builder.Add(%q): %v", p[0], err)
		}
	}
	data, err := b.Bytes()
	if err != nil {
		t.Fatalf("builder.Bytes: %v", err)
	}
	return data
}

func mustCheck(t *testing.T, data []byte) *Layout {
	t.Helper()
	lay, err := Check(data)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return lay
}

func wantErr(t *testing.T, data []byte, want error) {
	t.Helper()
	if _, err := Check(data); err != want {
		t.Fatalf("Check: got %v, want %v", err, want)
	}
}

func putU32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
func putU64(b []byte, off int, v uint64) { binary.LittleEndian.PutUint64(b[off:], v) }

// Header field offsets.
const (
	offEntryCount  = 12
	offTableOffset = 16
	offDataOffset  = 24
	offDataLength  = 32
)

var sampleEntries = [][2]string{
	{"apple", "fruit"},
	{"banana", "yellow"},
	{"cherry", "red"},
	{"date", "sweet"},
	{"elderberry", "tart"},
}

func TestRoundTrip(t *testing.T) {
	data := buildIndex(t, sampleEntries...)
	s := NewStore()
	if err := s.Load(data); err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, err := s.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer v.Close()
	for _, p := range sampleEntries {
		got, err := v.Query([]byte(p[0]))
		if err != nil {
			t.Fatalf("Query(%q): %v", p[0], err)
		}
		if string(got) != p[1] {
			t.Fatalf("Query(%q) = %q, want %q", p[0], got, p[1])
		}
	}
	if _, err := v.Query([]byte("zzz")); err != ErrNotFound {
		t.Fatalf("Query(zzz): got %v, want ErrNotFound", err)
	}
}

func TestOccupiedBytes(t *testing.T) {
	data := buildIndex(t, sampleEntries...)
	want := HeaderSize + (len(sampleEntries)+1)*offsetEntrySize
	for _, p := range sampleEntries {
		want += recordSize([]byte(p[0]), []byte(p[1]))
	}
	if len(data) != want {
		t.Fatalf("occupied bytes = %d, want %d", len(data), want)
	}
	lay := mustCheck(t, data)
	var validated uint64
	for _, r := range lay.Regions {
		validated += r.End - r.Start
	}
	if validated != uint64(len(data)) {
		t.Fatalf("validated %d bytes of %d", validated, len(data))
	}
	t.Logf("file=%dB header=%dB offtable=%dB data=%dB entries=%d",
		len(data), HeaderSize, (len(sampleEntries)+1)*offsetEntrySize,
		len(data)-HeaderSize-(len(sampleEntries)+1)*offsetEntrySize, len(sampleEntries))
}

func TestEmptyFileRejected(t *testing.T) {
	wantErr(t, []byte{}, ErrEmptyFile)
	wantErr(t, make([]byte, HeaderSize-1), ErrTooShort)
}

func TestEmptyIndexIsValidNonEmptyFile(t *testing.T) {
	data := buildIndex(t)
	if len(data) == 0 {
		t.Fatal("empty index must still occupy bytes")
	}
	mustCheck(t, data)
	s := NewStore()
	if err := s.Load(data); err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, _ := s.View()
	defer v.Close()
	if _, err := v.Query([]byte("a")); err != ErrNotFound {
		t.Fatalf("Query on empty index: got %v, want ErrNotFound", err)
	}
}

func TestBadMagicAndVersion(t *testing.T) {
	data := buildIndex(t, sampleEntries...)
	data[0] = 'X'
	wantErr(t, data, ErrBadMagic)

	data = buildIndex(t, sampleEntries...)
	putU32(data, 8, 99)
	wantErr(t, data, ErrUnsupportedVersion)
}

func TestTruncatedAndTrailing(t *testing.T) {
	data := buildIndex(t, sampleEntries...)
	wantErr(t, data[:len(data)-1], ErrTruncated)
	wantErr(t, data[:HeaderSize+4], ErrTruncated)
	wantErr(t, append(append([]byte(nil), data...), 0x00), ErrTrailingData)
}

func TestMaliciousOffsetBeyondFile(t *testing.T) {
	// Last offset far beyond the data region.
	data := buildIndex(t, sampleEntries...)
	putU32(data, HeaderSize+len(sampleEntries)*offsetEntrySize, 0xFFFFFF00)
	wantErr(t, data, ErrOffsetOutOfRange)

	// Middle offset past dataLength.
	data = buildIndex(t, sampleEntries...)
	dl := binary.LittleEndian.Uint64(data[offDataLength:])
	putU32(data, HeaderSize+offsetEntrySize, uint32(dl)+100)
	wantErr(t, data, ErrOffsetOutOfRange)

	// First offset not anchored at 0.
	data = buildIndex(t, sampleEntries...)
	putU32(data, HeaderSize, 4)
	wantErr(t, data, ErrOffsetOutOfRange)
}

func TestOffsetCycle(t *testing.T) {
	data := buildIndex(t, sampleEntries...)
	// offsets[2] jumps backwards: a back-edge in the offset graph.
	putU32(data, HeaderSize+2*offsetEntrySize, 0)
	wantErr(t, data, ErrOffsetCycle)
}

func TestRegionOverlap(t *testing.T) {
	// Data region overlaps the offset table.
	data := buildIndex(t, sampleEntries...)
	putU64(data, offDataOffset, uint64(HeaderSize))
	putU64(data, offDataLength, uint64(len(data))-HeaderSize)
	wantErr(t, data, ErrRegionOverlap)

	// Offset table overlaps the header.
	data = buildIndex(t, sampleEntries...)
	putU64(data, offTableOffset, 8)
	wantErr(t, data, ErrRegionOverlap)
}

func TestIntegerOverflow(t *testing.T) {
	// dataOffset + dataLength wraps.
	data := buildIndex(t, sampleEntries...)
	putU64(data, offDataOffset, math.MaxUint64-3)
	wantErr(t, data, ErrOverflow)

	// tableOffset + tableBytes wraps.
	data = buildIndex(t, sampleEntries...)
	putU64(data, offTableOffset, math.MaxUint64-1)
	wantErr(t, data, ErrOverflow)

	// entryCount+1 wraps.
	data = buildIndex(t, sampleEntries...)
	putU32(data, offEntryCount, math.MaxUint32)
	wantErr(t, data, ErrOverflow)
}

func TestRecordMalformed(t *testing.T) {
	// Collapse record 0 to zero length: offsets[1] = offsets[0] = 0.
	data := buildIndex(t, sampleEntries...)
	putU32(data, HeaderSize+offsetEntrySize, 0)
	wantErr(t, data, ErrRecordMalformed)
}

func TestLocalValidButGlobalInvalid(t *testing.T) {
	// Every record parses fine locally, but the global key ordering is
	// violated: local validation must not imply global trust.
	data := buildIndex(t, [][2]string{
		{"k1", "v1"}, {"k2", "v2"}, {"k3", "v3"},
	}...)
	lay := mustCheck(t, data)
	rec1 := lay.DataOffset + uint64(lay.Offsets[1])
	// Record 1 key starts after its 1-byte uvarint length.
	data[rec1+1] = 'k'
	data[rec1+2] = '0' // "k2" -> "k0", now k1 > k0
	wantErr(t, data, ErrKeyOrder)
}

// buildWithGap returns a valid index that has a 16-byte unvalidated gap
// between the offset table and the data region, plus the gap bounds.
func buildWithGap(t *testing.T) (data []byte, gapStart, gapEnd uint64) {
	t.Helper()
	base := buildIndex(t, sampleEntries...)
	tableEnd := uint64(HeaderSize + (len(sampleEntries)+1)*offsetEntrySize)
	dataOffset := binary.LittleEndian.Uint64(base[offDataOffset:])
	if dataOffset != tableEnd {
		t.Fatalf("builder left a gap: dataOffset=%d tableEnd=%d", dataOffset, tableEnd)
	}
	const gap = 16
	out := make([]byte, 0, len(base)+gap)
	out = append(out, base[:tableEnd]...)
	out = append(out, make([]byte, gap)...)
	out = append(out, base[tableEnd:]...)
	putU64(out, offDataOffset, dataOffset+gap)
	return out, tableEnd, tableEnd + gap
}

func TestGuardedReaderRefusesUnvalidated(t *testing.T) {
	data, gapStart, gapEnd := buildWithGap(t)
	lay := mustCheck(t, data) // gap is legal padding, but unvalidated
	g := NewGuarded(data, lay)

	if _, err := g.ReadAt(gapStart, 1); err != ErrUnvalidatedRead {
		t.Fatalf("read in gap: got %v, want ErrUnvalidatedRead", err)
	}
	if _, err := g.ReadAt(gapEnd-1, 2); err != ErrUnvalidatedRead {
		t.Fatalf("read straddling gap: got %v, want ErrUnvalidatedRead", err)
	}
	if _, err := g.ReadAt(uint64(len(data)), 1); err != ErrOutOfBounds {
		t.Fatalf("read past EOF: got %v, want ErrOutOfBounds", err)
	}
	if _, err := g.ReadAt(math.MaxUint64-1, 4); err != ErrOverflow {
		t.Fatalf("wrapping read: got %v, want ErrOverflow", err)
	}
	if _, err := g.ReadAt(0, HeaderSize); err != nil {
		t.Fatalf("read of validated header: %v", err)
	}

	// Queries still work and never touch the gap.
	s := NewStore()
	if err := s.Load(data); err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, _ := s.View()
	defer v.Close()
	got, err := v.Query([]byte("cherry"))
	if err != nil || string(got) != "red" {
		t.Fatalf("Query(cherry) = %q, %v", got, err)
	}
}
