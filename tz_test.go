package chronos

import "testing"

func TestOverlapResolutionCandidates(t *testing.T) {
	v1 := SyntheticIslandV1()
	// 2021-10-31 02:30 local happens twice: at +2h and at +1h.
	wallSec := daysFromCivil(2021, 10, 31)*86400 + 2*3600 + 30*60
	cands := v1.ResolveWall(wallSec)
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(cands))
	}
	if cands[0].OffsetSec != 7200 || cands[0].Fold != 0 {
		t.Fatalf("candidate 0 = %+v", cands[0])
	}
	if cands[1].OffsetSec != 3600 || cands[1].Fold != 1 {
		t.Fatalf("candidate 1 = %+v", cands[1])
	}
	// The two instants are exactly one hour apart.
	i0 := wallSec - int64(cands[0].OffsetSec)
	i1 := wallSec - int64(cands[1].OffsetSec)
	if i1-i0 != 3600 {
		t.Fatalf("candidates %d and %d not 1h apart", i0, i1)
	}
}

func TestGapRejected(t *testing.T) {
	v1 := SyntheticIslandV1()
	// Spring forward 2021-03-28: local 02:00..03:00 does not exist.
	if _, err := NewWallClock(2021, 3, 28, 2, 30, 0, 0, PrecSecond, v1, 0); err != ErrGap {
		t.Fatalf("expected ErrGap, got %v", err)
	}
}

func TestFoldOutOfRangeRejected(t *testing.T) {
	v1 := SyntheticIslandV1()
	if _, err := NewWallClock(2021, 10, 31, 2, 30, 0, 0, PrecSecond, v1, 2); err == nil {
		t.Fatal("expected fold out of range error")
	}
	// Non-ambiguous time has exactly one candidate.
	if _, err := NewWallClock(2021, 1, 15, 12, 0, 0, 0, PrecSecond, v1, 1); err == nil {
		t.Fatal("expected fold out of range error for unique wall time")
	}
}

func TestAmbiguousWallResolvesToChosenInstant(t *testing.T) {
	v1 := SyntheticIslandV1()
	early, err := NewWallClock(2021, 10, 31, 2, 30, 0, 0, PrecSecond, v1, 0)
	if err != nil {
		t.Fatal(err)
	}
	late, err := NewWallClock(2021, 10, 31, 2, 30, 0, 0, PrecSecond, v1, 1)
	if err != nil {
		t.Fatal(err)
	}
	ie, err := early.Instant(v1)
	if err != nil {
		t.Fatal(err)
	}
	il, err := late.Instant(v1)
	if err != nil {
		t.Fatal(err)
	}
	if il.Sec()-ie.Sec() != 3600 {
		t.Fatalf("folds resolve %d s apart, want 3600", il.Sec()-ie.Sec())
	}
	all, err := early.Candidates(v1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 candidate instants, got %d", len(all))
	}
}

func TestUnknownTZVersionUnresolvable(t *testing.T) {
	v1 := SyntheticIslandV1()
	w, err := NewWallClock(2021, 1, 15, 12, 0, 0, 0, PrecSecond, v1, 0)
	if err != nil {
		t.Fatal(err)
	}
	empty := NewRegistry()
	if w.Resolvable(empty) {
		t.Fatal("unknown table version must be marked unresolvable")
	}
	if _, err := w.InstantFrom(empty); err == nil {
		t.Fatal("expected unresolvable error for unknown tz version")
	}
	reg := SyntheticRegistry()
	if !w.Resolvable(reg) {
		t.Fatal("registered version must be resolvable")
	}
}
