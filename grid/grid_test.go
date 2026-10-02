package grid

import (
	"math"
	"math/rand"
	"testing"
)

var beijing = LatLng{Lat: 39.9042, Lng: 116.4074}

func angDist(a, b LatLng) float64 {
	la1 := a.Lat * math.Pi / 180
	lo1 := a.Lng * math.Pi / 180
	la2 := b.Lat * math.Pi / 180
	lo2 := b.Lng * math.Pi / 180
	x := math.Sin(la1)*math.Sin(la2) + math.Cos(la1)*math.Cos(la2)*math.Cos(lo1-lo2)
	if x > 1 {
		x = 1
	}
	if x < -1 {
		x = -1
	}
	return math.Acos(x)
}

func mustCell(t *testing.T, ll LatLng, res int) Cell {
	t.Helper()
	c, err := LatLngToCell(ll, res)
	if err != nil {
		t.Fatalf("LatLngToCell(%v, %d): %v", ll, res, err)
	}
	return c
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		ll := LatLng{
			Lat: rng.Float64()*170 - 85, // avoid exact poles here
			Lng: rng.Float64()*360 - 180,
		}
		res := rng.Intn(MaxRes + 1)
		c := mustCell(t, ll, res)
		if !c.IsValid() {
			t.Fatalf("invalid cell %s for %v res %d", c, ll, res)
		}
		if got := angDist(ll, CellToLatLng(c)); got > CellVertexRadius(res) {
			t.Fatalf("center error %g exceeds bound %g (res %d, %v)",
				got, CellVertexRadius(res), res, ll)
		}
	}
}

func TestPolesAndDateline(t *testing.T) {
	for _, ll := range []LatLng{
		{Lat: 90, Lng: 0}, {Lat: -90, Lng: 120},
		{Lat: 0, Lng: 180}, {Lat: 0, Lng: -180},
		{Lat: 12.3, Lng: 179.999}, {Lat: -45.6, Lng: -179.999},
	} {
		c := mustCell(t, ll, 8)
		if !c.IsValid() {
			t.Fatalf("invalid cell at %v", ll)
		}
		if got := angDist(ll, CellToLatLng(c)); got > CellVertexRadius(8) {
			t.Fatalf("center error %g too large at %v", got, ll)
		}
	}
	// +180 and -180 longitude are the same meridian
	a := mustCell(t, LatLng{Lat: 10, Lng: 180}, 8)
	b := mustCell(t, LatLng{Lat: 10, Lng: -180}, 8)
	if a != b {
		t.Fatalf("dateline mismatch: %s != %s", a, b)
	}
}

func TestParentConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 3000; i++ {
		ll := LatLng{
			Lat: rng.Float64()*170 - 85,
			Lng: rng.Float64()*360 - 180,
		}
		res := 1 + rng.Intn(MaxRes)
		c := mustCell(t, ll, res)
		parentRes := rng.Intn(res + 1)
		p, err := c.Parent(parentRes)
		if err != nil {
			t.Fatalf("Parent: %v", err)
		}
		direct := mustCell(t, ll, parentRes)
		if p != direct {
			t.Fatalf("parent mismatch at res %d->%d for %v: %s != %s",
				res, parentRes, ll, p, direct)
		}
		if parentRes < res && !p.IsParentOf(c) {
			t.Fatalf("IsParentOf false for true ancestor")
		}
	}
}

func TestParentErrors(t *testing.T) {
	c := mustCell(t, beijing, 5)
	if _, err := c.Parent(-1); err == nil {
		t.Fatal("expected error for negative resolution")
	}
	if _, err := c.Parent(MaxRes + 1); err == nil {
		t.Fatal("expected error for resolution > MaxRes")
	}
	if _, err := c.Parent(6); err == nil {
		t.Fatal("expected error for parent finer than cell")
	}
	p, err := c.Parent(5)
	if err != nil || p != c {
		t.Fatalf("parent at same resolution should be identity, got %s %v", p, err)
	}
}

