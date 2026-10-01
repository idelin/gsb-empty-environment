package sim

import (
	"math/big"
	"testing"
)

func TestSeqExtendedPrecision(t *testing.T) {
	// Cross the 2^64 boundary: no wraparound with extended integers.
	base := SeqFromBig(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(10)))
	end := base.Add(20)
	if got := end.Sub(base).Int64(); got != 20 {
		t.Fatalf("end-base = %d, want 20", got)
	}
	if end.Cmp(base) <= 0 {
		t.Fatal("end must be greater than base across 2^64 boundary")
	}
	// Far beyond 64 bits.
	huge := SeqFromBig(new(big.Int).Lsh(big.NewInt(1), 100))
	if got := huge.Add(5).Sub(huge).Int64(); got != 5 {
		t.Fatalf("huge arithmetic broken: got %d", got)
	}
	if NewSeq(7).Add(3).Diff(NewSeq(7)) != 3 {
		t.Fatal("Diff broken")
	}
}

func TestSackSetOverlapCountedOnce(t *testing.T) {
	var s SackSet
	add := func(lo, hi uint64) { s.Add(Range{NewSeq(lo), NewSeq(hi)}) }
	add(0, 100)
	add(50, 150)  // overlaps [0,100)
	add(200, 300) // disjoint
	add(140, 200) // bridges: adjacent to both -> merges all into [0,300)
	add(75, 125)  // fully contained duplicate
	add(0, 300)   // exact duplicate of the union

	if got := s.Total().Int64(); got != 300 {
		t.Fatalf("union total = %d, want 300 (overlaps must count once)", got)
	}
	rs := s.Ranges()
	if len(rs) != 1 || !rs[0].Start.Eq(NewSeq(0)) || !rs[0].End.Eq(NewSeq(300)) {
		t.Fatalf("ranges = %v, want single [0,300)", rs)
	}
	// Idempotent: re-adding changes nothing.
	add(10, 290)
	if got := s.Total().Int64(); got != 300 {
		t.Fatalf("after re-add total = %d, want 300", got)
	}
}

func TestSackSetGaps(t *testing.T) {
	var s SackSet
	s.Add(Range{NewSeq(0), NewSeq(100)})
	s.Add(Range{NewSeq(200), NewSeq(300)})
	s.Add(Range{NewSeq(400), NewSeq(500)})

	gaps := s.Gaps(NewSeq(0), NewSeq(500))
	if len(gaps) != 2 {
		t.Fatalf("gaps = %v, want 2", gaps)
	}
	want := [][2]int64{{100, 200}, {300, 400}}
	for i, g := range gaps {
		if g.Start.Diff(NewSeq(0)) != uint64(want[i][0]) || g.End.Diff(NewSeq(0)) != uint64(want[i][1]) {
			t.Fatalf("gap %d = [%s,%s), want [%d,%d)", i, g.Start, g.End, want[i][0], want[i][1])
		}
	}
	// Window clipped: gaps only inside [lo,hi).
	gaps = s.Gaps(NewSeq(150), NewSeq(350))
	if len(gaps) != 2 || gaps[0].Start.Diff(NewSeq(0)) != 150 || gaps[0].End.Diff(NewSeq(0)) != 200 ||
		gaps[1].Start.Diff(NewSeq(0)) != 300 || gaps[1].End.Diff(NewSeq(0)) != 350 {
		t.Fatalf("clipped gaps = %v", gaps)
	}
	// Fully covered: no gaps.
	if g := s.Gaps(NewSeq(0), NewSeq(100)); len(g) != 0 {
		t.Fatalf("covered interval produced gaps: %v", g)
	}
}
