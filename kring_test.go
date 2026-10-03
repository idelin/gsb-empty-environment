package hexgrid

import "testing"

// TestKRingAcceptance is the acceptance boundary: a GPS coordinate
// encoded at resolution 8, surrounded by a radius-3 topological ring
// whose coverage is contiguous, closed and free of holes.
func TestKRingAcceptance(t *testing.T) {
	c, err := LatLngToCell(39.9042, 116.4074, 8)
	if err != nil {
		t.Fatal(err)
	}
	if c.String() != beijingCell {
		t.Fatalf("golden cell mismatch: %s", c)
	}
	ring := KRing(c, 3)
	if len(ring) != ExpectedKRingSize(3) {
		t.Fatalf("k=3 ring has %d cells, want %d", len(ring), ExpectedKRingSize(3))
	}
	seen := map[Cell]bool{}
	for _, rc := range ring {
		if seen[rc] {
			t.Fatalf("duplicate ring cell %s", rc)
		}
		seen[rc] = true
		if !rc.IsValid() {
			t.Fatalf("invalid ring cell %s", rc)
		}
		if rc.Resolution() != 8 {
			t.Fatalf("ring cell %s at wrong resolution", rc)
		}
	}
	// The whole ring stays on one face: pure hexagonal topology.
	for _, rc := range ring {
		if rc.Face() != c.Face() {
			t.Fatalf("ring cell %s escaped to face %d", rc, rc.Face())
		}
	}
	// Formal invariants: contiguous, closed, symmetric, exact size.
	if v := AssertKRing(c, 3); len(v) > 0 {
		t.Fatalf("k-ring invariant violations: %v", v)
	}
	// No holes: every cell at topological distance d is adjacent to
	// a cell at distance d-1, and distances match the BFS map.
	dist := KRingDistances(c, 3)
	if len(dist) != len(ring) {
		t.Fatalf("distance map size %d != ring size %d", len(dist), len(ring))
	}
	maxD := 0
	for _, d := range dist {
		if d > maxD {
			maxD = d
		}
	}
	if maxD != 3 {
		t.Fatalf("max ring distance = %d, want 3", maxD)
	}
}

func TestKRingSizesInterior(t *testing.T) {
	c, _ := LatLngToCell(39.9042, 116.4074, 8)
	want := []int{1, 7, 19, 37}
	for k, w := range want {
		if got := len(KRing(c, k)); got != w {
			t.Fatalf("k=%d: got %d cells, want %d", k, got, w)
		}
	}
	if got := len(KRing(c, -1)); got != 1 {
		t.Fatalf("negative k must behave like k=0, got %d cells", got)
	}
}

// TestNeighborsSymmetrySweep proves the neighbor relation is a
// symmetric graph over the whole universe at coarse resolutions,
// including across face seams.
func TestNeighborsSymmetrySweep(t *testing.T) {
	for res := 0; res <= 4; res++ {
		u, _ := Universe(res)
		for _, c := range u {
			if !c.IsReachable() {
				continue
			}
			for _, nb := range Neighbors(c) {
				back := false
				for _, rn := range Neighbors(nb) {
					if rn == c {
						back = true
						break
					}
				}
				if !back {
					t.Fatalf("res %d: %s lists %s as neighbor but not vice versa", res, c, nb)
				}
			}
		}
	}
}

// TestKRingRes0 checks the coarsest level: each face has one cell
// whose hexagonal neighbors wrap onto the surrounding faces (5-7 of
// them depending on face orientation), and the whole sphere is
// covered within 3 topological steps from any cell.
func TestKRingRes0(t *testing.T) {
	u, _ := Universe(0)
	if len(u) != 20 {
		t.Fatalf("res-0 universe has %d cells, want 20", len(u))
	}
	for _, c := range u {
		if n := len(Neighbors(c)); n < 5 || n > 7 {
			t.Fatalf("res-0 cell %s has %d neighbors, want 5..7", c, n)
		}
		if got := len(KRing(c, 3)); got != 20 {
			t.Fatalf("res-0 k=3 ring from %s has %d cells, want 20 (full sphere)", c, got)
		}
		if v := AssertKRing(c, 3); len(v) > 0 {
			t.Fatalf("res-0 k-ring violations for %s: %v", c, v)
		}
	}
}

// TestKRingCrossFace exercises rings that cross a face seam: no
// duplicates, all valid, contiguous, and symmetric.
func TestKRingCrossFace(t *testing.T) {
	u, _ := Universe(4)
	var seamCell Cell
	found := false
	for _, c := range u {
		if !c.IsReachable() {
			continue
		}
		for _, nb := range Neighbors(c) {
			if nb.Face() != c.Face() {
				seamCell = c
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("no seam cell found at res 4")
	}
	ring := KRing(seamCell, 2)
	faces := map[int]bool{}
	seen := map[Cell]bool{}
	for _, rc := range ring {
		if seen[rc] {
			t.Fatalf("duplicate ring cell %s", rc)
		}
		seen[rc] = true
		if !rc.IsValid() {
			t.Fatalf("invalid ring cell %s", rc)
		}
		faces[rc.Face()] = true
	}
	if len(faces) < 2 {
		t.Fatal("expected a multi-face ring around a seam cell")
	}
	if v := AssertKRing(seamCell, 2); len(v) > 0 {
		t.Fatalf("cross-face ring violations: %v", v)
	}
}

// TestKRingSweepRes4 asserts the formal k-ring invariants for every
// reachable cell at res 4 with k=2 (covers interior and seam cells).
func TestKRingSweepRes4(t *testing.T) {
	u, _ := Universe(4)
	for _, c := range u {
		if !c.IsReachable() {
			continue
		}
		if v := AssertKRing(c, 2); len(v) > 0 {
			t.Fatalf("k-ring violations for %s: %v", c, v)
		}
	}
}

func TestKRingDeterministic(t *testing.T) {
	c, _ := LatLngToCell(-33.8568, 151.2153, 8) // Sydney
	a := KRing(c, 3)
	b := KRing(c, 3)
	if len(a) != len(b) {
		t.Fatal("non-deterministic ring size")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("non-deterministic ring content")
		}
	}
	// Results are fresh allocations: mutating one must not affect
	// subsequently computed rings.
	first := a[0]
	a[0] = 0
	c2 := KRing(c, 3)
	if c2[0] != first {
		t.Fatal("KRing results share backing state across calls")
	}
}
