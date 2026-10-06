package bitemp

import "testing"

func mustGet(t *testing.T, s *Store, key string, vt int64, asOf uint64, want string, wantOK bool) {
	t.Helper()
	got, ok := s.Get(key, vt, asOf)
	if got != want || ok != wantOK {
		t.Errorf("Get(%q, t=%d, asOf=%d) = (%q, %v), want (%q, %v)",
			key, vt, asOf, got, ok, want, wantOK)
	}
}

// Corrections rewrite the past traceably: an old knowledge snapshot
// (asOf) must never see a later correction.
func TestCorrectionAndSnapshotIsolation(t *testing.T) {
	s := NewStore()
	s1 := s.Put("fx", "6.5", 100, 200) // seq 1
	s2 := s.Put("fx", "6.9", 150, 250) // seq 2: correction, overlaps and extends

	// Before the overlap the old value stands at every snapshot.
	mustGet(t, s, "fx", 120, s1, "6.5", true)
	mustGet(t, s, "fx", 120, s2, "6.5", true)
	// Inside the overlap the newer ingestion wins, but only for
	// snapshots that already know it.
	mustGet(t, s, "fx", 170, s1, "6.5", true)
	mustGet(t, s, "fx", 170, s2, "6.9", true)
	// The extension into the future is invisible to the old snapshot.
	mustGet(t, s, "fx", 220, s1, "", false)
	mustGet(t, s, "fx", 220, s2, "6.9", true)
	// Before any fact: nothing at any snapshot.
	mustGet(t, s, "fx", 50, s2, "", false)
}

// Overlapping facts: the greatest ingestion sequence wins.
func TestOverlapTakesMaxSeq(t *testing.T) {
	s := NewStore()
	s.Put("k", "a", 0, 100)
	s.Put("k", "b", 20, 60)
	s3 := s.Put("k", "c", 40, 80)

	mustGet(t, s, "k", 10, s3, "a", true)  // only a
	mustGet(t, s, "k", 30, s3, "b", true)  // a,b -> b
	mustGet(t, s, "k", 50, s3, "c", true)  // a,b,c -> c
	mustGet(t, s, "k", 70, s3, "c", true)  // a,c -> c
	mustGet(t, s, "k", 90, s3, "a", true)  // only a
}

// Deletes carry their own valid interval and only shadow that interval,
// and only for snapshots taken after the delete.
func TestDeleteHasValidInterval(t *testing.T) {
	s := NewStore()
	s.Put("k", "v", 0, 100)
	s2 := s.Put("k", "w", 150, 250)
	s3 := s.Delete("k", 40, 160) // seq 3

	mustGet(t, s, "k", 30, s3, "v", true)   // outside tombstone
	mustGet(t, s, "k", 50, s3, "", false)   // tombstoned
	mustGet(t, s, "k", 50, s2, "v", true)   // old snapshot unaffected
	mustGet(t, s, "k", 155, s3, "", false)  // tombstone shadows w too
	mustGet(t, s, "k", 155, s2, "w", true)  // ...but not retroactively
	mustGet(t, s, "k", 200, s3, "w", true)  // past tombstone's To
}

// A correction after a delete revives the key on its own interval.
func TestPutAfterDelete(t *testing.T) {
	s := NewStore()
	s.Put("k", "v", 0, 100)
	s.Delete("k", 0, 100)
	s4 := s.Put("k", "z", 20, 40)

	mustGet(t, s, "k", 30, s4, "z", true)
	mustGet(t, s, "k", 60, s4, "", false)
}

// Interval splitting: segments must be sorted, non-overlapping, and
// every segment's version list must be ascending in seq.
func TestSegmentInvariants(t *testing.T) {
	s := NewStore()
	s.Put("k", "a", 0, 100)
	s.Put("k", "b", 50, 150)
	s.Delete("k", 25, 75)
	s.Put("k", "c", 10, 20)

	segs := s.keys["k"].segs
	if len(segs) == 0 {
		t.Fatal("no segments")
	}
	for i, sg := range segs {
		if sg.from >= sg.to {
			t.Errorf("segment %d: empty or inverted [%d,%d)", i, sg.from, sg.to)
		}
		if i > 0 && segs[i-1].to > sg.from {
			t.Errorf("segments %d,%d overlap", i-1, i)
		}
		for j := 1; j < len(sg.vers); j++ {
			if sg.vers[j-1].seq >= sg.vers[j].seq {
				t.Errorf("segment %d: versions not ascending", i)
			}
		}
	}
	// Boundaries of every record must be segment boundaries.
	for _, b := range []int64{0, 10, 20, 25, 50, 75, 100, 150} {
		found := false
		for _, sg := range segs {
			if sg.from == b || sg.to == b {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("boundary %d not a segment edge", b)
		}
	}
}

func TestEmptyIntervalIgnored(t *testing.T) {
	s := NewStore()
	if seq := s.Put("k", "v", 50, 50); seq != 0 {
		t.Fatalf("empty interval consumed seq %d", seq)
	}
	if seq := s.Delete("k", 60, 40); seq != 0 {
		t.Fatalf("inverted interval consumed seq %d", seq)
	}
	mustGet(t, s, "k", 50, 1, "", false)
}
