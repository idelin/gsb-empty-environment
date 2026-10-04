package blockmatch

import (
	"bytes"
	"reflect"
	"testing"
)

const (
	testWeakBits  = 4 // tiny fingerprint: collisions are guaranteed
	testBlockSize = 8
)

func testIndex(t *testing.T, source []byte, blockSizes ...int) *Index {
	t.Helper()
	idx, err := NewIndex(source, testWeakBits)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	for _, bs := range blockSizes {
		if err := idx.AddBlockSize(bs); err != nil {
			t.Fatalf("AddBlockSize(%d): %v", bs, err)
		}
	}
	return idx
}

func runOneShot(t *testing.T, idx *Index, blockSize int, target []byte) []Segment {
	t.Helper()
	m, err := NewMatcher(idx, blockSize)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	segs := m.Feed(target)
	return append(segs, m.Flush()...)
}

// assertCoverage verifies the output tiles the target exactly: ordered,
// non-overlapping, positive-length segments covering [0, total).
func assertCoverage(t *testing.T, segs []Segment, total int64) {
	t.Helper()
	var pos int64
	for i, s := range segs {
		if s.Length <= 0 {
			t.Fatalf("segment %d has non-positive length %d", i, s.Length)
		}
		if s.Offset != pos {
			t.Fatalf("segment %d starts at %d, want %d (overlap or hole)", i, s.Offset, pos)
		}
		pos += s.Length
	}
	if pos != total {
		t.Fatalf("segments cover %d bytes, target has %d", pos, total)
	}
}

// assertMatchContent verifies every reported match against the real bytes.
func assertMatchContent(t *testing.T, idx *Index, target []byte, segs []Segment) {
	t.Helper()
	for i, s := range segs {
		if s.Kind != Match {
			continue
		}
		got := target[s.Offset : s.Offset+s.Length]
		want := idx.source[s.SourceOffset : s.SourceOffset+s.Length]
		if !bytes.Equal(got, want) {
			t.Fatalf("segment %d claims match but content differs", i)
		}
	}
}

func weakOf(block []byte, mask uint32) uint32 {
	var h rollingHash
	for _, b := range block {
		h.append(b)
	}
	return h.value() & mask
}

func TestRepeatedBlocks(t *testing.T) {
	var source []byte
	for i := 0; i < 4; i++ {
		source = append(source, bytes.Repeat([]byte{byte('A' + i)}, testBlockSize)...)
	}
	idx := testIndex(t, source, testBlockSize)

	block := func(i int) []byte { return source[i*testBlockSize : (i+1)*testBlockSize] }
	noise := []byte("xyz")
	var target []byte
	target = append(target, block(1)...)
	target = append(target, noise...)
	target = append(target, block(1)...) // repeated block
	target = append(target, block(3)...)
	target = append(target, block(0)...)
	target = append(target, noise...)

	segs := runOneShot(t, idx, testBlockSize, target)
	assertCoverage(t, segs, int64(len(target)))
	assertMatchContent(t, idx, target, segs)

	var matches, gaps int
	for _, s := range segs {
		if s.Kind == Match {
			matches++
		} else {
			gaps++
		}
	}
	if matches != 4 || gaps != 2 {
		t.Fatalf("got %d matches, %d gaps; want 4 matches, 2 gaps; segs=%v", matches, gaps, segs)
	}
	// The repeated block must resolve to the same source offset both times.
	if segs[0].SourceOffset != segs[2].SourceOffset {
		t.Fatalf("repeated block resolved to %d and %d", segs[0].SourceOffset, segs[2].SourceOffset)
	}
}

func TestCollisionRejected(t *testing.T) {
	orig := []byte("AAAAAAAA")
	idx := testIndex(t, orig, testBlockSize)
	mask := uint32(1<<testWeakBits - 1)

	// Craft a block with the same weak fingerprint but different content.
	var evil []byte
	for b := 0; b < 256; b++ {
		cand := []byte{'B', 'B', 'B', 'B', 'B', 'B', 'B', byte(b)}
		if !bytes.Equal(cand, orig) && weakOf(cand, mask) == weakOf(orig, mask) {
			evil = cand
			break
		}
	}
	if evil == nil {
		t.Fatal("could not craft a colliding block")
	}

	segs := runOneShot(t, idx, testBlockSize, evil)
	assertCoverage(t, segs, int64(len(evil)))
	for _, s := range segs {
		if s.Kind == Match {
			t.Fatalf("colliding block was reported as match: %+v", s)
		}
	}

	// Sanity: the genuine block does match.
	segs = runOneShot(t, idx, testBlockSize, orig)
	if len(segs) != 1 || segs[0].Kind != Match {
		t.Fatalf("genuine block did not match: %v", segs)
	}
}

