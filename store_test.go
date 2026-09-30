package telemetry

import (
	"math"
	"sort"
	"testing"
)

// reference is the brute-force oracle: fully sort the small stream by
// (TS, ID) and aggregate by hand.
func reference(events []Event, start, end int64) (count int, sum, min, max float64, first, last Event) {
	sorted := append([]Event(nil), events...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].TS != sorted[j].TS {
			return sorted[i].TS < sorted[j].TS
		}
		return sorted[i].ID < sorted[j].ID
	})
	for _, e := range sorted {
		if e.TS < start || e.TS >= end {
			continue
		}
		if count == 0 {
			min, max = e.Value, e.Value
			first, last = e, e
		}
		count++
		sum += e.Value
		if e.Value < min {
			min = e.Value
		}
		if e.Value > max {
			max = e.Value
		}
		last = e
	}
	return count, sum, min, max, first, last
}

func TestBucketBoundaries(t *testing.T) {
	s := NewStore(60)
	s.Ingest([]Event{
		{ID: "a", TS: 0, Value: 1},
		{ID: "b", TS: 59, Value: 2},
		{ID: "c", TS: 60, Value: 3}, // exactly on the boundary: opens bucket 1
		{ID: "d", TS: -1, Value: 4}, // negative: floor division -> bucket -1
	})
	r := s.Query(-60, 120)
	want := map[int64]int{-1: 1, 0: 2, 1: 1}
	if len(r.Buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(r.Buckets))
	}
	for _, b := range r.Buckets {
		if !b.Present {
			t.Fatalf("bucket %d should be present", b.Bucket)
		}
		if b.Summary.Count != want[b.Bucket] {
			t.Fatalf("bucket %d count = %d, want %d", b.Bucket, b.Summary.Count, want[b.Bucket])
		}
	}
}

func TestGapVsZeroValue(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{{ID: "z", TS: 5, Value: 0}}) // bucket 0 holds a real zero
	r := s.Query(0, 30)
	if len(r.Buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(r.Buckets))
	}
	if !r.Buckets[0].Present || r.Buckets[0].Summary.Sum != 0 || r.Buckets[0].Summary.Count != 1 {
		t.Fatalf("bucket 0 must be present with sum 0 count 1: %+v", r.Buckets[0])
	}
	for _, b := range r.Buckets[1:] {
		if b.Present {
			t.Fatalf("bucket %d is a gap and must not be present", b.Bucket)
		}
	}
	if r.Count != 1 || r.Sum != 0 {
		t.Fatalf("totals count=%d sum=%v, want 1/0", r.Count, r.Sum)
	}
}

func TestOverlappingBatchDedup(t *testing.T) {
	s := NewStore(60)
	batch := []Event{
		{ID: "a", TS: 1, Value: 10},
		{ID: "b", TS: 2, Value: 20},
		{ID: "b", TS: 2, Value: 20}, // duplicate inside the batch
	}
	added, dups := s.Ingest(batch)
	if added != 2 || dups != 1 {
		t.Fatalf("first ingest added=%d dups=%d, want 2/1", added, dups)
	}
	added, dups = s.Ingest(append([]Event{}, batch...)) // overlapping redelivery
	if added != 0 || dups != 3 {
		t.Fatalf("redelivery added=%d dups=%d, want 0/3", added, dups)
	}
	r := s.Query(0, 60)
	if r.Count != 2 || r.Sum != 30 {
		t.Fatalf("count=%d sum=%v, want 2/30", r.Count, r.Sum)
	}
}

func TestMeanIsSumOverCount(t *testing.T) {
	s := NewStore(10)
	// bucket 0: one event of 100; bucket 1: nine events of 1.
	// average-of-averages would give 50.5; sum/count gives 109/10 = 10.9.
	s.Ingest([]Event{{ID: "big", TS: 1, Value: 100}})
	var small []Event
	for i := 0; i < 9; i++ {
		small = append(small, Event{ID: string(rune('a' + i)), TS: int64(10 + i), Value: 1})
	}
	s.Ingest(small)
	r := s.Query(0, 20)
	mean, ok := r.Mean()
	if !ok {
		t.Fatal("mean should be available")
	}
	if math.Abs(mean-10.9) > 1e-9 {
		t.Fatalf("mean = %v, want 10.9 (sum/count, not average of averages)", mean)
	}
}

