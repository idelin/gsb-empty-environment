package hexgrid

import (
	"math"
	"strings"
	"testing"
)

// Beijing CBD, comfortably inside one icosahedron face.
const (
	testLat = 39.9042
	testLng = 116.4074
	testRes = 8
)

func TestEncodeDeterministicHexString(t *testing.T) {
	a := FromLatLng(testLat, testLng, testRes)
	b := FromLatLng(testLat, testLng, testRes)
	if a != b {
		t.Fatalf("encoding not deterministic: %s != %s", a, b)
	}
	s := a.String()
	if len(s) != 16 {
		t.Fatalf("hex string length = %d, want 16 (%q)", len(s), s)
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("non-hex character %q in %q", r, s)
		}
	}
	parsed, err := ParseCell(s)
	if err != nil {
		t.Fatalf("ParseCell(%q): %v", s, err)
	}
	if parsed != a {
		t.Fatalf("round trip mismatch: %s -> %s", a, parsed)
	}
	if !a.IsValid() {
		t.Fatalf("cell %s reports invalid", a)
	}
	if a.Resolution() != testRes {
		t.Fatalf("resolution = %d, want %d", a.Resolution(), testRes)
	}
}

func TestParseCellRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "zzzz", "f000000000000000", "10ffffffffffffff"} {
		if _, err := ParseCell(s); err == nil {
			t.Fatalf("ParseCell(%q) unexpectedly succeeded", s)
		}
	}
}

func TestIcosahedronFacesEquilateral(t *testing.T) {
	if len(IcoFaces) != 20 {
		t.Fatalf("face count = %d, want 20", len(IcoFaces))
	}
	for f, tri := range IcoFaces {
		a, b, c := IcoVertices[tri[0]], IcoVertices[tri[1]], IcoVertices[tri[2]]
		for _, e := range [][2]vec3{{a, b}, {b, c}, {c, a}} {
			d := e[0].sub(e[1])
			l := math.Sqrt(d.dot(d))
			if math.Abs(l-2) > 1e-9 {
				t.Fatalf("face %d edge length %v, want 2", f, l)
			}
		}
		centroid := a.add(b).add(c).scale(1.0 / 3.0)
		if faceGeoms[f].normal.dot(centroid) <= 0 {
			t.Fatalf("face %d normal points inward", f)
		}
	}
}

func TestCenterLatLngRoundTrip(t *testing.T) {
	for _, res := range []int{0, 3, 8, 12} {
		c := FromLatLng(testLat, testLng, res)
		lat, lng := c.CenterLatLng()
		back := FromLatLng(lat, lng, res)
		if back != c {
			t.Fatalf("res %d: center round trip mismatch %s != %s", res, back, c)
		}
	}
}

func TestParentChildHierarchy(t *testing.T) {
	c := FromLatLng(testLat, testLng, testRes)
	// Parent chain: resolution strictly decreases, face preserved.
	cur := c
	for r := testRes; r > 0; r-- {
		p := cur.Parent(r - 1)
		if p.Resolution() != r-1 {
			t.Fatalf("parent resolution = %d, want %d", p.Resolution(), r-1)
		}
		if p.Face() != c.Face() {
			t.Fatalf("parent face changed: %d != %d", p.Face(), c.Face())
		}
		// Direct jump must equal stepwise descent.
		if direct := c.Parent(r - 1); direct != p {
			t.Fatalf("Parent(%d) inconsistent: %s vs %s", r-1, direct, p)
		}
		cur = p
	}
	// Every cell must appear among its parent's children.
	p := c.Parent(testRes - 1)
	found := false
	for _, ch := range p.Children() {
		if ch == c {
			found = true
		}
		if ch.Parent(testRes-1) != p {
			t.Fatalf("child %s does not map back to parent %s", ch, p)
		}
	}
	if !found {
		t.Fatalf("cell %s missing from children of its parent %s", c, p)
	}
}

func TestKRingCountsAndUniqueness(t *testing.T) {
	c := FromLatLng(testLat, testLng, testRes)
	for k := 0; k <= 5; k++ {
		disk := c.KRing(k)
		want := 1 + 3*k*(k+1)
		if len(disk) != want {
			t.Fatalf("k=%d: disk size = %d, want %d", k, len(disk), want)
		}
		seen := make(map[Cell]bool, len(disk))
		for _, cell := range disk {
			if seen[cell] {
				t.Fatalf("k=%d: duplicate cell %s", k, cell)
			}
			seen[cell] = true
		}
		if disk[0] != c {
			t.Fatalf("k=%d: disk[0] = %s, want centre %s", k, disk[0], c)
		}
	}
}

