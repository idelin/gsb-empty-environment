package hexgrid

import (
	"errors"
	"testing"
)

func beijingCell(t *testing.T) Cell {
	t.Helper()
	c, err := LatLngToCell(39.9042, 116.4074, 8)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestAcceptanceKRing3 is the acceptance boundary: a GPS coordinate
// encoded at resolution 8, its radius-3 neighborhood must contain all
// 37 cells forming a continuous closed cover with no holes.
func TestAcceptanceKRing3(t *testing.T) {
	center := beijingCell(t)
	ring, err := KRing(center, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ring) != 37 {
		t.Fatalf("k-ring(3) has %d cells, want 37", len(ring))
	}
	if err := AssertKRing(center, 3); err != nil {
		t.Fatalf("k-ring invariant battery failed: %v", err)
	}
	if err := VerifyClosedCoverage(ring); err != nil {
		t.Fatalf("coverage not continuous/closed: %v", err)
	}
}

func TestKRingSizes(t *testing.T) {
	center := beijingCell(t)
	for k, want := range map[int]int{0: 1, 1: 7, 2: 19, 3: 37, 4: 61, 5: 91} {
		ring, err := KRing(center, k)
		if err != nil {
			t.Fatal(err)
		}
		if len(ring) != want {
			t.Fatalf("k=%d: %d cells, want %d", k, len(ring), want)
		}
		if ring[0] != center {
			t.Fatalf("k=%d: first cell is not the center", k)
		}
	}
}

func TestKRingInvariantBattery(t *testing.T) {
	center := beijingCell(t)
	for k := 0; k <= 4; k++ {
		if err := AssertKRing(center, k); err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
	}
}

func TestKRingErrors(t *testing.T) {
	center := beijingCell(t)
	if _, err := KRing(center, -1); err == nil {
		t.Fatal("negative radius accepted")
	}
	if _, err := KRing(Cell(0), 1); err == nil {
		t.Fatal("invalid center cell accepted")
	}
	// A res-1 cell only covers a radius-1 ball; a radius-5 ring must
	// leave the base-cell hierarchy and fail loudly.
	edge := MustCell(0, 1, []uint8{0})
	if _, err := KRing(edge, 5); !errors.Is(err, ErrFaceBoundary) {
		t.Fatalf("want ErrFaceBoundary, got %v", err)
	}
}

func TestNeighbors(t *testing.T) {
	center := beijingCell(t)
	nbs, err := Neighbors(center)
	if err != nil {
		t.Fatal(err)
	}
	if len(nbs) != 6 {
		t.Fatalf("%d neighbors, want 6", len(nbs))
	}
	seen := map[Cell]bool{center: true}
	for _, nb := range nbs {
		if seen[nb] {
			t.Fatalf("duplicate/self neighbor %s", nb)
		}
		seen[nb] = true
		d, err := RingDistance(center, nb)
		if err != nil {
			t.Fatal(err)
		}
		if d != 1 {
			t.Fatalf("neighbor at distance %d", d)
		}
		// Adjacency is symmetric.
		back, err := Neighbors(nb)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, b := range back {
			if b == center {
				found = true
			}
		}
		if !found {
			t.Fatalf("neighbor %s does not link back", nb)
		}
	}
}

func TestRingDistance(t *testing.T) {
	center := beijingCell(t)
	ring, err := KRing(center, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Cells are emitted as center, ring 1, ring 2, ring 3.
	wantDist := append([]int{0},
		append(append(rep(1, 6), rep(2, 12)...), rep(3, 18)...)...)
	for i, c := range ring {
		d, err := RingDistance(center, c)
		if err != nil {
			t.Fatal(err)
		}
		if d != wantDist[i] {
			t.Fatalf("cell %d at distance %d, want %d", i, d, wantDist[i])
		}
	}
	// Cross-base-cell / cross-resolution distances must fail.
	other := MustCell(3, 8, []uint8{0, 0, 0, 0, 0, 0, 0, 0})
	if _, err := RingDistance(center, other); err == nil {
		t.Fatal("cross-base-cell distance accepted")
	}
	coarse := MustCell(17, 7, []uint8{1, 0, 1, 0, 4, 6, 3})
	if _, err := RingDistance(center, coarse); err == nil {
		t.Fatal("cross-resolution distance accepted")
	}
}

func rep(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestVerifyClosedCoverageRejectsHoles(t *testing.T) {
	center := beijingCell(t)
	ring, err := KRing(center, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Remove the center: the remaining 18 cells form a ring with a
	// hole, so the boundary splits into two loops.
	holed := ring[1:]
	if err := VerifyClosedCoverage(holed); err == nil {
		t.Fatal("holed coverage accepted")
	}
	// A single cell is a valid closed cover.
	if err := VerifyClosedCoverage(ring[:1]); err != nil {
		t.Fatalf("single cell coverage rejected: %v", err)
	}
	// Mixed resolutions must be rejected.
	mixed := append([]Cell{}, ring[:1]...)
	coarse, _ := ring[0].Parent()
	mixed = append(mixed, coarse)
	if err := VerifyClosedCoverage(mixed); err == nil {
		t.Fatal("mixed-resolution coverage accepted")
	}
	if err := VerifyClosedCoverage(nil); err == nil {
		t.Fatal("empty coverage accepted")
	}
}
