package agg

import (
	"math"
	"reflect"
	"testing"
)

func ev(id string, ts int64, v float64) Event { return Event{ID: id, TS: ts, Value: v} }

func TestBucketBoundariesAndGaps(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{
		ev("a", 0, 1), ev("b", 9, 2), // bucket 0
		ev("c", 10, 3), ev("d", 19, 4), // bucket 1
		ev("z", 25, 0), // bucket 2: present with value 0
	})
	r := s.Query(0, 40)
	if len(r.Buckets) != 4 {
		t.Fatalf("want 4 buckets, got %d", len(r.Buckets))
	}
	// Boundary: ts=10 belongs to bucket 1, not bucket 0.
	if r.Buckets[0].Count != 2 || r.Buckets[0].Sum != 3 {
		t.Errorf("bucket 0: %+v", r.Buckets[0].Summary)
	}
	if r.Buckets[1].Count != 2 || r.Buckets[1].Sum != 7 || r.Buckets[1].First.ID != "c" {
		t.Errorf("bucket 1: %+v", r.Buckets[1].Summary)
	}
	// Value 0 is not a gap.
	if !r.Buckets[2].Present || r.Buckets[2].Count != 1 || r.Buckets[2].Sum != 0 {
		t.Errorf("bucket 2 should be present with sum 0: %+v", r.Buckets[2].Summary)
	}
	// Bucket 3 has no events: a gap, distinct from zero.
	if r.Buckets[3].Present || r.Buckets[3].Count != 0 {
		t.Errorf("bucket 3 should be an absent gap: %+v", r.Buckets[3].Summary)
	}
	if r.Count != 5 || r.Sum != 10 {
		t.Errorf("total: %+v", r.Summary)
	}
}

func TestNegativeTimestamps(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{ev("n", -1, 5), ev("p", 0, 6)})
	r := s.Query(-10, 10)
	if r.Buckets[0].Index != -1 || r.Buckets[0].Count != 1 || r.Buckets[0].Max.Value != 5 {
		t.Errorf("bucket -1: %+v", r.Buckets[0])
	}
	if r.Buckets[1].Index != 0 || r.Buckets[1].Count != 1 {
		t.Errorf("bucket 0: %+v", r.Buckets[1])
	}
}

func TestDedupOverlappingBatches(t *testing.T) {
	s := NewStore(10)
	r1 := s.Ingest([]Event{ev("a", 1, 1), ev("b", 2, 2), ev("c", 3, 3)})
	if r1.Added != 3 || r1.Duplicates != 0 {
		t.Fatalf("first ingest: %+v", r1)
	}
	// Overlapping batch: b and c already delivered.
	r2 := s.Ingest([]Event{ev("b", 2, 2), ev("c", 3, 3), ev("d", 4, 4)})
	if r2.Added != 1 || r2.Duplicates != 2 {
		t.Fatalf("overlapping ingest: %+v", r2)
	}
	// Full redelivery is a no-op.
	r3 := s.Ingest([]Event{ev("a", 1, 1), ev("b", 2, 2), ev("c", 3, 3), ev("d", 4, 4)})
	if r3.Added != 0 || r3.Duplicates != 4 {
		t.Fatalf("redelivery: %+v", r3)
	}
	q := s.Query(0, 100)
	if q.Count != 4 || q.Sum != 10 {
		t.Fatalf("after dedup: count=%d sum=%v", q.Count, q.Sum)
	}
}

func TestFirstLastTiebreakByID(t *testing.T) {
	s := NewStore(10)
	// Same timestamp: ID decides first/last.
	s.Ingest([]Event{ev("b", 5, 1), ev("a", 5, 2), ev("c", 5, 3)})
	r := s.Query(0, 10)
	if r.First.ID != "a" || r.Last.ID != "c" {
		t.Fatalf("first=%+v last=%+v", r.First, r.Last)
	}
}

func TestMeanIsSumOverCount(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{
		ev("big", 0, 10),                               // bucket 0: mean 10
		ev("x", 10, 1), ev("y", 11, 2), ev("z", 12, 3), // bucket 1: mean 2
	})
	r := s.Query(0, 20)
	// (10+1+2+3)/4 = 4, not the average of bucket means (10+2)/2 = 6.
	if r.Mean() != 4 {
		t.Fatalf("mean = %v, want 4 (sum/count, not mean of means)", r.Mean())
	}
}