// TestKRingClosedNoHoles verifies the acceptance boundary: a radius-3 disk
// around a res-8 cell is contiguous, closed and hole-free.
func TestKRingClosedNoHoles(t *testing.T) {
	c := FromLatLng(testLat, testLng, testRes)
	const k = 3
	disk := c.KRing(k)
	if len(disk) != 37 {
		t.Fatalf("disk size = %d, want 37", len(disk))
	}
	in := make(map[Cell]bool, len(disk))
	for _, cell := range disk {
		in[cell] = true
	}
	// Every cell's distance from the centre must be <= k, and each ring
	// must contain exactly 6d cells at distance d.
	ringCount := make(map[int]int)
	for _, cell := range disk {
		d, ok := c.GridDistance(cell)
		if !ok {
			t.Fatalf("cell %s not comparable with centre", cell)
		}
		if d > k {
			t.Fatalf("cell %s at distance %d > %d inside disk", cell, d, k)
		}
		ringCount[d]++
	}
	for d := 1; d <= k; d++ {
		if ringCount[d] != 6*d {
			t.Fatalf("ring %d has %d cells, want %d", d, ringCount[d], 6*d)
		}
	}
	// Closure / no holes: every interior cell (distance < k) has all six
	// neighbours inside the disk; every boundary cell (distance == k) is
	// reachable, i.e. has at least one neighbour at distance k-1.
	for _, cell := range disk {
		d, _ := c.GridDistance(cell)
		inner := 0
		for _, nb := range cell.Neighbors() {
			if in[nb] {
				inner++
			}
			nd, _ := c.GridDistance(nb)
			if d < k && !in[nb] {
				t.Fatalf("hole: neighbour %s of interior cell %s (d=%d) missing from disk", nb, cell, d)
			}
			if nd > k+1 {
				t.Fatalf("neighbour jump: %s -> %s distance %d", cell, nb, nd)
			}
		}
		if d == k && inner == 0 {
			t.Fatalf("boundary cell %s disconnected from disk", cell)
		}
		if d < k && inner != 6 {
			t.Fatalf("interior cell %s (d=%d) has %d in-disk neighbours, want 6", cell, d, inner)
		}
	}
}

func TestNeighborsSymmetricAndDistanceOne(t *testing.T) {
	c := FromLatLng(testLat, testLng, testRes)
	for _, nb := range c.Neighbors() {
		d, ok := c.GridDistance(nb)
		if !ok || d != 1 {
			t.Fatalf("neighbour distance = %d, ok=%v; want 1, true", d, ok)
		}
		back := false
		for _, r := range nb.Neighbors() {
			if r == c {
				back = true
			}
		}
		if !back {
			t.Fatalf("neighbour relation not symmetric between %s and %s", c, nb)
		}
	}
}

func TestGridDistanceRingRadii(t *testing.T) {
	c := FromLatLng(testLat, testLng, testRes)
	disk := c.KRing(4)
	off := 0
	for d := 0; d <= 4; d++ {
		for n := 0; n < 1; n++ { // centre
			if d == 0 {
				if dist, _ := c.GridDistance(disk[off]); dist != 0 {
					t.Fatalf("centre distance = %d", dist)
				}
				off++
			}
		}
		if d == 0 {
			continue
		}
		for n := 0; n < 6*d; n++ {
			if dist, _ := c.GridDistance(disk[off]); dist != d {
				t.Fatalf("cell %s distance = %d, want ring %d", disk[off], dist, d)
			}
			off++
		}
	}
}

func TestGridDistanceIncomparable(t *testing.T) {
	a := FromLatLng(testLat, testLng, 8)
	b := a.Parent(4)
	if _, ok := a.GridDistance(b); ok {
		t.Fatal("distance across resolutions must not be comparable")
	}
}

func TestResolutionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for resolution > MaxResolution")
		}
	}()
	FromLatLng(testLat, testLng, MaxResolution+1)
}
