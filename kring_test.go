package hexgrid

import "testing"

// kRingDiskSize is the closed-form size of a filled hex disk of radius k.
func kRingDiskSize(k int) int { return 1 + 3*k*(k+1) }

func TestKRingSizes(t *testing.T) {
	origin := GeoToCell(39.9042, 116.4074, 8)
	for k, want := range map[int]int{0: 1, 1: 7, 2: 19, 3: 37, 4: 61} {
		cells := KRing(origin, k)
		if len(cells) != want || want != kRingDiskSize(k) {
			t.Errorf("KRing(k=%d) = %d cells, want %d", k, len(cells), want)
		}
	}
	if got := KRing(origin, -1); got != nil {
		t.Errorf("KRing(k=-1) = %v, want nil", got)
	}
}

func TestKRingAcceptanceRadius3(t *testing.T) {
	// Acceptance boundary: GPS -> res 8 hex string -> radius-3 ring,
	// continuous closed coverage with no holes.
	origin := GeoToCell(39.9042, 116.4074, 8)
	const k = 3
	cells := KRing(origin, k)

	if len(cells) != 37 {
		t.Fatalf("ring size = %d, want 37", len(cells))
	}
	if cells[0] != origin {
		t.Fatalf("first cell must be the origin, got %s", cells[0])
	}

	// All cells unique, valid, same resolution.
	uniq := make(map[Cell]struct{}, len(cells))
	for _, c := range cells {
		if !c.IsValid() {
			t.Fatalf("invalid cell in ring: %s", c)
		}
		if c.Resolution() != 8 {
			t.Fatalf("cell %s resolution = %d, want 8", c, c.Resolution())
		}
		if _, dup := uniq[c]; dup {
			t.Fatalf("duplicate cell in ring: %s", c)
		}
		uniq[c] = struct{}{}
	}

	// Ring-distance histogram must be exactly 1, 6, 12, 18: this proves
	// the disk is closed with no holes and no missing wedge.
	hist := map[int]int{}
	for _, c := range cells {
		d, ok := GridDistance(origin, c)
		if !ok {
			t.Fatalf("cell %s not distance-comparable to origin", c)
		}
		if d > k {
			t.Fatalf("cell %s at distance %d exceeds radius %d", c, d, k)
		}
		hist[d]++
	}
	for d, want := range map[int]int{0: 1, 1: 6, 2: 12, 3: 18} {
		if hist[d] != want {
			t.Fatalf("cells at distance %d = %d, want %d", d, hist[d], want)
		}
	}

	// Closure: every cell's 6 topological neighbors must lie inside the
	// radius-(k+1) disk; interior cells (distance < k) must have all 6
	// neighbors inside the radius-k set itself.
	_, _, oc := cellToCube(origin)
	inDisk := make(map[cubeCoord]bool, len(cells))
	for _, c := range cells {
		_, _, cc := cellToCube(c)
		inDisk[cc] = true
	}
	for _, c := range cells {
		_, _, cc := cellToCube(c)
		d := cubeDistance(oc, cc)
		if d >= k {
			continue
		}
		for _, dir := range cubeDirections {
			if !inDisk[cc.add(dir)] {
				t.Fatalf("hole: neighbor of distance-%d cell missing from disk", d)
			}
		}
	}
}

func TestKRingStaysOnFaceForInteriorPoint(t *testing.T) {
	// For a point well inside an icosahedron face, the whole radius-3
	// traversal is purely topological: no face-boundary fallback, so all
	// cells share the origin's base cell.
	origin := GeoToCell(39.9042, 116.4074, 8)
	for _, c := range KRing(origin, 3) {
		if c.BaseCell() != origin.BaseCell() {
			t.Fatalf("cell %s crossed to face %d from %d", c, c.BaseCell(), origin.BaseCell())
		}
	}
}

func TestKRingAcrossResolutions(t *testing.T) {
	for _, res := range []int{0, 1, 2, 3, 6, 10} {
		origin := GeoToCell(-33.8688, 151.2093, res)
		cells := KRing(origin, 2)
		// The BFS always visits exactly 19 lattice positions.
		if len(cells) != 19 {
			t.Fatalf("res %d: KRing(2) = %d cells, want 19", res, len(cells))
		}
		sameFace := true
		uniq := map[Cell]bool{}
		for _, c := range cells {
			if !c.IsValid() {
				t.Fatalf("res %d: invalid cell %s", res, c)
			}
			if c.BaseCell() != origin.BaseCell() {
				sameFace = false
			}
			uniq[c] = true
		}
		// When the disk stays on one icosahedron face (pure topological
		// traversal), all 19 cells must be distinct. At coarse
		// resolutions the disk may spill onto neighboring faces, where
		// multiple lattice positions can legitimately collapse onto the
		// same coarse cell (one face == one cell at res 0).
		if sameFace && len(uniq) != 19 {
			t.Fatalf("res %d: in-face disk has %d unique cells, want 19", res, len(uniq))
		}
	}
}

func TestGridDistance(t *testing.T) {
	origin := GeoToCell(39.9042, 116.4074, 8)
	if d, ok := GridDistance(origin, origin); !ok || d != 0 {
		t.Fatalf("self distance = %d, %v; want 0, true", d, ok)
	}
	ring := KRing(origin, 1)
	if d, ok := GridDistance(origin, ring[1]); !ok || d != 1 {
		t.Fatalf("neighbor distance = %d, %v; want 1, true", d, ok)
	}
	// Different resolutions are not comparable.
	if _, ok := GridDistance(origin, origin.Parent(4)); ok {
		t.Fatal("distance across resolutions must not be comparable")
	}
}
