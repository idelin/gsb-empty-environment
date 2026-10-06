package tileindex

import (
	"math/big"
	"testing"
)

func mustFind(t *testing.T, s *Snapshot, z, x, y int) (Key, Region) {
	t.Helper()
	k, r, ok := s.Find(z, x, y)
	if !ok {
		t.Fatalf("Find(%d, %d, %d): expected hit, got miss", z, x, y)
	}
	return k, r
}

func mustMiss(t *testing.T, s *Snapshot, z, x, y int) {
	t.Helper()
	if k, _, ok := s.Find(z, x, y); ok {
		t.Fatalf("Find(%d, %d, %d): expected miss, got %+v", z, x, y, k)
	}
}

func ratEq(got *big.Rat, num, den int64) bool {
	return got.Cmp(big.NewRat(num, den)) == 0
}

func checkRegion(t *testing.T, r Region, u0n, u0d, v0n, v0d, u1n, u1d, v1n, v1d int64) {
	t.Helper()
	if !ratEq(r.U0, u0n, u0d) || !ratEq(r.V0, v0n, v0d) ||
		!ratEq(r.U1, u1n, u1d) || !ratEq(r.V1, v1n, v1d) {
		t.Fatalf("region = (%v,%v)-(%v,%v), want (%d/%d,%d/%d)-(%d/%d,%d/%d)",
			r.U0, r.V0, r.U1, r.V1, u0n, u0d, v0n, v0d, u1n, u1d, v1n, v1d)
	}
}

func TestFindExactHit(t *testing.T) {
	s := NewIndex([]Key{{3, 5, 2}}).Snapshot()
	k, r := mustFind(t, s, 3, 5, 2)
	if k != (Key{3, 5, 2}) {
		t.Fatalf("tile = %+v", k)
	}
	checkRegion(t, r, 0, 1, 0, 1, 1, 1, 1, 1)
}

// Boundary tiles: last column/row of a level map to the edge of the
// ancestor texture via the scaling formula u = (x mod 2^k)/2^k.
func TestAncestorRegionBoundaryTiles(t *testing.T) {
	s := NewIndex([]Key{{2, 3, 0}, {2, 0, 3}}).Snapshot()

	// z=5, x=31 (max column), y=0 (min row); k=3, scale=8.
	k, r := mustFind(t, s, 5, 31, 0)
	if k != (Key{2, 3, 0}) {
		t.Fatalf("ancestor = %+v, want {2 3 0}", k)
	}
	checkRegion(t, r, 7, 8, 0, 1, 1, 1, 1, 8)

	// Opposite corner: x=0, y=31.
	k, r = mustFind(t, s, 5, 0, 31)
	if k != (Key{2, 0, 3}) {
		t.Fatalf("ancestor = %+v, want {2 0 3}", k)
	}
	checkRegion(t, r, 0, 1, 7, 8, 1, 8, 1, 1)
}

func TestAncestorRegionDeepLevel(t *testing.T) {
	s := NewIndex([]Key{{0, 0, 0}}).Snapshot()
	// z=10, x=513, y=256; k=10, scale=1024.
	k, r := mustFind(t, s, 10, 513, 256)
	if k != (Key{0, 0, 0}) {
		t.Fatalf("ancestor = %+v", k)
	}
	checkRegion(t, r, 513, 1024, 256, 1024, 514, 1024, 257, 1024)
}

func TestNearestAncestorWins(t *testing.T) {
	s := NewIndex([]Key{{0, 0, 0}, {2, 1, 1}}).Snapshot()
	k, r := mustFind(t, s, 4, 5, 6)
	if k != (Key{2, 1, 1}) {
		t.Fatalf("ancestor = %+v, want nearest {2 1 1}", k)
	}
	// k=2, scale=4: x mod 4 = 1, y mod 4 = 2.
	checkRegion(t, r, 1, 4, 2, 4, 2, 4, 3, 4)
}

// A neighbouring tile at the parent level must not be used as a parent.
func TestNeighborIsNotParent(t *testing.T) {
	s := NewIndex([]Key{{1, 0, 0}}).Snapshot()
	// (2,3,3) ancestors: (1,1,1) -> (0,0,0); (1,0,0) is a neighbor, not an ancestor.
	mustMiss(t, s, 2, 3, 3)
	// But its true ancestor chain resolves.
	s2 := NewIndex([]Key{{1, 1, 1}}).Snapshot()
	k, _ := mustFind(t, s2, 2, 3, 3)
	if k != (Key{1, 1, 1}) {
		t.Fatalf("ancestor = %+v, want {1 1 1}", k)
	}
}

func TestNoAncestorAvailable(t *testing.T) {
	s := NewIndex([]Key{{3, 0, 0}}).Snapshot()
	mustMiss(t, s, 4, 15, 15) // chain never hits (3,0,0)
	mustMiss(t, NewIndex(nil).Snapshot(), 0, 0, 0)
}

func TestInvalidCoordinatesRejected(t *testing.T) {
	s := NewIndex([]Key{{0, 0, 0}, {2, 3, 3}}).Snapshot()
	mustMiss(t, s, -1, 0, 0)  // negative level
	mustMiss(t, s, 2, -1, 0)  // negative x
	mustMiss(t, s, 2, 0, -1)  // negative y
	mustMiss(t, s, 2, 4, 0)   // x out of range (2^2 = 4)
	mustMiss(t, s, 2, 0, 4)   // y out of range
	mustMiss(t, s, 0, 1, 0)   // root level only has (0,0,0)
}

// Update swaps the set atomically; old snapshots keep the old data.
func TestAtomicUpdateSnapshots(t *testing.T) {
	idx := NewIndex([]Key{{1, 0, 0}})
	old := idx.Snapshot()

	idx.Update([]Key{{1, 1, 1}})
	cur := idx.Snapshot()

	// Old snapshot still resolves via the old set.
	k, _ := mustFind(t, old, 2, 1, 0)
	if k != (Key{1, 0, 0}) {
		t.Fatalf("old snapshot ancestor = %+v, want {1 0 0}", k)
	}
	mustMiss(t, old, 2, 3, 3)

	// New snapshot reflects the update.
	k, _ = mustFind(t, cur, 2, 3, 3)
	if k != (Key{1, 1, 1}) {
		t.Fatalf("new snapshot ancestor = %+v, want {1 1 1}", k)
	}
	mustMiss(t, cur, 2, 1, 0)
}

func TestRegionStaysWithinUnitSquare(t *testing.T) {
	s := NewIndex([]Key{{0, 0, 0}}).Snapshot()
	zero, one := big.NewRat(0, 1), big.NewRat(1, 1)
	for z := 1; z <= 12; z++ {
		n := 1 << uint(z)
		for _, x := range []int{0, n / 2, n - 1} {
			for _, y := range []int{0, n / 2, n - 1} {
				_, r, ok := s.Find(z, x, y)
				if !ok {
					t.Fatalf("Find(%d,%d,%d) missed root", z, x, y)
				}
				for _, e := range []*big.Rat{r.U0, r.V0, r.U1, r.V1} {
					if e.Cmp(zero) < 0 || e.Cmp(one) > 0 {
						t.Fatalf("Find(%d,%d,%d) edge %v outside [0,1]", z, x, y, e)
					}
				}
				if r.U0.Cmp(r.U1) >= 0 || r.V0.Cmp(r.V1) >= 0 {
					t.Fatalf("Find(%d,%d,%d) degenerate region", z, x, y)
				}
			}
		}
	}
}
