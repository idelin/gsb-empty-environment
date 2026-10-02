package hexgrid

import (
	"sync"
	"testing"
)

// beijingCell is deep inside one icosahedron face, so its neighbourhoods
// never cross face edges and exact hex-grid counts must hold.
func beijingCell() Cell { return LatLngToCell(39.9042, 116.4074, 8) }

// TestGridDiskCounts: a disk of radius k holds exactly 1+3k(k+1) cells.
func TestGridDiskCounts(t *testing.T) {
	c := beijingCell()
	want := map[int]int{0: 1, 1: 7, 2: 19, 3: 37, 4: 61}
	for k, n := range want {
		disk := GridDisk(c, k, nil)
		if len(disk) != n {
			t.Fatalf("k=%d: got %d cells, want %d", k, len(disk), n)
		}
		assertUnique(t, disk)
		for _, x := range disk {
			if !x.IsValid() {
				t.Fatalf("k=%d: invalid cell %s", k, x)
			}
			if x.Resolution() != c.Resolution() {
				t.Fatalf("k=%d: resolution changed", k)
			}
		}
	}
}

// TestGridRingCounts: a hollow ring at distance k holds exactly 6k cells.
func TestGridRingCounts(t *testing.T) {
	c := beijingCell()
	for k := 1; k <= 5; k++ {
		ring := GridRing(c, k, nil)
		if len(ring) != 6*k {
			t.Fatalf("k=%d: got %d ring cells, want %d", k, len(ring), 6*k)
		}
		assertUnique(t, ring)
	}
	if ring := GridRing(c, 0, nil); len(ring) != 1 || ring[0] != c {
		t.Fatal("k=0 ring must be the center cell itself")
	}
}

// TestHexDistance: every cell in ring k is at exact topological distance k.
func TestHexDistance(t *testing.T) {
	c := beijingCell()
	for k := 0; k <= 4; k++ {
		for _, x := range GridRing(c, k, nil) {
			d, ok := HexDistance(c, x)
			if !ok {
				t.Fatalf("k=%d: distance not computable (face mismatch)", k)
			}
			if d != k {
				t.Fatalf("ring k=%d contains cell at distance %d", k, d)
			}
		}
	}
	if d, _ := HexDistance(c, c); d != 0 {
		t.Fatal("self distance must be 0")
	}
	// Cross-face distance reports ok=false.
	other := LatLngToCell(-33.8688, 151.2093, 8)
	if c.Face() != other.Face() {
		if _, ok := HexDistance(c, other); ok {
			t.Fatal("cross-face distance must report ok=false")
		}
	}
}

// TestDiskNoHoles: acceptance criterion — the k=3 disk is topologically
// continuous and closed. Every cell at distance d >= 1 is adjacent to some
// cell at distance d-1 (no holes), and every disk cell is reachable.
func TestDiskNoHoles(t *testing.T) {
	c := beijingCell()
	const k = 3
	disk := GridDisk(c, k, nil)
	if len(disk) != 37 {
		t.Fatalf("want 37 cells, got %d", len(disk))
	}
	inDisk := make(map[Cell]bool, len(disk))
	for _, x := range disk {
		inDisk[x] = true
	}
	for d := 1; d <= k; d++ {
		inner := make(map[Cell]bool)
		for _, x := range GridRing(c, d-1, nil) {
			inner[x] = true
		}
		for _, x := range GridRing(c, d, nil) {
			// Adjacency to the previous ring => no holes in coverage.
			neighbors := Neighbors(x, nil)
			adjacent := false
			for _, nb := range neighbors {
				if inner[nb] {
					adjacent = true
					break
				}
			}
			if !adjacent {
				t.Fatalf("ring %d cell %s is detached from ring %d: hole in coverage", d, x, d-1)
			}
			// Every ring cell must be inside the disk.
			if !inDisk[x] {
				t.Fatalf("ring %d cell %s missing from disk", d, x)
			}
		}
	}
}

// TestRingClosed: the outer ring forms a closed loop — every ring cell has
// exactly two neighbours on the same ring.
func TestRingClosed(t *testing.T) {
	c := beijingCell()
	for k := 1; k <= 4; k++ {
		ring := GridRing(c, k, nil)
		onRing := make(map[Cell]bool, len(ring))
		for _, x := range ring {
			onRing[x] = true
		}
		for _, x := range ring {
			count := 0
			for _, nb := range Neighbors(x, nil) {
				if onRing[nb] {
					count++
				}
			}
			if count != 2 {
				t.Fatalf("k=%d: ring cell %s has %d on-ring neighbours, want 2 (not closed)", k, x, count)
			}
		}
	}
}

// TestDiskContainsRings: disk(k) == union of rings 0..k.
func TestDiskContainsRings(t *testing.T) {
	c := beijingCell()
	disk := GridDisk(c, 3, nil)
	inDisk := make(map[Cell]bool, len(disk))
	for _, x := range disk {
		inDisk[x] = true
	}
	total := 0
	for k := 0; k <= 3; k++ {
		for _, x := range GridRing(c, k, nil) {
			total++
			if !inDisk[x] {
				t.Fatalf("ring %d cell %s not in disk", k, x)
			}
		}
	}
	if total != len(disk) {
		t.Fatalf("rings sum to %d cells, disk has %d", total, len(disk))
	}
}

// TestGridDiskBoundary: neighbourhoods crossing icosahedron face edges must
// stay valid, unique and (for this configuration) complete.
func TestGridDiskBoundary(t *testing.T) {
	// (26.565, 0) is an icosahedron vertex. At res 0 the radius-2 disk
	// reaches past the 7 root hexagons, forcing the cross-face fallback.
	v := LatLngToCell(26.565, 0, 0)
	disk := GridDisk(v, 2, nil)
	if len(disk) < 7 {
		t.Fatalf("boundary disk: got %d cells, want at least 7", len(disk))
	}
	assertUnique(t, disk)
	faces := map[int]bool{}
	for _, x := range disk {
		if !x.IsValid() {
			t.Fatalf("invalid boundary cell %s", x)
		}
		faces[x.Face()] = true
	}
	if len(faces) < 2 {
		t.Fatal("expected the vertex neighbourhood to span multiple faces")
	}
}

// TestGridDiskNegativeK is a boundary condition: negative radius is a no-op.
func TestGridDiskNegativeK(t *testing.T) {
	c := beijingCell()
	if got := GridDisk(c, -1, nil); len(got) != 0 {
		t.Fatal("negative k must return no cells")
	}
	if got := GridRing(c, -2, nil); len(got) != 0 {
		t.Fatal("negative k must return no cells")
	}
}

// TestGridDiskConcurrent: hammer the trig-free traversal from many
// goroutines; results must be identical (run with -race).
func TestGridDiskConcurrent(t *testing.T) {
	c := beijingCell()
	want := GridDisk(c, 3, nil)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool := sharedTestPool
			for i := 0; i < 2000; i++ {
				buf := pool.Get()
				disk, ok := GridDiskBuffered(c, 3, buf)
				if !ok || len(disk) != len(want) {
					t.Error("buffered disk mismatch")
					pool.Put(buf)
					return
				}
				for j := range disk {
					if disk[j] != want[j] {
						t.Error("concurrent disk result differs")
						pool.Put(buf)
						return
					}
				}
				pool.Put(buf)
			}
		}()
	}
	wg.Wait()
}

func assertUnique(t *testing.T, cells []Cell) {
	t.Helper()
	seen := make(map[Cell]bool, len(cells))
	for _, x := range cells {
		if seen[x] {
			t.Fatalf("duplicate cell %s", x)
		}
		seen[x] = true
	}
}
