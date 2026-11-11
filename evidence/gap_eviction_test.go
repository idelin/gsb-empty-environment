package evidence

import "testing"

func TestEvictionRecordPreservesGaps(t *testing.T) {
	b := NewBuffer(20)
	// Register first, then deliver both sides of a window with a real stream
	// hole [10,20) between them.
	if err := b.Notify(Candidate{
		EventID: 1, Priority: 9, Match: Range{Start: 5, End: 25},
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b, 0, mkBytes(0, 10))   // [0,10): pins [5,10)
	mustWrite(t, b, 20, mkBytes(20, 10)) // [20,30): pins [20,25), over budget
	if got := b.Stats().ActiveCandidates; got != 1 {
		t.Fatalf("active before pressure = %d, want 1", got)
	}
	// A second, less-important candidate plus a disjoint write forces the
	// policy to evict event 1 while both of its gapped ranges are retained.
	if err := b.Notify(Candidate{EventID: 2, Priority: 0, Match: Range{Start: 40, End: 50}}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b, 40, mkBytes(40, 10))

	records := b.Evictions()
	if len(records) != 1 || records[0].EventID != 1 {
		t.Fatalf("evictions = %+v", records)
	}
	// Only retained ranges are reported lost; the hole [10,20) must not be
	// invented as an eviction range.
	want := []Range{{Start: 5, End: 10}, {Start: 20, End: 25}}
	if len(records[0].Missing) != len(want) {
		t.Fatalf("missing = %+v want %v", records[0].Missing, want)
	}
	for i, seg := range records[0].Missing {
		if seg.Range != want[i] || seg.Reason != ReasonEvicted {
			t.Fatalf("missing[%d] = %+v want %v", i, seg, want[i])
		}
	}

	ev, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev)

	// The evicted snapshot keeps the hole as an explicit gap, not evicted.
	var sawGap bool
	for _, seg := range ev.Segments {
		if seg.Range == (Range{Start: 10, End: 20}) {
			if seg.Reason != ReasonGap || seg.Data != nil {
				t.Fatalf("hole reported as %+v", seg)
			}
			sawGap = true
		}
	}
	if !sawGap {
		t.Fatalf("hole missing from evicted snapshot: %+v", ev.Segments)
	}
}

func TestHighPrioritySurvivesBudgetPressure(t *testing.T) {
	// A ten-byte budget holds only one pinned window at a time, so every
	// lower-priority arrival must evict the previous least-important holder.
	b := NewBuffer(10)
	// Important candidate pins a small window.
	if err := b.Notify(Candidate{EventID: 1, Priority: 0, Match: Range{Start: 0, End: 10}}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b, 0, mkBytes(0, 10))

	// Flood less-important candidates over disjoint ranges; each should be
	// evicted before touching priority-0 event 1.
	for event := uint64(2); event <= 8; event++ {
		base := int64(event * 20)
		if err := b.Notify(Candidate{EventID: event, Priority: 9, Match: Range{Start: base, End: base + 10}}); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, b, base, mkBytes(int(base), 10))
	}

	if b.Stats().ActiveCandidates != 1 {
		t.Fatalf("active = %d, want only event 1", b.Stats().ActiveCandidates)
	}
	ev, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Evicted {
		t.Fatal("priority-0 candidate was evicted")
	}
	assertPartition(t, ev)
	for off := int64(0); off < 10; off++ {
		got, ok := windowBytes(ev, off)
		if !ok || got != byte(off) {
			t.Fatalf("high-priority window truncated at %d", off)
		}
	}
}