func TestChildren(t *testing.T) {
	c := mustCell(t, beijing, 3)
	// one level down: exactly 7 children, all distinct, correct parent
	ch, err := c.Children(4)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(ch) != 7 {
		t.Fatalf("expected 7 children, got %d", len(ch))
	}
	seen := map[Cell]bool{}
	for _, child := range ch {
		if seen[child] {
			t.Fatalf("duplicate child %s", child)
		}
		seen[child] = true
		if !c.IsParentOf(child) {
			t.Fatalf("child %s does not belong to parent %s", child, c)
		}
		p, _ := child.Parent(3)
		if p != c {
			t.Fatalf("child.Parent != parent: %s != %s", p, c)
		}
		if !child.IsValid() {
			t.Fatalf("invalid child %s", child)
		}
	}
	// two levels down: 49 children
	ch2, err := c.Children(5)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(ch2) != 49 {
		t.Fatalf("expected 49 grandchildren, got %d", len(ch2))
	}
	seen2 := map[Cell]bool{}
	for _, child := range ch2 {
		if seen2[child] {
			t.Fatalf("duplicate grandchild %s", child)
		}
		seen2[child] = true
	}
	// children centers must lie within the parent footprint
	pc := CellToLatLng(c)
	for _, child := range ch {
		if d := angDist(pc, CellToLatLng(child)); d > CellVertexRadius(3) {
			t.Fatalf("child center %g rad away from parent center (bound %g)",
				d, CellVertexRadius(3))
		}
	}
	// error cases
	if _, err := c.Children(2); err == nil {
		t.Fatal("expected error for children coarser than cell")
	}
	if _, err := c.Children(MaxRes + 1); err == nil {
		t.Fatal("expected error for children beyond MaxRes")
	}
	same, err := c.Children(3)
	if err != nil || len(same) != 1 || same[0] != c {
		t.Fatalf("children at same resolution should be [self], got %v %v", same, err)
	}
}

func TestStringRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 500; i++ {
		ll := LatLng{Lat: rng.Float64()*170 - 85, Lng: rng.Float64()*360 - 180}
		c := mustCell(t, ll, rng.Intn(MaxRes+1))
		parsed, err := ParseCell(c.String())
		if err != nil {
			t.Fatalf("ParseCell(%q): %v", c.String(), err)
		}
		if parsed != c {
			t.Fatalf("round trip mismatch: %s -> %q -> %s", c, c.String(), parsed)
		}
	}
	for _, bad := range []string{"", "zz", "0x", "ffffffffffffffff", "1", "880810bb213fff8"} {
		if _, err := ParseCell(bad); err == nil {
			t.Fatalf("expected parse error for %q", bad)
		}
	}
}

func TestInvalidResolution(t *testing.T) {
	if _, err := LatLngToCell(beijing, -1); err != ErrInvalidResolution {
		t.Fatalf("expected ErrInvalidResolution, got %v", err)
	}
	if _, err := LatLngToCell(beijing, MaxRes+1); err != ErrInvalidResolution {
		t.Fatalf("expected ErrInvalidResolution, got %v", err)
	}
}

func TestKRingSizesAndUniqueness(t *testing.T) {
	c := mustCell(t, beijing, 8)
	for k := 0; k <= 4; k++ {
		ring, err := KRing(c, k)
		if err != nil {
			t.Fatalf("KRing(%d): %v", k, err)
		}
		if want := KRingSize(k); len(ring) != want {
			t.Fatalf("KRing(%d) size = %d, want %d", k, len(ring), want)
		}
		seen := map[Cell]bool{}
		for _, cell := range ring {
			if seen[cell] {
				t.Fatalf("duplicate cell %s in ring %d", cell, k)
			}
			seen[cell] = true
			if cell.Res() != c.Res() || cell.Face() != c.Face() {
				t.Fatalf("ring cell %s has wrong res/face", cell)
			}
			if !cell.IsValid() {
				t.Fatalf("invalid ring cell %s", cell)
			}
		}
		if !seen[c] {
			t.Fatalf("ring %d does not contain its center", k)
		}
	}
	if _, err := KRing(c, -1); err == nil {
		t.Fatal("expected error for negative k")
	}
}

