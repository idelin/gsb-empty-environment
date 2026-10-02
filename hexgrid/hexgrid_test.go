package hexgrid

import (
	"sync"
	"testing"
)

var testGrid = NewGrid()

// TestEncodeGolden pins the hierarchical encoding of a known GPS fix.
func TestEncodeGolden(t *testing.T) {
	cell := testGrid.FromLatLon(39.9042, 116.4074, 8)
	if got, want := cell.String(), "2080558020"; got != want {
		t.Fatalf("golden encode mismatch: got %s want %s", got, want)
	}
	if cell.Resolution() != 8 {
		t.Fatalf("resolution = %d, want 8", cell.Resolution())
	}
}

// TestParseRoundTrip checks hex string <-> CellID round trips.
func TestParseRoundTrip(t *testing.T) {
	cell := testGrid.FromLatLon(-33.8688, 151.2093, 8) // Sydney
	parsed, err := ParseCellID(cell.String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed != cell {
		t.Fatalf("round trip mismatch: %s != %s", parsed, cell)
	}
	if _, err := ParseCellID("zzzz"); err == nil {
		t.Fatal("expected error for garbage input")
	}
	if _, err := ParseCellID("ffffffffff"); err == nil {
		t.Fatal("expected error for invalid cell fields")
	}
}

// TestHierarchy exercises parent/child scaling across all resolutions.
func TestHierarchy(t *testing.T) {
	leaf := testGrid.FromLatLon(51.5074, -0.1278, 10) // London
	cur := leaf
	for res := 10; res > 0; res-- {
		parent, ok := cur.Parent()
		if !ok {
			t.Fatalf("res %d: missing parent", res)
		}
		if parent.Resolution() != res-1 {
			t.Fatalf("parent resolution = %d, want %d", parent.Resolution(), res-1)
		}
		if !parent.IsAncestorOf(leaf) {
			t.Fatalf("parent %s not ancestor of %s", parent, leaf)
		}
		children, ok := parent.Children()
		if !ok {
			t.Fatalf("res %d: missing children", res-1)
		}
		found := false
		for _, ch := range children {
			if ch == cur {
				found = true
			}
			if p, _ := ch.Parent(); p != parent {
				t.Fatalf("child %s parent != %s", ch, parent)
			}
		}
		if !found {
			t.Fatalf("cell %s not among children of its parent %s", cur, parent)
		}
		cur = parent
	}
	if _, ok := cur.Parent(); ok {
		t.Fatal("res-0 cell must not have a parent")
	}
}

// TestRootCells verifies resolution 0 covers exactly the 10 rhombi.
func TestRootCells(t *testing.T) {
	seen := map[int]bool{}
	for rh := 0; rh < NumRhombi; rh++ {
		c := MakeCell(rh, 0, 0, 0)
		if !c.IsValid() {
			t.Fatalf("root cell %s invalid", c)
		}
		seen[c.Rhombus()] = true
	}
	if len(seen) != NumRhombi {
		t.Fatalf("expected %d root rhombi, got %d", NumRhombi, len(seen))
	}
}

// TestKRingAcceptance is the acceptance boundary: res-8 cell, radius-3
// neighborhood must be 37 unique cells forming a contiguous closed disk
// with no holes.
func TestKRingAcceptance(t *testing.T) {
	cell := testGrid.FromLatLon(39.9042, 116.4074, 8)
	const k = 3
	disk := testGrid.KRing(cell, k)
	if want := 1 + 3*k*(k+1); len(disk) != want {
		t.Fatalf("disk size = %d, want %d", len(disk), want)
	}
	set := map[CellID]bool{}
	for _, c := range disk {
		if set[c] {
			t.Fatalf("duplicate cell %s in disk", c)
		}
		set[c] = true
		if !c.IsValid() {
			t.Fatalf("invalid cell %s in disk", c)
		}
	}
	// Ring sizes must be exactly 6r.
	for r := 1; r <= k; r++ {
		if got := len(testGrid.Ring(cell, r)); got != 6*r {
			t.Fatalf("ring %d size = %d, want %d", r, got, 6*r)
		}
	}
	// Contiguity / no holes: BFS from the center over lattice neighbors
	// restricted to the disk must reach every disk cell.
	reached := map[CellID]bool{cell: true}
	queue := []CellID{cell}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range testGrid.Neighbors(cur) {
			if set[nb] && !reached[nb] {
				reached[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	if len(reached) != len(disk) {
		t.Fatalf("disk not contiguous: reached %d of %d cells", len(reached), len(disk))
	}
	// Closure: every non-center disk cell has at least one neighbor one
	// step closer to the center, i.e. the disk is closed under rings.
	for r := 1; r <= k; r++ {
		inner := map[CellID]bool{}
		for _, c := range testGrid.KRing(cell, r-1) {
			inner[c] = true
		}
		for _, c := range testGrid.Ring(cell, r) {
			ok := false
			for _, nb := range testGrid.Neighbors(c) {
				if inner[nb] {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("ring-%d cell %s detached from inner disk", r, c)
			}
		}
	}
}

// TestKRingRhombusCrossing verifies topology stays closed when the disk
// spills over rhombus boundaries.
func TestKRingRhombusCrossing(t *testing.T) {
	const res = 8
	// Cell hugging the a=0 edge of rhombus 0, mid-edge to avoid vertices.
	edge := MakeCell(0, res, 1, 128)
	disk := testGrid.KRing(edge, 3)
	if len(disk) != 37 {
		t.Fatalf("cross-boundary disk size = %d, want 37", len(disk))
	}
	rhombi := map[int]bool{}
	for _, c := range disk {
		rhombi[c.Rhombus()] = true
	}
	if len(rhombi) < 2 {
		t.Fatal("expected disk to span multiple rhombi")
	}
	// Corner-near cell: the disk wraps an icosahedron vertex where 5
	// rhombus corners meet (topological defect, like H3 pentagons), so
	// the deduplicated disk is smaller but must stay valid and closed.
	corner := MakeCell(0, res, 1, 1)
	cdisk := testGrid.KRing(corner, 3)
	seen := map[CellID]bool{}
	crhombi := map[int]bool{}
	for _, c := range cdisk {
		if seen[c] {
			t.Fatalf("duplicate cell %s in corner disk", c)
		}
		seen[c] = true
		if !c.IsValid() {
			t.Fatalf("invalid cell %s in corner disk", c)
		}
		crhombi[c.Rhombus()] = true
	}
	if len(cdisk) >= 37 || len(cdisk) == 0 {
		t.Fatalf("corner disk size = %d, want 0 < n < 37 (vertex defect)", len(cdisk))
	}
	if len(crhombi) < 2 {
		t.Fatal("corner disk should span multiple rhombi")
	}
	// Neighbor relation must be symmetric across the boundary.
	for _, c := range disk {
		for _, nb := range testGrid.Neighbors(c) {
			back := testGrid.Neighbors(nb)
			found := false
			for _, b := range back {
				if b == c {
					found = true
				}
			}
			if !found {
				t.Fatalf("asymmetric adjacency: %s -> %s", c, nb)
			}
		}
	}
}

// TestCenterRoundTrip checks encode(center(cell)) == cell.
func TestCenterRoundTrip(t *testing.T) {
	for _, ll := range [][2]float64{
		{39.9042, 116.4074}, {-33.8688, 151.2093}, {51.5074, -0.1278},
		{40.7128, -74.0060}, {-1.2921, 36.8219}, {35.6762, 139.6503},
		{0.0, 0.0}, {63.0, -21.9}, {-23.55, -46.63},
	} {
		cell := testGrid.FromLatLon(ll[0], ll[1], 8)
		lat, lon := testGrid.Center(cell)
		again := testGrid.FromLatLon(lat, lon, 8)
		if again != cell {
			t.Fatalf("center round trip failed: %s -> (%.4f,%.4f) -> %s",
				cell, lat, lon, again)
		}
	}
}

// TestVertexDefect documents and pins behavior near the 12 icosahedron
// vertices, where 5 (not 6) rhombus corners meet: the disk is deduplicated.
func TestVertexDefect(t *testing.T) {
	const res = 4
	c := MakeCell(0, res, 0, 0) // rhombus corner == icosahedron vertex region
	disk := testGrid.KRing(c, 2)
	seen := map[CellID]bool{}
	for _, cell := range disk {
		if seen[cell] {
			t.Fatalf("duplicate cell %s near vertex", cell)
		}
		seen[cell] = true
	}
	if len(disk) == 0 {
		t.Fatal("empty disk near vertex")
	}
}

// TestConcurrentEncode hammers the read-only grid from many goroutines;
// run with -race to prove the tables are safe for concurrent use.
func TestConcurrentEncode(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				lat := float64((g*7+i)%170-85) + 0.123
				lon := float64((g*13+i)%360-180) - 0.456
				c := testGrid.FromLatLon(lat, lon, 8)
				if !c.IsValid() {
					t.Errorf("invalid cell from (%f, %f)", lat, lon)
					return
				}
				if n := len(testGrid.KRing(c, 2)); n == 0 {
					t.Errorf("empty disk")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
