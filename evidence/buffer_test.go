package evidence

import "testing"

func mkBytes(start, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((start + i) & 0xff)
	}
	return out
}

func mustWrite(t *testing.T, b *Buffer, off int64, p []byte) {
	t.Helper()
	if err := b.Write(off, p); err != nil {
		t.Fatalf("write at %d: %v", off, err)
	}
}

func windowBytes(ev Evidence, off int64) (byte, bool) {
	for _, seg := range ev.Segments {
		if off >= seg.Range.Start && off < seg.Range.End {
			if seg.Data == nil {
				return 0, false
			}
			return seg.Data[off-seg.Range.Start], true
		}
	}
	return 0, false
}

// assertPartition verifies segments tile the window exactly.
func assertPartition(t *testing.T, ev Evidence) {
	t.Helper()
	if len(ev.Segments) == 0 {
		t.Fatalf("event %d: no segments", ev.EventID)
	}
	cursor := ev.Window.Start
	for i, seg := range ev.Segments {
		if seg.Range.Start != cursor {
			t.Fatalf("event %d: segment %d starts at %d, expected %d", ev.EventID, i, seg.Range.Start, cursor)
		}
		if seg.Range.Len() <= 0 {
			t.Fatalf("event %d: empty segment %v", ev.EventID, seg.Range)
		}
		if seg.Data != nil {
			if int64(len(seg.Data)) != seg.Range.Len() {
				t.Fatalf("event %d: data length %d != range %d", ev.EventID, len(seg.Data), seg.Range.Len())
			}
		} else if seg.Reason == ReasonEvicted && !ev.Evicted {
			t.Fatalf("event %d: evicted segment on active candidate", ev.EventID)
		}
		cursor = seg.Range.End
	}
	if cursor != ev.Window.End {
		t.Fatalf("event %d: segments end at %d, window ends %d", ev.EventID, cursor, ev.Window.End)
	}
}

func TestWriteCollectWithContextAndClamping(t *testing.T) {
	b := NewBuffer(0)
	mustWrite(t, b, 0, mkBytes(0, 40))
	cand := Candidate{
		EventID:       1,
		Match:         Range{Start: 10, End: 20},
		BeforeContext: 50,
		AfterContext:  5,
	}
	if err := b.Notify(cand); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Window != (Range{Start: 0, End: 25}) {
		t.Fatalf("unexpected window %v", ev.Window)
	}
	assertPartition(t, ev)
	for off := int64(0); off < 25; off++ {
		got, ok := windowBytes(ev, off)
		if !ok || got != byte(off&0xff) {
			t.Fatalf("offset %d: got %d ok=%v", off, got, ok)
		}
	}
}

func TestGapsAreExplicitAndPendingIsDistinct(t *testing.T) {
	b := NewBuffer(0)
	mustWrite(t, b, 0, mkBytes(0, 10))
	mustWrite(t, b, 20, mkBytes(20, 10))
	mustWrite(t, b, 40, mkBytes(40, 10))
	// Stream never delivers bytes [50,60); a large AfterContext reaches into
	// that never-received tail.
	mustWrite(t, b, 60, mkBytes(60, 5))

	if err := b.Notify(Candidate{
		EventID: 7, Match: Range{Start: 22, End: 24},
		BeforeContext: 15, AfterContext: 30,
	}); err != nil {
		t.Fatal(err)
	}
	b.Close()

	ev, err := b.Collect(7)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev)

	wantReasons := map[Range]Reason{
		{Start: 10, End: 20}: ReasonGap,
		{Start: 30, End: 40}: ReasonGap,
		{Start: 50, End: 54}: ReasonGap,
	}
	for _, seg := range ev.Segments {
		if seg.Data != nil {
			continue
		}
		if want, ok := wantReasons[seg.Range]; ok {
			if seg.Reason != want || seg.Data != nil {
				t.Fatalf("range %v: got reason=%v data=%v want %v", seg.Range, seg.Reason, seg.Data != nil, want)
			}
			delete(wantReasons, seg.Range)
		} else {
			t.Fatalf("unexpected missing segment %v reason %v", seg.Range, seg.Reason)
		}
	}
	if len(wantReasons) != 0 {
		t.Fatalf("missing expected segments: %v", wantReasons)
	}
}

func TestOutOfOrderWritesFillGaps(t *testing.T) {
	b := NewBuffer(0)
	mustWrite(t, b, 20, mkBytes(20, 10))
	if err := b.Notify(Candidate{
		EventID: 1, Match: Range{Start: 20, End: 30},
		BeforeContext: 5, AfterContext: 5,
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b, 15, mkBytes(15, 5))
	mustWrite(t, b, 30, mkBytes(30, 5))

	ev, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev)
	for off := int64(15); off < 35; off++ {
		got, ok := windowBytes(ev, off)
		if !ok || got != byte(off&0xff) {
			t.Fatalf("offset %d not filled: %d ok=%v", off, got, ok)
		}
	}
}

func TestOverlappingCandidatesShareStorage(t *testing.T) {
	b := NewBuffer(0)
	mustWrite(t, b, 0, mkBytes(0, 100))

	c1 := Candidate{EventID: 1, Priority: 1, Match: Range{Start: 20, End: 30}, BeforeContext: 10, AfterContext: 10}
	c2 := Candidate{EventID: 2, Priority: 1, Match: Range{Start: 25, End: 35}, BeforeContext: 10, AfterContext: 10}
	if err := b.Notify(c1); err != nil {
		t.Fatal(err)
	}
	if err := b.Notify(c2); err != nil {
		t.Fatal(err)
	}

	stats := b.Stats()
	// The windows cover the whole retained stream between them; their overlap
	// [15,40) is stored once, so referenced equals retained with no cache.
	if stats.ReferencedBytes != 100 || stats.RetainedBytes != 100 || stats.CachedBytes != 0 {
		t.Fatalf("stats = %+v, want referenced=100 retained=100 cached=0", stats)
	}

	ev1, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	ev2, err := b.Collect(2)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev1)
	assertPartition(t, ev2)
	for off := int64(15); off < 40; off++ {
		g1, _ := windowBytes(ev1, off)
		g2, _ := windowBytes(ev2, off)
		if g1 != g2 || g1 != byte(off&0xff) {
			t.Fatalf("shared offset %d diverged: %d vs %d", off, g1, g2)
		}
	}
}
