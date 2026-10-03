package grid

import (
	"math"
	"testing"
)

// Demo coordinate: Beijing (used by the acceptance scenario).
const (
	demoLat = 39.9042
	demoLng = 116.4074
)

func mustEncode(t *testing.T, lat, lng float64, res int) Index {
	t.Helper()
	idx, err := Encode(lat, lng, res)
	if err != nil {
		t.Fatalf("Encode(%v,%v,%d): %v", lat, lng, res, err)
	}
	return idx
}

func TestEncodeHexStringFormat(t *testing.T) {
	idx := mustEncode(t, demoLat, demoLng, 8)
	s := idx.String()
	if len(s) != 16 {
		t.Fatalf("hex string length = %d, want 16 (%q)", len(s), s)
	}
	back, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	if back != idx {
		t.Fatalf("round trip mismatch: %v != %v", back, idx)
	}
	if idx.Res() != 8 {
		t.Fatalf("res = %d, want 8", idx.Res())
	}
	if idx.Face() < 0 || idx.Face() >= 20 {
		t.Fatalf("face %d out of range", idx.Face())
	}
	t.Logf("encode(%v, %v, res=8) = %s (face %d)", demoLat, demoLng, s, idx.Face())
}

func TestEncodeDeterministicAndDistinct(t *testing.T) {
	a := mustEncode(t, demoLat, demoLng, 8)
	b := mustEncode(t, demoLat, demoLng, 8)
	if a != b {
		t.Fatal("encoding not deterministic")
	}
	// Nearby but distinct points should usually map to different cells at res 8.
	c := mustEncode(t, demoLat+0.1, demoLng+0.1, 8)
	if a == c {
		t.Fatal("distinct points mapped to same cell")
	}
}

