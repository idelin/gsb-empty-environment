package hexgrid

import (
	"math"
	"testing"
)

// faceCenterCell returns a cell deep inside a face (fast-path territory).
func faceCenterCell(t *testing.T, face, res int) Cell {
	t.Helper()
	lat, lng := FaceCenterLatLng(face)
	return CellFromLatLng(lat, lng, res)
}

func TestGridDiskSizes(t *testing.T) {
	c := faceCenterCell(t, 3, 8)
	want := []int{1, 7, 19, 37, 61}
	for k, w := range want {
		disk := GridDisk(c, k)
		if len(disk) != w {
			t.Fatalf("k=%d: got %d cells, want %d", k, len(disk), w)
		}
		assertUnique(t, disk)
	}
}

func TestGridRingSizes(t *testing.T) {
	c := faceCenterCell(t, 3, 8)
	for k := 0; k <= 4; k++ {
		ring := GridRing(c, k)
		want := 6 * k
		if k == 0 {
			want = 1
		}
		if len(ring) != want {
			t.Fatalf("ring %d: got %d cells, want %d", k, len(ring), want)
		}
		assertUnique(t, ring)
	}
}

func assertUnique(t *testing.T, cells []Cell) {
	t.Helper()
	seen := make(map[Cell]struct{}, len(cells))
	for _, c := range cells {
		if _, ok := seen[c]; ok {
			t.Fatalf("duplicate cell %s", c)
		}
		seen[c] = struct{}{}
	}
}

// TestGridDiskNoHoles verifies the k=3 disk is topologically closed: every
// cell within distance 2 of the center has all six neighbors inside the
// disk, so the covered area has no gaps.
func TestGridDiskNoHoles(t *testing.T) {
	c := faceCenterCell(t, 7, 8)
	disk := GridDisk(c, 3)
	inDisk := make(map[Cell]struct{}, len(disk))
	for _, x := range disk {
		inDisk[x] = struct{}{}
	}
	inner2 := GridDisk(c, 2)
	var buf [8]Cell
	for _, x := range inner2 {
		n := GridDiskInto(x, 1, buf[:])
		if n != 7 {
			t.Fatalf("interior cell %s has %d ring-1 cells, want 7", x, n)
		}
		for i := 0; i < n; i++ {
			if _, ok := inDisk[buf[i]]; !ok {
				t.Fatalf("hole: neighbor %s of interior cell %s missing from disk", buf[i], x)
			}
		}
	}
}

// TestGridDiskContinuousCoverage verifies that adjacent cells share exact
// boundary vertices, i.e. the union of cell polygons is continuous.
func TestGridDiskContinuousCoverage(t *testing.T) {
	c := faceCenterCell(t, 7, 8)
	disk := GridDisk(c, 3)
	inDisk := make(map[Cell]struct{}, len(disk))
	for _, x := range disk {
		inDisk[x] = struct{}{}
	}
	var buf [8]Cell
	sharedVertices := 0
	for _, x := range disk {
		n := GridDiskInto(x, 1, buf[:])
		bx := x.Boundary()
		for i := 1; i < n; i++ { // skip self at index 0
			y := buf[i]
			if _, ok := inDisk[y]; !ok {
				continue
			}
			by := y.Boundary()
			shared := countSharedVertices(bx, by)
			if shared != 2 {
				t.Fatalf("adjacent cells %s and %s share %d vertices, want 2", x, y, shared)
			}
			sharedVertices += shared
		}
	}
	if sharedVertices == 0 {
		t.Fatal("no adjacency checked")
	}
}

func countSharedVertices(a, b [6]LatLng) int {
	n := 0
	for _, va := range a {
		for _, vb := range b {
			if math.Abs(va.Lat-vb.Lat) < 1e-9 && math.Abs(va.Lng-vb.Lng) < 1e-9 {
				n++
				break
			}
		}
	}
	return n
}

func TestGridDiskIntoMatchesGridDisk(t *testing.T) {
	c := faceCenterCell(t, 11, 6)
	var buf [64]Cell
	for k := 0; k <= 3; k++ {
		n := GridDiskInto(c, k, buf[:])
		disk := GridDisk(c, k)
		if n != len(disk) {
			t.Fatalf("k=%d: Into=%d GridDisk=%d", k, n, len(disk))
		}
		for i := 0; i < n; i++ {
			if buf[i] != disk[i] {
				t.Fatalf("k=%d idx=%d: %s != %s", k, i, buf[i], disk[i])
			}
		}
	}
}

// TestGridDiskFaceCrossing picks a cell next to a face edge and checks the
// disk crosses onto the neighboring face without duplicates or losses.
func TestGridDiskFaceCrossing(t *testing.T) {
	edge := findEdgeCell(t, 8)
	disk := GridDisk(edge, 3)
	assertUnique(t, disk)
	if len(disk) < 30 || len(disk) > 37 {
		t.Fatalf("edge disk has %d cells, want in [30,37]", len(disk))
	}
	facesSeen := make(map[int]bool)
	for _, x := range disk {
		facesSeen[x.Face()] = true
	}
	if len(facesSeen) < 2 {
		t.Fatalf("expected disk to span multiple faces, got %v", facesSeen)
	}
	// Every emitted cell must re-encode to itself (stable quantization).
	for _, x := range disk {
		lat, lng := x.LatLng()
		if got := CellFromLatLng(lat, lng, 8); got != x {
			t.Fatalf("unstable cell %s -> %s", x, got)
		}
	}
}

// findEdgeCell walks along a meridian until the face flips and returns a
// cell adjacent to the boundary.
func findEdgeCell(t *testing.T, res int) Cell {
	t.Helper()
	lng := 10.0
	lo, hi := -60.0, 80.0
	prev := CellFromLatLng(lo, lng, res)
	// Coarse scan for a face flip.
	flipLo, flipHi := lo, hi
	found := false
	for lat := lo + 0.05; lat <= hi; lat += 0.05 {
		c := CellFromLatLng(lat, lng, res)
		if c.Face() != prev.Face() {
			flipLo, flipHi = lat-0.05, lat
			found = true
			break
		}
		prev = c
	}
	if !found {
		t.Fatal("no face crossing found")
	}
	// Bisect the flip interval down to below one cell width.
	for i := 0; i < 40; i++ {
		mid := (flipLo + flipHi) / 2
		if CellFromLatLng(mid, lng, res).Face() == CellFromLatLng(flipLo, lng, res).Face() {
			flipLo = mid
		} else {
			flipHi = mid
		}
	}
	return CellFromLatLng(flipLo, lng, res)
}

func TestGridDiskIntoZeroAlloc(t *testing.T) {
	c := faceCenterCell(t, 5, 8)
	var buf [64]Cell
	GridDiskInto(c, 3, buf[:]) // warm up
	allocs := testing.AllocsPerRun(200, func() {
		GridDiskInto(c, 3, buf[:])
	})
	if allocs != 0 {
		t.Fatalf("GridDiskInto allocated %v times per run, want 0", allocs)
	}
}

func TestGridRingIntoZeroAlloc(t *testing.T) {
	c := faceCenterCell(t, 5, 8)
	var buf [32]Cell
	GridRingInto(c, 3, buf[:])
	allocs := testing.AllocsPerRun(200, func() {
		GridRingInto(c, 3, buf[:])
	})
	if allocs != 0 {
		t.Fatalf("GridRingInto allocated %v times per run, want 0", allocs)
	}
}
