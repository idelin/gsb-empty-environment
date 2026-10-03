package hexgrid

import (
	"math/rand"
	"testing"
)

func TestIcosahedronTopology(t *testing.T) {
	if len(icosaFaces) != 20 {
		t.Fatalf("want 20 faces, got %d", len(icosaFaces))
	}
	// Every edge crosses forth and back to the original point.
	for f := 0; f < 20; f++ {
		for m := 0; m < 3; m++ {
			w := [3]int64{100, 200, 300}
			w[m] = -7
			want := w[0] + w[1] + w[2]
			f2, w2 := crossEdge(f, m, w)
			if f2 == f {
				t.Fatalf("face %d edge %d crosses to itself", f, m)
			}
			if sum := w2[0] + w2[1] + w2[2]; sum != want {
				t.Fatalf("crossing changed barycentric sum: %d != %d", sum, want)
			}
			f3, w3 := crossEdge(f2, faceCross[f][m].opp, w2)
			if f3 != f || w3 != w {
				t.Fatalf("cross roundtrip failed: (%d,%v) -> (%d,%v) -> (%d,%v)",
					f, w, f2, w2, f3, w3)
			}
		}
	}
	// Crossing preserves the resolution sublattice (2+w)^r * Z[w].
	for r := 1; r <= 6; r++ {
		for f := 0; f < 20; f++ {
			for m := 0; m < 3; m++ {
				p := powM(r, vec2{3, -2})
				w := baryFromIJ(p, freqTable[r])
				w[m] -= freqTable[r] // push across edge m
				w[(m+1)%3] += freqTable[r]
				_, w2 := crossEdge(f, m, w)
				q := ijFromBary(w2)
				if rem := divMPow(r, q); powM(r, rem) != q {
					t.Fatalf("crossing left sublattice at res %d", r)
				}
			}
		}
	}
}

func TestEncodeCenterRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 20000; i++ {
		ll := LatLng{
			Lat: rng.Float64()*180 - 90,
			Lng: rng.Float64()*360 - 180,
		}
		res := rng.Intn(13)
		cell, err := FromLatLng(ll, res)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		center, err := cell.Center()
		if err != nil {
			t.Fatalf("center: %v", err)
		}
		back, err := FromLatLng(center, res)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if back != cell {
			t.Fatalf("roundtrip mismatch: %s -> %v -> %s", cell, center, back)
		}
	}
}

func TestHexStringRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		ll := LatLng{rng.Float64()*180 - 90, rng.Float64()*360 - 180}
		res := rng.Intn(MaxRes + 1)
		cell, err := FromLatLng(ll, res)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseCell(cell.String())
		if err != nil {
			t.Fatalf("parse %q: %v", cell.String(), err)
		}
		if parsed != cell {
			t.Fatalf("string roundtrip: %s != %s", parsed, cell)
		}
	}
	if _, err := ParseCell("zz"); err == nil {
		t.Fatal("expected error for garbage input")
	}
	if _, err := ParseCell("0"); err == nil {
		t.Fatal("expected error for mode 0")
	}
}

func TestBaseCellsAndPentagons(t *testing.T) {
	base := BaseCells()
	if len(base) != 12 {
		t.Fatalf("want 12 base cells, got %d", len(base))
	}
	seen := map[Cell]bool{}
	for _, c := range base {
		if c.Resolution() != 0 {
			t.Fatalf("base cell res != 0")
		}
		if !c.IsPentagon() {
			t.Fatalf("base cell %s not pentagon", c)
		}
		if seen[c] {
			t.Fatalf("duplicate base cell %s", c)
		}
		seen[c] = true
		// A pentagon has exactly 5 neighbors: disk(1) = 6 cells.
		disk, err := c.GridDisk(1)
		if err != nil {
			t.Fatal(err)
		}
		if len(disk) != 6 {
			t.Fatalf("pentagon disk(1) = %d cells, want 6", len(disk))
		}
	}
}