// TestKRingClosedCoverage is the acceptance test: the radius-3 ring around
// a res-8 cell must form a continuous, closed patch with no holes.
func TestKRingClosedCoverage(t *testing.T) {
	center := mustCell(t, beijing, 8)
	const k = 3
	ring, err := KRing(center, k)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	if err := ValidateFaceLocal(ring); err != nil {
		t.Fatalf("ring not face-local: %v", err)
	}
	set := make(map[Cell]IJ, len(ring))
	for _, c := range ring {
		set[c] = c.IJ()
	}
	cij := center.IJ()
	interior, boundary := 0, 0
	for cell, ij := range set {
		d := ij.Dist(cij)
		if d > k {
			t.Fatalf("cell %s at distance %d > k", cell, d)
		}
		if d == k {
			boundary++
			continue
		}
		interior++
		// every interior cell must have all six neighbors inside the set:
		// this is exactly the "no holes, closed coverage" property
		nbrs, err := Neighbors(cell)
		if err != nil {
			t.Fatalf("Neighbors(%s): %v", cell, err)
		}
		for _, n := range nbrs {
			if _, ok := set[n]; !ok {
				t.Fatalf("hole: neighbor %s of interior cell %s missing", n, cell)
			}
		}
	}
	if boundary != 6*k {
		t.Fatalf("boundary cell count = %d, want %d", boundary, 6*k)
	}
	if interior != KRingSize(k-1) {
		t.Fatalf("interior cell count = %d, want %d", interior, KRingSize(k-1))
	}
}

// TestKRingGeometricContinuity checks the ring is also geometrically
// contiguous: adjacent cell centers are uniformly spaced and the whole
// patch stays within the expected radius.
func TestKRingGeometricContinuity(t *testing.T) {
	center := mustCell(t, beijing, 8)
	const k = 3
	ring, err := KRing(center, k)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	cc := CellToLatLng(center)
	// expected center-to-center spacing: sqrt(3) * hexSize / scale
	spacing := 1.0 / scales[8] // lattice unit in tangent-plane radians
	maxR := 0.0
	for _, c := range ring {
		if d := angDist(cc, CellToLatLng(c)); d > maxR {
			maxR = d
		}
	}
	if maxR > float64(k)*spacing*1.3 {
		t.Fatalf("ring radius %g exceeds expected %g", maxR, float64(k)*spacing*1.3)
	}
	// adjacent centers must be ~uniformly spaced (no gaps, no overlaps)
	nbrs, err := Neighbors(center)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	for _, n := range nbrs {
		d := angDist(cc, CellToLatLng(n))
		if d < 0.7*spacing || d > 1.3*spacing {
			t.Fatalf("neighbor spacing %g outside [0.7,1.3]*%g", d, spacing)
		}
	}
}

func TestNeighborsMatchKRing1(t *testing.T) {
	c := mustCell(t, beijing, 10)
	nbrs, err := Neighbors(c)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	ring, err := KRing(c, 1)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	set := map[Cell]bool{}
	for _, cell := range ring {
		set[cell] = true
	}
	for _, n := range nbrs {
		if !set[n] {
			t.Fatalf("neighbor %s missing from k=1 ring", n)
		}
	}
	if len(ring) != 7 {
		t.Fatalf("k=1 ring size = %d, want 7", len(ring))
	}
}

// TestKRingIntegerRoundTrip verifies the pure-integer invariant: decoding
// every ring cell to its lattice coordinate yields exactly the expected
// cube-coordinate offset set, with distances matching ring membership.
func TestKRingIntegerRoundTrip(t *testing.T) {
	center := mustCell(t, beijing, 6)
	const k = 3
	ring, err := KRing(center, k)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	cij := center.IJ()
	distCount := map[int]int{}
	for _, c := range ring {
		d := c.IJ().Dist(cij)
		distCount[d]++
	}
	for d := 0; d <= k; d++ {
		want := 1
		if d > 0 {
			want = 6 * d
		}
		if distCount[d] != want {
			t.Fatalf("distance %d: got %d cells, want %d", d, distCount[d], want)
		}
	}
}

// TestBeijingRes8 is a regression snapshot of the acceptance encoding.
func TestBeijingRes8(t *testing.T) {
	c := mustCell(t, beijing, 8)
	const want = "880810bb203ffff"
	if c.String() != want {
		t.Fatalf("Beijing res-8 cell = %s, want %s", c, want)
	}
	if c.Res() != 8 || c.Face() != 4 {
		t.Fatalf("unexpected res/face: %d/%d", c.Res(), c.Face())
	}
}