func TestShortTail(t *testing.T) {
	source := []byte("0123456789ABCDEF") // two full blocks
	idx := testIndex(t, source, testBlockSize)

	// Full block + 3-byte tail shorter than one window.
	target := append(append([]byte{}, source[:testBlockSize]...), source[testBlockSize:testBlockSize+3]...)
	segs := runOneShot(t, idx, testBlockSize, target)
	assertCoverage(t, segs, int64(len(target)))
	assertMatchContent(t, idx, target, segs)
	want := []Segment{
		{Kind: Match, Offset: 0, Length: 8, SourceOffset: 0},
		{Kind: Gap, Offset: 8, Length: 3},
	}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("got %v, want %v", segs, want)
	}

	// Stream shorter than one whole window: a single gap.
	segs = runOneShot(t, idx, testBlockSize, []byte("hello"))
	assertCoverage(t, segs, 5)
	if len(segs) != 1 || segs[0].Kind != Gap {
		t.Fatalf("got %v, want one gap", segs)
	}
}

func TestAllZeroBytes(t *testing.T) {
	source := make([]byte, 32) // four identical zero blocks
	idx := testIndex(t, source, testBlockSize)

	target := make([]byte, 20) // 2 full blocks + 4-byte tail
	segs := runOneShot(t, idx, testBlockSize, target)
	assertCoverage(t, segs, int64(len(target)))
	assertMatchContent(t, idx, target, segs)
	if len(segs) != 3 || segs[0].Kind != Match || segs[1].Kind != Match || segs[2].Kind != Gap {
		t.Fatalf("unexpected tiling: %v", segs)
	}
	if segs[2].Length != 4 {
		t.Fatalf("tail gap length = %d, want 4", segs[2].Length)
	}

	// Zero target against a non-zero source must not match.
	nzIdx := testIndex(t, bytes.Repeat([]byte{0x5A}, 32), testBlockSize)
	segs = runOneShot(t, nzIdx, testBlockSize, target)
	assertCoverage(t, segs, int64(len(target)))
	for _, s := range segs {
		if s.Kind == Match {
			t.Fatalf("zero bytes matched non-zero source: %+v", s)
		}
	}
}

func TestPauseResume(t *testing.T) {
	var source []byte
	for i := 0; i < 6; i++ {
		source = append(source, bytes.Repeat([]byte{byte(i + 1)}, testBlockSize)...)
	}
	idx := testIndex(t, source, testBlockSize)

	var target []byte
	target = append(target, []byte("noise!")...)
	target = append(target, source...)
	target = append(target, []byte("end")...)

	want := runOneShot(t, idx, testBlockSize, target)

	// Feed in awkward chunks, pausing mid-window.
	m, err := NewMatcher(idx, testBlockSize)
	if err != nil {
		t.Fatal(err)
	}
	var got []Segment
	got = append(got, m.Feed(target[:13])...)
	snap, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	m2, err := Restore(idx, snap)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got = append(got, m2.Feed(target[13:31])...)
	snap2, err := m2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	m3, err := Restore(idx, snap2)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got = append(got, m3.Feed(target[31:])...)
	got = append(got, m3.Flush()...)

	assertCoverage(t, got, int64(len(target)))
	assertMatchContent(t, idx, target, got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed run differs from one-shot:\n got %v\nwant %v", got, want)
	}
}

func TestChangeBlockSize(t *testing.T) {
	source := []byte("0123456789ABCDEF")
	idx := testIndex(t, source, 4, 8)

	// Change size between chunks: two 4-byte matches, then one 8-byte match.
	m, err := NewMatcher(idx, 4)
	if err != nil {
		t.Fatal(err)
	}
	segs := m.Feed(source[:8])
	if err := m.SetBlockSize(8); err != nil {
		t.Fatalf("SetBlockSize: %v", err)
	}
	segs = append(segs, m.Feed(source[8:])...)
	segs = append(segs, m.Flush()...)
	assertCoverage(t, segs, int64(len(source)))
	assertMatchContent(t, idx, source, segs)
	want := []Segment{
		{Kind: Match, Offset: 0, Length: 4, SourceOffset: 0},
		{Kind: Match, Offset: 4, Length: 4, SourceOffset: 4},
		{Kind: Match, Offset: 8, Length: 8, SourceOffset: 8},
	}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("got %v, want %v", segs, want)
	}

	// Change size while the window holds undecided bytes: nothing is lost.
	m2, err := NewMatcher(idx, 8)
	if err != nil {
		t.Fatal(err)
	}
	var segs2 []Segment
	segs2 = append(segs2, m2.Feed(source[:6])...) // partial window, no output
	if err := m2.SetBlockSize(4); err != nil {
		t.Fatalf("SetBlockSize: %v", err)
	}
	segs2 = append(segs2, m2.Feed(source[6:])...)
	segs2 = append(segs2, m2.Flush()...)
	assertCoverage(t, segs2, int64(len(source)))
	assertMatchContent(t, idx, source, segs2)
}
