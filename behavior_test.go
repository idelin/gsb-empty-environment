package netsec

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

// TestBoundaryIntersections pins exact behavior on touching and overlapping
// half-open boundaries.
func TestBoundaryIntersections(t *testing.T) {
	origin := []byte("0123456789") // offsets 0..9
	doc := NewDocument(origin)
	v, err := doc.AddRanges(
		Range{Start: 2, End: 5, Label: "a"}, // masks 2,3,4
		Range{Start: 5, End: 7, Label: "a"}, // touches at 5: masks 5,6
		Range{Start: 4, End: 6, Label: "b"}, // crosses both: masks 4,5
	)
	if err != nil {
		t.Fatal(err)
	}

	// No authorization: touching and crossing ranges coalesce into [2,7).
	none := v.Reader()
	if got, want := none.MaskedRanges(), []Range{{Start: 2, End: 7}}; !rangesEqual(got, want) {
		t.Fatalf("none proof = %+v, want %+v", got, want)
	}
	assertView(t, none, origin, "01*****789")

	// Only "a": bytes 4,5 stay hidden by "b", splitting the proof.
	onlyA := v.Reader("a")
	if got, want := onlyA.MaskedRanges(), []Range{{Start: 4, End: 6}}; !rangesEqual(got, want) {
		t.Fatalf("only-a proof = %+v, want %+v", got, want)
	}
	assertView(t, onlyA, origin, "0123**6789")

	// Both labels: everything visible, empty proof.
	both := v.Reader("a", "b")
	if len(both.MaskedRanges()) != 0 {
		t.Fatalf("fully authorized reader must have empty proof: %+v", both.MaskedRanges())
	}
	assertView(t, both, origin, "0123456789")

	// A read straddling a single boundary on both sides.
	buf := make([]byte, 4)
	if n, err := onlyA.ReadAt(buf, 3); err != nil || n != 4 {
		t.Fatalf("boundary read: n=%d err=%v", n, err)
	}
	if string(buf) != "3**6" {
		t.Fatalf("boundary window = %q want 3**6", buf)
	}
}