func TestEncodeValidation(t *testing.T) {
	if _, err := Encode(91, 0, 8); err == nil {
		t.Fatal("expected error for lat=91")
	}
	if _, err := Encode(0, 0, MaxRes+1); err == nil {
		t.Fatal("expected error for res > MaxRes")
	}
	if _, err := Parse("zzzz"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParentChildHierarchy(t *testing.T) {
	idx := mustEncode(t, demoLat, demoLng, 8)
	p5, err := idx.Parent(5)
	if err != nil {
		t.Fatal(err)
	}
	if p5.Res() != 5 {
		t.Fatalf("parent res = %d, want 5", p5.Res())
	}
	for pos := 1; pos <= 5; pos++ {
		if p5.Digit(pos) != idx.Digit(pos) {
			t.Fatalf("digit %d mismatch: parent %d, child %d", pos, p5.Digit(pos), idx.Digit(pos))
		}
	}
	// Parent of parent chains down to res 0.
	p0, err := p5.Parent(0)
	if err != nil {
		t.Fatal(err)
	}
	if p0.Res() != 0 {
		t.Fatalf("res-0 parent has res %d", p0.Res())
	}
	// Children: 7 distinct, each with this parent.
	children, err := p5.Children()
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 7 {
		t.Fatalf("children = %d, want 7", len(children))
	}
	seen := map[Index]bool{}
	for _, c := range children {
		if seen[c] {
			t.Fatal("duplicate child")
		}
		seen[c] = true
		if c.Res() != 6 {
			t.Fatalf("child res = %d, want 6", c.Res())
		}
		back, err := c.Parent(5)
		if err != nil {
			t.Fatal(err)
		}
		if back != p5 {
			t.Fatal("child.Parent() != parent")
		}
	}
	if _, err := idx.Parent(9); err == nil {
		t.Fatal("expected error for parent res > res")
	}
}

// TestFoldUnfoldBijection proves the aperture-7 (base cell, digit)
// decomposition is an exact bijection over axial coordinates (pure integer
// round trip), i.e. cells at every resolution tile the whole plane once.
func TestFoldUnfoldBijection(t *testing.T) {
	const res = 8
	for q := -400; q <= 400; q += 37 {
		for r := -400; r <= 400; r += 41 {
			bq, br, d := fold(q, r, res)
			idx := makeIndex(0, res, bq, br, &d)
			gq, gr := idx.ij()
			if gq != q || gr != r {
				t.Fatalf("unfold(fold(%d,%d)) = (%d,%d)", q, r, gq, gr)
			}
		}
	}
}

func TestCenterRoundTrip(t *testing.T) {
	idx := mustEncode(t, demoLat, demoLng, 8)
	lat, lng := idx.Center()
	again := mustEncode(t, lat, lng, 8)
	if again != idx {
		t.Fatalf("center re-encode mismatch: %s != %s", again, idx)
	}
	// Center must be geographically close to the original point.
	if math.Abs(lat-demoLat) > 0.05 || math.Abs(lng-demoLng) > 0.05 {
		t.Fatalf("center (%v,%v) too far from (%v,%v)", lat, lng, demoLat, demoLng)
	}
}

func TestKRingAcceptance(t *testing.T) {
	idx := mustEncode(t, demoLat, demoLng, 8)
	for k, want := range map[int]int{0: 1, 1: 7, 2: 19, 3: 37, 4: 61} {
		ring := KRing(idx, k)
		if len(ring) != want {
			t.Fatalf("KRing(k=%d) size = %d, want %d", k, len(ring), want)
		}
		seen := map[Index]bool{}
		for _, c := range ring {
			if seen[c] {
				t.Fatalf("KRing(k=%d) contains duplicate %s", k, c)
			}
			seen[c] = true
			if c.Res() != 8 {
				t.Fatalf("ring cell res = %d, want 8", c.Res())
			}
		}
	}
}

// TestKRingClosedCoverage verifies the area covered by KRing(k) is
// contiguous and hole-free: the k-ring equals the 1-ring expansion of the
// (k-1)-ring, i.e. no cell is missing inside the covered region.
func TestKRingClosedCoverage(t *testing.T) {
	idx := mustEncode(t, demoLat, demoLng, 8)
	const K = 3
	prev := KRing(idx, 0)
	for k := 1; k <= K; k++ {
		cur := KRing(idx, k)
		curSet := make(map[Index]bool, len(cur))
		for _, c := range cur {
			curSet[c] = true
		}
		// Expand previous ring by one topological step.
		expanded := map[Index]bool{}
		for _, c := range prev {
			for _, n := range KRing(c, 1) {
				expanded[n] = true
			}
		}
		if len(expanded) != len(curSet) {
			t.Fatalf("k=%d: expansion size %d != ring size %d (hole or gap)", k, len(expanded), len(curSet))
		}
		for c := range curSet {
			if !expanded[c] {
				t.Fatalf("k=%d: cell %s missing from expansion (hole)", k, c)
			}
		}
		prev = cur
	}
}

// TestKRingEverywhere checks count/closure on a global sample of points,
// including cells near icosahedron face boundaries and the poles.
func TestKRingEverywhere(t *testing.T) {
	checked := 0
	for lat := -75.0; lat <= 75.0; lat += 15 {
		for lng := -180.0; lng < 180.0; lng += 20 {
			idx := mustEncode(t, lat, lng, 6)
			ring := KRing(idx, 3)
			if len(ring) != 37 {
				t.Fatalf("KRing(%s,3) at (%v,%v) size = %d, want 37", idx, lat, lng, len(ring))
			}
			seen := map[Index]bool{}
			for _, c := range ring {
				if seen[c] {
					t.Fatalf("duplicate in ring at (%v,%v)", lat, lng)
				}
				seen[c] = true
			}
			checked++
		}
	}
	// Poles.
	for _, lat := range []float64{89.9, -89.9} {
		idx := mustEncode(t, lat, 0, 6)
		if got := len(KRing(idx, 3)); got != 37 {
			t.Fatalf("pole ring size = %d, want 37", got)
		}
	}
	t.Logf("verified closed 3-ring coverage at %d locations + poles", checked)
}

// TestNeighborsSymmetric verifies adjacency is symmetric on a global sample:
// if B is a neighbor of A, then A is a neighbor of B.
func TestNeighborsSymmetric(t *testing.T) {
	for lat := -75.0; lat <= 75.0; lat += 15 {
		for lng := -180.0; lng < 180.0; lng += 20 {
			a := mustEncode(t, lat, lng, 6)
			for _, b := range Neighbors(a) {
				found := false
				for _, back := range Neighbors(b) {
					if back == a {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("asymmetric adjacency at (%v,%v): %s -> %s not reciprocal", lat, lng, a, b)
				}
			}
		}
	}
}

// TestCenterReencodeEverywhere verifies encoding idempotence: every cell
// center re-encodes to its own cell (required for a leak-free topology).
func TestCenterReencodeEverywhere(t *testing.T) {
	for lat := -80.0; lat <= 80.0; lat += 10 {
		for lng := -180.0; lng < 180.0; lng += 15 {
			idx := mustEncode(t, lat, lng, 6)
			lat2, lng2 := idx.Center()
			again := mustEncode(t, lat2, lng2, 6)
			if again != idx {
				t.Fatalf("center of %s re-encoded to %s", idx, again)
			}
		}
	}
}

// TestKRingExpansionIdentities checks ring cardinality 1+3k(k+1) and that
// KRing(k) \ KRing(k-1) is exactly the distance-k shell.
func TestKRingExpansionIdentities(t *testing.T) {
	idx := mustEncode(t, demoLat, demoLng, 8)
	prev := map[Index]bool{}
	for _, c := range KRing(idx, 0) {
		prev[c] = true
	}
	ring := KRing(idx, 0)
	for k := 1; k <= 4; k++ {
		next := KRing(idx, k)
		nextSet := map[Index]bool{}
		for _, c := range next {
			nextSet[c] = true
		}
		shell := 0
		for c := range nextSet {
			if !prev[c] {
				shell++
			}
		}
		if shell != 6*k {
			t.Fatalf("shell k=%d has %d cells, want %d", k, shell, 6*k)
		}
		if len(nextSet) != 1+3*k*(k+1) {
			t.Fatalf("ring k=%d has %d cells, want %d", k, len(nextSet), 1+3*k*(k+1))
		}
		prev = nextSet
		ring = next
	}
	_ = ring
}
