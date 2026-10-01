package agg

import (
	"math/rand"
	"testing"
)

// TestCrossLayerMatchesFullSort replays random small streams with random
// corrections and checks every version of every query against a full-sort
// reference computed from the visible details.
func TestCrossLayerMatchesFullSort(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 60; trial++ {
		s := NewStore(1 + int64(r.Intn(5)))
		n := 1 + r.Intn(60)
		events := make([]Event, 0, n)
		for i := 0; i < n; i++ {
			events = append(events, Event{
				ID:    string(rune('a'+i%26)) + string(rune('A'+i/26)),
				TS:    int64(r.Intn(40)),
				Value: float64(r.Intn(200)) / 2,
			})
		}
		s.Ingest(events)
		// A few random corrections and deletions to create versions.
		for k := 0; k < 3; k++ {
			lo := int64(r.Intn(40))
			hi := lo + int64(r.Intn(10))
			if r.Intn(2) == 0 {
				s.Delete(lo, hi)
			} else {
				s.Correct(lo, hi, []Event{{
					ID:    "fix" + string(rune('0'+k)) + string(rune('0'+trial)),
					TS:    lo,
					Value: float64(r.Intn(200)) / 2,
				}})
			}
		}
		// Check every snapshot version against the reference.
		for v := 0; v <= s.Version(); v++ {
			for q := 0; q < 4; q++ {
				lo := int64(r.Intn(45))
				hi := lo + int64(r.Intn(15))
				res := s.QueryAt(lo, hi, v)
				want := Reference(s.Details(lo, hi, v), lo, hi)
				if err := compareSummary(want, res.Summary); err != nil {
					t.Fatalf("trial %d version %d range [%d,%d): %v", trial, v, lo, hi, err)
				}
				if err := s.Validate(res); err != nil {
					t.Fatalf("trial %d version %d range [%d,%d): %v", trial, v, lo, hi, err)
				}
			}
		}
	}
}

// TestEdgeDetailsInternalSummaries builds a range that cuts two buckets and
// fully covers the middle one, then checks the cross-layer combination.
func TestEdgeDetailsInternalSummaries(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{
		ev("e1", 0, 1), ev("e2", 5, 2), ev("e3", 9, 3), // bucket 0 (edge)
		ev("m1", 10, 10), ev("m2", 15, 20), // bucket 1 (internal)
		ev("x1", 20, 4), ev("x2", 25, 5), ev("x3", 29, 6), // bucket 2 (edge)
	})
	r := s.Query(5, 25) // cuts bucket 0 at 5 and bucket 2 at 25
	// e2,e3,m1,m2,x1 are inside; x2 at ts=25 is excluded (end-exclusive).
	if r.Count != 5 || r.Sum != 39 {
		t.Fatalf("count=%d sum=%v, want 5/39", r.Count, r.Sum)
	}
	if r.First.ID != "e2" || r.Last.ID != "x1" {
		t.Fatalf("first=%+v last=%+v", r.First, r.Last)
	}
	if r.Max.ID != "m2" || r.Min.ID != "e2" {
		t.Fatalf("min=%+v max=%+v", r.Min, r.Max)
	}
	if err := s.Validate(r); err != nil {
		t.Fatal(err)
	}
	// Per-bucket counts add up exactly once each.
	total := 0
	for _, b := range r.Buckets {
		total += b.Count
	}
	if total != r.Count {
		t.Fatalf("bucket counts %d != total %d", total, r.Count)
	}
}

// TestSnapshotSequence verifies that every intermediate snapshot stays
// queryable across a sequence of corrections.
func TestSnapshotSequence(t *testing.T) {
	s := NewStore(10)
	s.Ingest([]Event{ev("a", 1, 1), ev("b", 2, 2), ev("c", 3, 3)})
	s.Correct(1, 3, []Event{ev("a2", 1, 10)}) // v2
	s.Delete(2, 4)                            // v3

	type want struct {
		count int
		sum   float64
		maxID string
	}
	cases := []struct {
		version int
		want
	}{
		{1, want{3, 6, "c"}},
		{2, want{2, 13, "a2"}},
		{3, want{1, 10, "a2"}},
	}
	for _, tc := range cases {
		r := s.QueryAt(0, 10, tc.version)
		if r.Count != tc.count || r.Sum != tc.sum || r.Max.ID != tc.maxID {
			t.Errorf("v%d: count=%d sum=%v max=%s, want %d/%v/%s",
				tc.version, r.Count, r.Sum, r.Max.ID, tc.count, tc.sum, tc.maxID)
		}
		if err := s.Validate(r); err != nil {
			t.Errorf("v%d: %v", tc.version, err)
		}
	}
}

func TestDetailsAreSorted(t *testing.T) {
	s := NewStore(5)
	s.Ingest([]Event{ev("c", 7, 1), ev("a", 7, 2), ev("b", 3, 3)})
	d := s.Details(0, 100, s.Version())
	if len(d) != 3 || d[0].ID != "b" || d[1].ID != "a" || d[2].ID != "c" {
		t.Fatalf("details order: %+v", d)
	}
}