// TestMultiLabelOverlap requires every guard label on a byte to be granted.
func TestMultiLabelOverlap(t *testing.T) {
	origin := []byte("abcdefghij")
	doc := NewDocument(origin)
	v, err := doc.AddRanges(
		Range{Start: 0, End: 6, Label: "pii"},
		Range{Start: 3, End: 9, Label: "secrets"},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertView(t, v.Reader("pii"), origin, "abc******j")
	assertView(t, v.Reader("secrets"), origin, "******ghij")
	assertView(t, v.Reader(), origin, "*********j")
	assertView(t, v.Reader("pii", "secrets"), origin, "abcdefghij")

	got := v.Reader("pii").MaskedRanges()
	want := []Range{{Start: 3, End: 9}}
	if !rangesEqual(got, want) {
		t.Fatalf("overlap proof = %+v want %+v", got, want)
	}
}

// TestVersionsAndPinnedReaders checks that updates create new versions while
// readers pinned to an old version keep observing the old masking, including
// under concurrent updates and reads.
func TestVersionsAndPinnedReaders(t *testing.T) {
	origin := []byte("0123456789")
	doc := NewDocument(origin)
	v1, err := doc.AddRanges(Range{Start: 0, End: 4, Label: "l1"})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := doc.ReplaceRanges(Range{Start: 6, End: 10, Label: "l2"})
	if err != nil {
		t.Fatal(err)
	}

	if v1.Number() != 2 || v2.Number() != 3 || doc.Latest().Number() != 3 {
		t.Fatalf("unexpected version numbers %d %d %d", v1.Number(), v2.Number(), doc.Latest().Number())
	}

	oldReader := v1.Reader()
	newReader := v2.Reader()
	assertView(t, oldReader, origin, "****456789")
	assertView(t, newReader, origin, "012345****")

	if got, err := doc.Version(2); err != nil || got != v1 {
		t.Fatalf("Version(2) = %v err=%v", got, err)
	}
	if _, err := doc.Version(99); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("want ErrUnknownVersion, got %v", err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				if _, err := doc.AddRanges(Range{
					Start: int64(k % 5),
					End:   int64(k%5) + 1,
					Label: fmt.Sprintf("dynamic-%d", k),
				}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				buf := make([]byte, len(origin))
				if _, err := oldReader.ReadAt(buf, 0); err != nil {
					t.Error(err)
					return
				}
				if string(buf) != "****456789" {
					t.Errorf("old version view drifted: %q", buf)
					return
				}
				buf2 := make([]byte, len(origin))
				if _, err := newReader.ReadAt(buf2, 0); err != nil {
					t.Error(err)
					return
				}
				if string(buf2) != "012345****" {
					t.Errorf("new version view drifted: %q", buf2)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 1 initial + 1 add + 1 replace + 800 concurrent adds.
	if doc.Latest().Number() != 803 {
		t.Fatalf("final version = %d want 803", doc.Latest().Number())
	}
	// Old pinned view still correct after all updates.
	assertView(t, oldReader, origin, "****456789")
}

// TestHugeSparseRanges models a giant evidence space with a few small, widely
// separated sensitive intervals. No giant allocation is required: offsets are
// int64 and only covered offsets are represented.
func TestHugeSparseRanges(t *testing.T) {
	const size int64 = 1 << 40 // 1 TiB logical evidence; never allocated
	doc := NewDocument(make([]byte, 4096), WithLogicalLength(size))

	v, err := doc.AddRanges(
		Range{Start: 100, End: 200, Label: "head"},
		Range{Start: size - 10, End: size, Label: "tail"},
	)
	if err != nil {
		t.Fatal(err)
	}

	proof := v.Reader().MaskedRanges()
	want := []Range{
		{Start: 100, End: 200},
		{Start: size - 10, End: size},
	}
	if !rangesEqual(proof, want) {
		t.Fatalf("sparse proof = %+v want %+v", proof, want)
	}

	// Granting the far-away tail label must not open the head interval.
	partial := v.Reader("tail")
	if got, want := partial.MaskedRanges(), []Range{{Start: 100, End: 200}}; !rangesEqual(got, want) {
		t.Fatalf("partial sparse proof = %+v want %+v", got, want)
	}

	// Reading inside the physically backed head interval masks in place and
	// keeps offsets identical to the evidence.
	buf := make([]byte, 4)
	if n, err := partial.ReadAt(buf, 198); err != nil || n != 4 {
		t.Fatalf("sparse read: n=%d err=%v", n, err)
	}
	if string(buf) != "**"+string(doc.origin[200:202]) {
		t.Fatalf("sparse boundary read = %v", buf)
	}

	// A read starting at the logical evidence end is EOF, not a panic.
	if n, err := partial.ReadAt(make([]byte, 8), size); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("beyond-end read: n=%d err=%v", n, err)
	}

	// A read inside the unbacked tail returns zero bytes, masked in place.
	hole := make([]byte, 16)
	if n, err := partial.ReadAt(hole, size-8); n != 8 || !errors.Is(err, io.EOF) {
		t.Fatalf("hole read: n=%d err=%v", n, err)
	}
	wantHole := []byte{0, 0, 0, 0, 0, 0, 0, 0,
		doc.MaskByte(), doc.MaskByte(), doc.MaskByte(), doc.MaskByte(),
		doc.MaskByte(), doc.MaskByte(), doc.MaskByte(), doc.MaskByte()}[:8]
	if !bytesEqual(hole[:8], wantHole) {
		t.Fatalf("hole view = %v want %v", hole, wantHole)
	}

	if doc.Len() != size {
		t.Fatalf("logical length = %d want %d", doc.Len(), size)
	}

	// Segment storage stays proportional to covered intervals, not the gap.
	if got := len(v.segs); got != 2 {
		t.Fatalf("segment count = %d want 2", got)
	}
}

// TestInvalidRanges rejects malformed intervals without publishing a version.
func TestInvalidRanges(t *testing.T) {
	doc := NewDocument(make([]byte, 10))
	bad := []Range{
		{Start: -1, End: 2, Label: "x"},
		{Start: 5, End: 5, Label: "x"},
		{Start: 6, End: 4, Label: "x"},
		{Start: 8, End: 11, Label: "x"},
		{Start: 1, End: 3, Label: ""},
	}
	for _, r := range bad {
		if _, err := doc.AddRanges(r); !errors.Is(err, ErrInvalidRange) {
			t.Fatalf("range %+v: want ErrInvalidRange got %v", r, err)
		}
	}
	if doc.Latest().Number() != 1 || len(doc.Latest().Ranges()) != 0 {
		t.Fatalf("failed updates must not publish versions")
	}
}

// TestImmutabilityAndDefensiveCopies ensures callers cannot mutate evidence or
// internal version/reader state through returned slices.
func TestImmutabilityAndDefensiveCopies(t *testing.T) {
	origin := []byte("0123456789")
	doc := NewDocument(origin)
	v, err := doc.AddRanges(Range{Start: 0, End: 2, Label: "a"})
	if err != nil {
		t.Fatal(err)
	}

	origin[0] = 'X' // caller-side mutation must not corrupt the document
	ranges := v.Ranges()
	ranges[0].Start = 999
	if got := v.Ranges()[0].Start; got != 0 {
		t.Fatalf("internal ranges mutated through copy: start=%d", got)
	}

	r := v.Reader()
	proof := r.Proof()
	proof.Masked[0] = Range{Start: 1, End: 9}
	if got := r.Proof().Masked; !rangesEqual(got, []Range{{Start: 0, End: 2}}) {
		t.Fatalf("internal proof mutated through copy: %+v", got)
	}

	got := make([]byte, 10)
	if _, err := r.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if string(got) != "**23456789" {
		t.Fatalf("evidence view corrupted by caller mutation: %q", got)
	}
}

// TestCustomMaskByte verifies the fixed replacement byte is configurable.
func TestCustomMaskByte(t *testing.T) {
	doc := NewDocument([]byte("abcdef"), WithMaskByte(0x00))
	v, err := doc.AddRanges(Range{Start: 1, End: 4, Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 6)
	if _, err := v.Reader().ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	want := []byte{'a', 0, 0, 0, 'e', 'f'}
	if !bytesEqual(got, want) {
		t.Fatalf("custom mask view = %v want %v", got, want)
	}
	if doc.MaskByte() != 0 {
		t.Fatalf("MaskByte accessor = %d", doc.MaskByte())
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReadAtEdgeCases covers zero-length buffers, the initial empty version
// and a read that lands entirely inside an unbacked sparse hole.
func TestReadAtEdgeCases(t *testing.T) {
	doc := NewDocument([]byte("abc"), WithLogicalLength(10))
	v1 := doc.Latest()
	if v1.Number() != 1 || len(v1.Ranges()) != 0 {
		t.Fatalf("initial version = %d ranges=%d", v1.Number(), len(v1.Ranges()))
	}
	r := v1.Reader()

	if n, err := r.ReadAt(nil, 0); n != 0 || err != nil {
		t.Fatalf("empty read: n=%d err=%v", n, err)
	}
	if n, err := r.ReadAt(make([]byte, 0), 1); n != 0 || err != nil {
		t.Fatalf("zero-len read: n=%d err=%v", n, err)
	}
	if _, err := r.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("negative offset must error")
	}

	hole := make([]byte, 4)
	if n, err := r.ReadAt(hole, 6); err != nil || n != 4 {
		t.Fatalf("hole read: n=%d err=%v", n, err)
	}
	if !bytesEqual(hole, []byte{0, 0, 0, 0}) {
		t.Fatalf("hole bytes = %v want zeros", hole)
	}

	// No ranges means an empty proof for every authorization.
	if len(r.Proof().Masked) != 0 {
		t.Fatalf("empty version must produce empty proof: %+v", r.Proof())
	}
}

func BenchmarkSparseReadAt(b *testing.B) {
	const size int64 = 1 << 20
	origin := make([]byte, size)
	for i := range origin {
		origin[i] = byte(i)
	}
	doc := NewDocument(origin)
	v, err := doc.AddRanges(
		Range{Start: 10, End: 20, Label: "a"},
		Range{Start: size - 20, End: size, Label: "b"},
	)
	if err != nil {
		b.Fatal(err)
	}
	r := v.Reader("a")
	buf := make([]byte, 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.ReadAt(buf, int64((i*4096)%int(size-4096))); err != nil && !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
	}
}