func TestPeakRetractionFindsNextMax(t *testing.T) {
	s := NewStore(60)
	s.Ingest([]Event{
		{ID: "low", TS: 10, Value: 5},
		{ID: "mid", TS: 20, Value: 50},
		{ID: "peak", TS: 30, Value: 500},
	})
	r := s.Query(0, 60)
	if r.Max.ID != "peak" || r.Max.Value != 500 {
		t.Fatalf("max evidence = %+v, want peak/500", r.Max)
	}
	s.Delete(25, 35) // retract only the peak
	r = s.Query(0, 60)
	if r.Max.ID != "mid" || r.Max.Value != 50 {
		t.Fatalf("after retraction max = %+v, want mid/50 (next largest)", r.Max)
	}
	if r.Count != 2 || r.Sum != 55 {
		t.Fatalf("count=%d sum=%v, want 2/55", r.Count, r.Sum)
	}
}

func TestSinglePointPeakSurvives(t *testing.T) {
	s := NewStore(60)
	s.Ingest([]Event{
		{ID: "solo", TS: 10, Value: 7},
		{ID: "other", TS: 100, Value: 1},
	})
	s.Delete(90, 110) // delete the other bucket entirely
	r := s.Query(0, 120)
	if r.Max.ID != "solo" || r.Max.Value != 7 || r.Count != 1 {
		t.Fatalf("single-point peak vanished: %+v count=%d", r.Max, r.Count)
	}
}

func TestVersionedCorrectionAndOldSnapshot(t *testing.T) {
	s := NewStore(60)
	s.Ingest([]Event{
		{ID: "x", TS: 10, Value: 1},
		{ID: "y", TS: 20, Value: 2},
	})
	v1 := s.Version()
	s.Correct(0, 60, []Event{{ID: "z", TS: 15, Value: 100}})

	cur := s.Query(0, 60)
	if cur.Count != 1 || cur.Sum != 100 || cur.Max.ID != "z" {
		t.Fatalf("current: count=%d sum=%v max=%+v", cur.Count, cur.Sum, cur.Max)
	}
	old := s.QueryAsOf(0, 60, v1)
	if old.Count != 2 || old.Sum != 3 || old.Max.ID != "y" {
		t.Fatalf("snapshot v%d: count=%d sum=%v max=%+v", v1, old.Count, old.Sum, old.Max)
	}
}

func TestDeleteIntervalVersioned(t *testing.T) {
	s := NewStore(60)
	s.Ingest([]Event{{ID: "a", TS: 5, Value: 1}, {ID: "b", TS: 65, Value: 2}})
	v1 := s.Version()
	s.Delete(0, 60)
	if r := s.Query(0, 120); r.Count != 1 || r.Max.ID != "b" {
		t.Fatalf("after delete: %+v", r)
	}
	if r := s.QueryAsOf(0, 120, v1); r.Count != 2 {
		t.Fatalf("old snapshot should still have 2 events, got %d", r.Count)
	}
}

func TestFirstLastTieBreakByID(t *testing.T) {
	s := NewStore(60)
	s.Ingest([]Event{
		{ID: "b", TS: 10, Value: 1},
		{ID: "a", TS: 10, Value: 2}, // same TS, smaller ID wins "first"
		{ID: "y", TS: 20, Value: 3},
		{ID: "z", TS: 20, Value: 4}, // same TS, larger ID wins "last"
	})
	r := s.Query(0, 60)
	if r.First.ID != "a" {
		t.Fatalf("first = %s, want a (TS tie broken by ID)", r.First.ID)
	}
	if r.Last.ID != "z" {
		t.Fatalf("last = %s, want z (TS tie broken by ID)", r.Last.ID)
	}
}

