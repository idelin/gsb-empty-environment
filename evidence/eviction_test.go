package evidence

import (
	"errors"
	"fmt"
	"testing"
)

func TestEvictionOrderPriorityThenEventID(t *testing.T) {
	// Budget of one byte: each new disjoint window exceeds the cap as soon as
	// it is pinned, forcing an immediate policy-driven eviction.
	b := NewBuffer(1)
	cands := []Candidate{
		{EventID: 3, Priority: 1, Match: Range{Start: 0, End: 10}},
		{EventID: 1, Priority: 5, Match: Range{Start: 20, End: 30}},
		{EventID: 2, Priority: 5, Match: Range{Start: 40, End: 50}},
		{EventID: 4, Priority: 0, Match: Range{Start: 50, End: 60}},
	}
	for _, c := range cands {
		// Register first so the incoming bytes are pinned on arrival; with a
		// one-byte budget each new pinned window forces a policy eviction.
		if err := b.Notify(c); err != nil {
			t.Fatal(err)
		}
		matchStart := int(c.Match.Start)
		mustWrite(t, b, c.Match.Start, mkBytes(matchStart, int(c.Match.Len())))
	}

	evictions := b.Evictions()
	gotIDs := make([]uint64, 0, len(evictions))
	for _, e := range evictions {
		gotIDs = append(gotIDs, e.EventID)
	}
	// Each pinning write exceeds the one-byte cap immediately: event 3 goes
	// first, then priority-5 events in EventID order (1, 2), leaving the
	// priority-0 event 4 as the survivor.
	// The final 10-byte pinning write leaves event 4 itself over the cap, so
	// it too is evicted; every eviction still follows priority then EventID.
	wantIDs := []uint64{3, 1, 2, 4}
	if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("eviction order = %v, want %v", gotIDs, wantIDs)
	}
	if b.Stats().ActiveCandidates != 0 {
		t.Fatalf("active candidates = %d, want 0", b.Stats().ActiveCandidates)
	}

	// The last-evicted event still yields a complete, explicit snapshot.
	ev, err := b.Collect(4)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev)
	if !ev.Evicted || ev.Window != (Range{Start: 50, End: 60}) {
		t.Fatalf("event 4 snapshot = %+v", ev)
	}

	// The eviction record names the exact retained range lost.
	first := evictions[0]
	if len(first.Missing) != 1 ||
		first.Missing[0].Range != (Range{Start: 0, End: 10}) ||
		first.Missing[0].Reason != ReasonEvicted {
		t.Fatalf("bad eviction record: %+v", first)
	}
	ev1, err := b.Collect(3)
	if err != nil {
		t.Fatal(err)
	}
	if !ev1.Evicted {
		t.Fatal("expected Evicted flag")
	}
	assertPartition(t, ev1)
}

func TestCancelReleasesReferencesAndReclaimsCache(t *testing.T) {
	b := NewBuffer(100)
	// Register candidates first, as a detection host does, so incoming bytes
	// are pinned to their windows on arrival.
	if err := b.Notify(Candidate{EventID: 1, Match: Range{Start: 0, End: 60}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Notify(Candidate{EventID: 2, Match: Range{Start: 60, End: 100}}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b, 0, mkBytes(0, 100))
	if b.Stats().ReferencedBytes != 100 {
		t.Fatalf("referenced = %d", b.Stats().ReferencedBytes)
	}

	if err := b.Cancel(1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Collect(1); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("collect after cancel: %v", err)
	}
	if len(b.Evictions()) != 0 {
		t.Fatalf("cancel produced eviction: %+v", b.Evictions())
	}

	// An oversized follow-up write reclaims canceled cache rather than
	// evicting the still-active candidate 2.
	mustWrite(t, b, 100, mkBytes(100, 60))
	if b.Stats().ActiveCandidates != 1 {
		t.Fatalf("active candidates = %d", b.Stats().ActiveCandidates)
	}
	ev2, err := b.Collect(2)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev2)
	if ev2.Window != (Range{Start: 60, End: 100}) {
		t.Fatalf("window = %v", ev2.Window)
	}
	for off := int64(60); off < 100; off++ {
		got, ok := windowBytes(ev2, off)
		if !ok || got != byte(off&0xff) {
			t.Fatalf("active candidate truncated after cancel: %d", off)
		}
	}
}

func TestHardCapTrimsCacheAndEvictionReportsLoss(t *testing.T) {
	b := NewBuffer(10)
	mustWrite(t, b, 0, mkBytes(0, 40))
	// Unpinned cache cannot serve any committed window, so it is fully
	// reclaimed rather than held against the budget.
	if got := b.Stats().RetainedBytes; got != 0 {
		t.Fatalf("retained = %d, want 0 unpinned bytes", got)
	}

	// With a candidate, the same pressure evicts the candidate and names the
	// lost retained range explicitly instead of truncating silently.
	b2 := NewBuffer(10)
	if err := b2.Notify(Candidate{EventID: 1, Match: Range{Start: 0, End: 40}}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b2, 0, mkBytes(0, 40))

	evictions := b2.Evictions()
	if len(evictions) != 1 || evictions[0].EventID != 1 {
		t.Fatalf("evictions = %+v", evictions)
	}
	if len(evictions[0].Missing) != 1 ||
		evictions[0].Missing[0].Range != (Range{Start: 0, End: 40}) {
		t.Fatalf("missing record = %+v", evictions[0].Missing)
	}
	ev, err := b2.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, ev)
	if !ev.Evicted {
		t.Fatal("expected evicted evidence")
	}
}

func TestOverlappingWriteConflictRejectsAndPreserves(t *testing.T) {
	b := NewBuffer(0)
	mustWrite(t, b, 0, mkBytes(0, 10))
	bad := mkBytes(100, 10)
	if err := b.Write(5, bad); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("conflicting write: %v", err)
	}
	if err := b.Notify(Candidate{EventID: 1, Match: Range{Start: 0, End: 10}}); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	for off := int64(0); off < 10; off++ {
		got, _ := windowBytes(ev, off)
		if got != byte(off&0xff) {
			t.Fatalf("buffer mutated by rejected write at %d", off)
		}
	}

	// Identical overlapping content is accepted and deduplicated.
	if err := b.Write(5, mkBytes(5, 5)); err != nil {
		t.Fatal(err)
	}
	if b.Stats().RetainedBytes != 10 {
		t.Fatalf("retained = %d, want 10", b.Stats().RetainedBytes)
	}
}

func TestValidationAndSealErrors(t *testing.T) {
	b := NewBuffer(0)
	if err := b.Notify(Candidate{EventID: 0, Match: Range{Start: 0, End: 1}}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("zero event id: %v", err)
	}
	if err := b.Notify(Candidate{EventID: 1, Match: Range{Start: 0, End: 1}, BeforeContext: -1}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("negative context: %v", err)
	}
	mustWrite(t, b, 0, []byte{1})
	c := Candidate{EventID: 1, Match: Range{Start: 0, End: 1}}
	if err := b.Notify(c); err != nil {
		t.Fatal(err)
	}
	if err := b.Notify(c); !errors.Is(err, ErrDuplicateEvent) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := b.Cancel(99); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("unknown cancel: %v", err)
	}
	b.Close()
	if err := b.Write(1, []byte{2}); !errors.Is(err, ErrSealed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := b.Cancel(1); err != nil {
		t.Fatal(err)
	}
	if err := b.Cancel(1); !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("double cancel: %v", err)
	}
}