func TestPeakRetractionFindsNextMax(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{ev("lo", 1, 5), ev("peak", 2, 100), ev("mid", 3, 7)})
	before := s.Query(0, 10)
	if before.Max.ID != "peak" || before.Sum != 112 {
		t.Fatalf("before: max=%+v sum=%v", before.Max, before.Sum)
	}
	snap := s.Version()

	// Retract only the peak sample.
	rec := s.Delete(2, 3)
	if rec.Removed != 1 {
		t.Fatalf("removed %d, want 1", rec.Removed)
	}
	after := s.Query(0, 10)
	// The next-largest value must surface; only subtracting from the sum
	// would leave Max stale.
	if after.Max.ID != "mid" || after.Max.Value != 7 {
		t.Fatalf("after retraction: max=%+v, want mid/7", after.Max)
	}
	if after.Count != 2 || after.Sum != 12 || after.Mean() != 6 {
		t.Fatalf("after: %+v mean=%v", after.Summary, after.Mean())
	}

	// Old snapshot still shows the retracted peak.
	old := s.QueryAt(0, 10, snap)
	if old.Max.ID != "peak" || old.Count != 3 || old.Sum != 112 {
		t.Fatalf("snapshot: max=%+v count=%d sum=%v", old.Max, old.Count, old.Sum)
	}
}

func TestSinglePointPeakPersists(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{
		ev("spike", 5, 9000), // alone in bucket 0
		ev("a", 10, 1), ev("b", 11, 2),
	})
	r := s.Query(0, 20)
	if r.Buckets[0].Count != 1 || r.Buckets[0].Max.ID != "spike" || r.Buckets[0].Min.ID != "spike" {
		t.Fatalf("single-point bucket: %+v", r.Buckets[0].Summary)
	}
	// Deleting the other bucket must not make the single-point peak vanish.
	s.Delete(10, 20)
	r = s.Query(0, 20)
	if r.Max.ID != "spike" || r.Count != 1 {
		t.Fatalf("peak vanished: %+v", r.Summary)
	}
}

func TestVersionedCorrectionAndSnapshot(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{ev("a", 1, 1), ev("b", 2, 2), ev("c", 12, 3)})
	v1 := s.Version()
	before := s.Query(0, 20)

	// Correct [0,10): replace a,b with a single corrected event.
	rec := s.Correct(0, 10, []Event{ev("a2", 1, 40)})
	if rec.Kind != "correct" || rec.Removed != 2 || rec.Added != 1 {
		t.Fatalf("correction record: %+v", rec)
	}
	after := s.Query(0, 20)
	if after.Count != 2 || after.Sum != 43 || after.Max.Value != 40 {
		t.Fatalf("after correction: %+v", after.Summary)
	}

	// Snapshot at v1 is unchanged.
	old := s.QueryAt(0, 20, v1)
	if old.Count != before.Count || old.Sum != before.Sum || old.Max != before.Max ||
		old.Min != before.Min || old.First != before.First || old.Last != before.Last {
		t.Fatalf("snapshot drifted: old=%+v want %+v", old.Summary, before.Summary)
	}
}

func TestCorrectionAuditRecordsBucketsAndDetailRange(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{ev("a", 3, 1), ev("b", 13, 2), ev("c", 23, 3), ev("d", 33, 4)})
	rec := s.Delete(5, 25) // touches buckets 0,1,2; scanned detail range [5,25)
	if rec.Version != 2 || rec.Kind != "delete" {
		t.Fatalf("record: %+v", rec)
	}
	wantBuckets := []int64{0, 1, 2}
	if len(rec.Buckets) != len(wantBuckets) {
		t.Fatalf("buckets: %v", rec.Buckets)
	}
	for i, b := range wantBuckets {
		if rec.Buckets[i] != b {
			t.Fatalf("buckets: %v, want %v", rec.Buckets, wantBuckets)
		}
	}
	if rec.DetailStart != 5 || rec.DetailEnd != 25 {
		t.Fatalf("detail range [%d,%d), want [5,25)", rec.DetailStart, rec.DetailEnd)
	}
	if rec.Removed != 2 || rec.Scanned != 2 { // b and c
		t.Fatalf("removed=%d scanned=%d, want 2/2", rec.Removed, rec.Scanned)
	}
	audit := s.Audit()
	if len(audit) != 1 || !reflect.DeepEqual(audit[0], rec) {
		t.Fatalf("audit log: %+v", audit)
	}
}

func TestCorrectionCanReuseRetractedIDs(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{ev("a", 1, 1)})
	rec := s.Correct(0, 10, []Event{ev("a", 2, 9)}) // same ID, corrected value
	if rec.Removed != 1 || rec.Added != 1 {
		t.Fatalf("record: %+v", rec)
	}
	r := s.Query(0, 10)
	if r.Count != 1 || r.Sum != 9 || r.First.TS != 2 {
		t.Fatalf("after ID reuse: %+v", r.Summary)
	}
}

func TestEmptyAndInvalidRanges(t *testing.T) {
	s := NewStore(10)
	r := s.Query(0, 100)
	if r.Present || r.Count != 0 || !math.IsNaN(r.Mean()) {
		t.Fatalf("empty store: %+v mean=%v", r.Summary, r.Mean())
	}
	s.Ingest([]Event{ev("a", 1, 1)})
	if r := s.Query(50, 50); r.Present || len(r.Buckets) != 0 {
		t.Fatalf("empty range: %+v", r)
	}
	if r := s.Query(50, 10); r.Present || len(r.Buckets) != 0 {
		t.Fatalf("inverted range: %+v", r)
	}
}