func TestHierarchyParentChild(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 3000; i++ {
		ll := LatLng{rng.Float64()*180 - 90, rng.Float64()*360 - 180}
		cell, err := FromLatLng(ll, 1+rng.Intn(10))
		if err != nil {
			t.Fatal(err)
		}
		res := cell.Resolution()
		// Keep the parent within 2 levels so Children() stays cheap.
		depth := 1 + rng.Intn(2)
		if depth > res {
			depth = res
		}
		parentRes := res - depth
		parent, err := cell.Parent(parentRes)
		if err != nil {
			t.Fatal(err)
		}
		if parent.Resolution() != parentRes {
			t.Fatalf("parent res %d, want %d", parent.Resolution(), parentRes)
		}
		if !parent.IsAncestorOf(cell) {
			t.Fatalf("%s not ancestor of %s", parent, cell)
		}
		// Associativity: Parent(Parent) == Parent.
		if parentRes >= 2 {
			p2a, _ := parent.Parent(parentRes - 1)
			p2b, _ := cell.Parent(parentRes - 1)
			if p2a != p2b {
				t.Fatalf("parent chain mismatch: %s != %s", p2a, p2b)
			}
		}
		// The cell must appear among its parent's children.
		children, err := parent.Children(res)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, ch := range children {
			if ch == cell {
				found = true
			}
			if ch.Resolution() != res {
				t.Fatalf("child res %d, want %d", ch.Resolution(), res)
			}
		}
		if !found {
			t.Fatalf("cell %s missing from children of %s", cell, parent)
		}
	}
}

func TestChildrenCountAndUniqueness(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	checked := 0
	for checked < 200 {
		ll := LatLng{rng.Float64()*170 - 85, rng.Float64()*360 - 180}
		cell, err := FromLatLng(ll, 6)
		if err != nil {
			t.Fatal(err)
		}
		if cell.IsPentagon() {
			continue
		}
		children, err := cell.Children(8)
		if err != nil {
			t.Fatal(err)
		}
		if len(children) != 49 {
			t.Fatalf("non-pentagon children(2 down) = %d, want 49", len(children))
		}
		seen := map[Cell]bool{}
		for _, ch := range children {
			if seen[ch] {
				t.Fatalf("duplicate child %s", ch)
			}
			seen[ch] = true
			p, err := ch.Parent(6)
			if err != nil || p != cell {
				t.Fatalf("child %s parent %s != %s", ch, p, cell)
			}
		}
		checked++
	}
}