func TestCrossLayerMatchesSortedReference(t *testing.T) {
	s := NewStore(10)
	events := Generate(42, 60, 3, 3) // spans buckets, lands on edges
	s.Ingest(events)
	// range deliberately cuts through the first and last buckets
	start, end := int64(7), int64(178)
	r := s.Query(start, end)

	count, sum, min, max, first, last := reference(events, start, end)
	if r.Count != count || math.Abs(r.Sum-sum) > 1e-9 {
		t.Fatalf("count/sum = %d/%v, reference %d/%v", r.Count, r.Sum, count, sum)
	}
	if r.Min.Value != min || r.Max.Value != max {
		t.Fatalf("min/max = %v/%v, reference %v/%v", r.Min.Value, r.Max.Value, min, max)
	}
	if r.First.ID != first.ID || r.Last.ID != last.ID {
		t.Fatalf("first/last = %s/%s, reference %s/%s", r.First.ID, r.Last.ID, first.ID, last.ID)
	}

	// edge buckets must be detail-computed, internal ones summary-served,
	// and per-bucket counts must sum to the total (no double counting).
	if !r.Buckets[0].Partial || !r.Buckets[len(r.Buckets)-1].Partial {
		t.Fatal("edge buckets should be marked partial")
	}
	total := 0
	for i, b := range r.Buckets {
		if i > 0 && i < len(r.Buckets)-1 && b.Partial {
			t.Fatalf("internal bucket %d wrongly marked partial", b.Bucket)
		}
		if b.Present {
			total += b.Summary.Count
		}
	}
	if total != r.Count {
		t.Fatalf("bucket counts sum to %d, total is %d (double count or loss)", total, r.Count)
	}

	// historical path must agree with the reference too
	h := s.QueryAsOf(start, end, s.Version())
	if h.Count != count || math.Abs(h.Sum-sum) > 1e-9 || h.First.ID != first.ID || h.Last.ID != last.ID {
		t.Fatal("QueryAsOf disagrees with sorted reference")
	}
}

func TestAuditRecordsBucketsAndDetailRange(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{
		{ID: "a", TS: 1, Value: 1},
		{ID: "b", TS: 12, Value: 2},
		{ID: "c", TS: 14, Value: 3},
	})
	s.Correct(10, 20, []Event{{ID: "d", TS: 15, Value: 9}})
	s.Delete(0, 5)

	audit := s.Audit()
	if len(audit) != 3 {
		t.Fatalf("expected 3 audit entries, got %d", len(audit))
	}
	corr := audit[1]
	if corr.Op != "correct" || corr.Version != 2 {
		t.Fatalf("unexpected correction entry: %+v", corr)
	}
	if len(corr.Buckets) != 1 || corr.Buckets[0] != 1 {
		t.Fatalf("correction should touch bucket [1], got %v", corr.Buckets)
	}
	if corr.DetailLo != 15 || corr.DetailHi != 15 || corr.DetailCount != 1 {
		t.Fatalf("correction detail range = [%d,%d] x%d, want [15,15] x1",
			corr.DetailLo, corr.DetailHi, corr.DetailCount)
	}
	del := audit[2]
	if del.Op != "delete" || len(del.Buckets) != 1 || del.Buckets[0] != 0 {
		t.Fatalf("unexpected delete entry: %+v", del)
	}
}

func TestFixtureReplayDeterministic(t *testing.T) {
	a, b := NewStore(60), NewStore(60)
	Replay(a, SampleScript())
	Replay(b, SampleScript())
	ra, rb := a.Query(1_000, 1_400), b.Query(1_000, 1_400)
	if ra.Count != rb.Count || ra.Sum != rb.Sum || ra.Max != rb.Max {
		t.Fatal("fixture replay is not deterministic")
	}
	// the sample script retracts its peak; the next-largest must surface
	if ra.Max.ID == "peak" {
		t.Fatal("retracted peak still reported as max")
	}
	old := a.QueryAsOf(1_250, 1_260, 2)
	if old.Count == 0 || old.Max.ID != "peak" {
		t.Fatalf("old snapshot should still see the peak: %+v", old.Max)
	}
}