// TestChildrenPartition proves the hierarchy tiles the whole sphere
// without gaps or overlaps: the children of the 12 base cells at
// resolution r are pairwise disjoint, total exactly 10*7^r+2, and every
// randomly encoded point lands in the set.
func TestChildrenPartition(t *testing.T) {
	const res = 2
	all := map[Cell]struct{}{}
	total := 0
	for _, base := range BaseCells() {
		children, err := base.Children(res)
		if err != nil {
			t.Fatal(err)
		}
		total += len(children)
		for _, ch := range children {
			if _, dup := all[ch]; dup {
				t.Fatalf("cell %s claimed by two base cells", ch)
			}
			all[ch] = struct{}{}
		}
	}
	want := 10*int64(1) + 2
	for r := 0; r < res; r++ {
		want *= 7
	}
	want = 10*7*7 + 2
	if int64(total) != want || int64(len(all)) != want {
		t.Fatalf("res-%d cell count = %d (unique %d), want %d", res, total, len(all), want)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100000; i++ {
		ll := LatLng{rng.Float64()*180 - 90, rng.Float64()*360 - 180}
		cell, err := FromLatLng(ll, res)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := all[cell]; !ok {
			t.Fatalf("encoded cell %s not in partition", cell)
		}
	}
}

// TestGridDiskAcceptance is the acceptance boundary: encode a GPS
// coordinate at resolution 8, expand a radius-3 ring neighborhood, and
// verify the coverage is contiguous, closed, and free of holes.
func TestGridDiskAcceptance(t *testing.T) {
	cell, err := FromLatLng(LatLng{39.9042, 116.4074}, 8)
	if err != nil {
		t.Fatal(err)
	}
	cells, dists, err := cell.GridDiskDistances(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 37 {
		t.Fatalf("disk(3) = %d cells, want 37", len(cells))
	}
	distOf := make(map[Cell]int, len(cells))
	for i, c := range cells {
		if _, dup := distOf[c]; dup {
			t.Fatalf("duplicate cell %s in disk", c)
		}
		distOf[c] = dists[i]
	}
	// Ring histogram must be 1, 6, 12, 18.
	hist := map[int]int{}
	for _, d := range dists {
		hist[d]++
	}
	for k, want := range map[int]int{0: 1, 1: 6, 2: 12, 3: 18} {
		if hist[k] != want {
			t.Fatalf("ring %d has %d cells, want %d", k, hist[k], want)
		}
	}
	// No holes: every cell at distance d > 0 is adjacent to a cell at
	// distance d-1, so the region is connected through the center.
	for c, d := range distOf {
		if d == 0 {
			continue
		}
		nbrs, err := c.GridDisk(1)
		if err != nil {
			t.Fatal(err)
		}
		ok := false
		for _, nb := range nbrs {
			if distOf[nb] == d-1 {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("cell %s at dist %d has no neighbor at dist %d (hole)", c, d, d-1)
		}
	}
	// Closed: every boundary (ring-3) cell has at least one neighbor
	// outside the disk, i.e. the region has a well-defined boundary.
	ring, err := cell.GridRing(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ring) != 18 {
		t.Fatalf("ring(3) = %d cells, want 18", len(ring))
	}
	for _, rc := range ring {
		nbrs, err := rc.GridDisk(1)
		if err != nil {
			t.Fatal(err)
		}
		outside := false
		for _, nb := range nbrs {
			if _, in := distOf[nb]; !in {
				outside = true
				break
			}
		}
		if !outside {
			t.Fatalf("boundary cell %s has no outside neighbor (not closed)", rc)
		}
	}
	// Topological distances agree with BFS distances.
	for c, d := range distOf {
		got, err := GridDistance(cell, c)
		if err != nil {
			t.Fatal(err)
		}
		if got != d {
			t.Fatalf("GridDistance(%s) = %d, want %d", c, got, d)
		}
	}
}

func TestGridDiskRandomCenters(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	for i := 0; i < 500; i++ {
		ll := LatLng{rng.Float64()*170 - 85, rng.Float64()*360 - 180}
		cell, err := FromLatLng(ll, 4+rng.Intn(6))
		if err != nil {
			t.Fatal(err)
		}
		k := 1 + rng.Intn(4)
		cells, _, err := cell.GridDiskDistances(k)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[Cell]bool{}
		for _, c := range cells {
			if seen[c] {
				t.Fatalf("duplicate %s in disk of %s", c, cell)
			}
			seen[c] = true
		}
		// Away from pentagons the disk must be complete.
		if cell.IsPentagon() {
			continue
		}
		nearPentagon := false
		for _, c := range cells {
			if c.IsPentagon() {
				nearPentagon = true
			}
		}
		if !nearPentagon && len(cells) != 1+3*k*(k+1) {
			t.Fatalf("disk(%d) of %s = %d cells, want %d",
				k, cell, len(cells), 1+3*k*(k+1))
		}
	}
}

func TestGridDiskCrossesFaces(t *testing.T) {
	// Find a cell near a face edge and verify its disk still has the
	// full cell count (the edge crossing must not create holes).
	found := 0
	rng := rand.New(rand.NewSource(77))
	for i := 0; i < 200000 && found < 20; i++ {
		ll := LatLng{rng.Float64()*180 - 90, rng.Float64()*360 - 180}
		cell, err := FromLatLng(ll, 5)
		if err != nil {
			t.Fatal(err)
		}
		disk, err := cell.GridDisk(2)
		if err != nil {
			t.Fatal(err)
		}
		faces := map[int]bool{}
		for _, c := range disk {
			faces[c.Face()] = true
		}
		if len(faces) < 2 {
			continue
		}
		found++
		if len(disk) != 19 {
			t.Fatalf("cross-face disk(2) = %d cells, want 19 (faces: %v)", len(disk), faces)
		}
	}
	if found == 0 {
		t.Fatal("no cross-face disk found in sampling")
	}
}

func TestConcurrentReadOnlySafety(t *testing.T) {
	// hexgrid is read-only after init; hammer it from many goroutines.
	done := make(chan struct{})
	for g := 0; g < 16; g++ {
		go func(g int) {
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				ll := LatLng{rng.Float64()*180 - 90, rng.Float64()*360 - 180}
				c, err := FromLatLng(ll, 8)
				if err != nil {
					t.Error(err)
					break
				}
				if _, err := c.GridDisk(2); err != nil {
					t.Error(err)
					break
				}
				if _, err := c.Parent(3); err != nil {
					t.Error(err)
					break
				}
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < 16; g++ {
		<-done
	}
}
