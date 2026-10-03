package hexgrid

import (
	"math/rand"
	"sync"
	"testing"
)

// beijingCell is the golden encoding of (39.9042, 116.4074) at res 8.
const beijingCell = "0c120000ab000020"

func TestEncodeGoldenBeijing(t *testing.T) {
	c := Encode(39.9042, 116.4074, 8)
	if got := c.String(); got != beijingCell {
		t.Fatalf("Encode(Beijing, 8) = %s, want %s", got, beijingCell)
	}
	if c.Resolution() != 8 {
		t.Fatalf("resolution = %d, want 8", c.Resolution())
	}
	if c.Mode() != modeCell {
		t.Fatalf("mode = %d, want %d", c.Mode(), modeCell)
	}
}

func TestEncodeStringRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		lat := rng.Float64()*170 - 85
		lng := rng.Float64()*360 - 180
		res := rng.Intn(13)
		c := Encode(lat, lng, res)
		if !c.Valid() {
			t.Fatalf("invalid cell %s from (%f, %f, %d)", c, lat, lng, res)
		}
		c2, err := Parse(c.String())
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.String(), err)
		}
		if c2 != c {
			t.Fatalf("round trip mismatch: %s -> %s", c, c2)
		}
		// Re-encoding the cell centroid must yield the same cell.
		clat, clng := c.Center()
		if c3 := Encode(clat, clng, res); c3 != c {
			t.Fatalf("center re-encode mismatch: %s vs %s", c, c3)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, s := range []string{
		"", "zz", "0x1", "0000000000000000", "ffffffffffffffff",
		"0c120000c50000250", "8c120000c5000025",
	} {
		if _, err := Parse(s); err == nil {
			t.Fatalf("Parse(%q): expected error", s)
		}
	}
}

func TestEncodeInvalidResolutionPanics(t *testing.T) {
	for _, res := range []int{-1, 16, 100} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("Encode(res=%d): expected panic", res)
				}
			}()
			Encode(0, 0, res)
		}()
	}
}

func TestParentChildConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	totalChildren := 0
	for i := 0; i < 500; i++ {
		res := 1 + rng.Intn(12)
		c := Encode(rng.Float64()*170-85, rng.Float64()*360-180, res)

		p, err := c.Parent(res - 1)
		if err != nil {
			t.Fatalf("Parent: %v", err)
		}
		if p.Resolution() != res-1 {
			t.Fatalf("parent resolution = %d, want %d", p.Resolution(), res-1)
		}
		// Parent of parent chains up to res 0.
		if res >= 2 {
			gp, err := p.Parent(res - 2)
			if err != nil || gp.Resolution() != res-2 {
				t.Fatalf("grandparent: %v res=%d", err, gp.Resolution())
			}
		}
		// The cell must appear among its parent's children, and every
		// child's parent must be p.
		children, err := p.Children()
		if err != nil {
			t.Fatalf("Children: %v", err)
		}
		// Aperture-4 hierarchy: 4 children on average, with bounded
		// spread even at face edges and vertices.
		if len(children) < 1 || len(children) > 12 {
			t.Fatalf("children count = %d, want 1..12", len(children))
		}
		totalChildren += len(children)
		found := false
		for _, ch := range children {
			if ch == c {
				found = true
			}
			if cp, err := ch.Parent(res - 1); err != nil || cp != p {
				t.Fatalf("child %s parent mismatch", ch)
			}
		}
		if !found {
			t.Fatalf("cell %s not among children of its parent %s", c, p)
		}
	}
	if avg := float64(totalChildren) / 500; avg < 3 || avg > 5 {
		t.Fatalf("average children = %.2f, want aperture-4 (~4)", avg)
	}
}

func TestParentInvalidResolution(t *testing.T) {
	c := Encode(39.9042, 116.4074, 8)
	for _, res := range []int{-1, 9, 16} {
		if _, err := c.Parent(res); err != ErrInvalidResolution {
			t.Fatalf("Parent(%d): err = %v", res, err)
		}
	}
	if _, err := c.Children(); err != nil {
		t.Fatalf("Children: %v", err)
	}
	maxC := Encode(39.9042, 116.4074, MaxResolution)
	if _, err := maxC.Children(); err != ErrInvalidResolution {
		t.Fatalf("Children at max res: err = %v", err)
	}
}

func TestKRingDiskInterior(t *testing.T) {
	origin := Encode(39.9042, 116.4074, 8)
	disk := KRingDisk(origin, 3)
	if len(disk) != 37 {
		t.Fatalf("disk size = %d, want 37", len(disk))
	}
	ring := KRing(origin, 3)
	if len(ring) != 18 {
		t.Fatalf("ring size = %d, want 18", len(ring))
	}
	if got := KRing(origin, 0); len(got) != 1 || got[0] != origin {
		t.Fatalf("k=0 ring = %v, want [origin]", got)
	}

	seen := make(map[Cell]bool, len(disk))
	for _, c := range disk {
		if seen[c] {
			t.Fatalf("duplicate cell %s in disk", c)
		}
		seen[c] = true
		if !c.Valid() {
			t.Fatalf("invalid cell %s in disk", c)
		}
	}
	// Hole-free: every non-origin cell touches another disk cell.
	for _, c := range disk {
		if c == origin {
			continue
		}
		ok := false
		for _, n := range c.Neighbors() {
			if seen[n] {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("hole: cell %s is isolated inside the disk", c)
		}
	}
	// Closed coverage: every neighbor of a non-boundary disk cell is in
	// the disk (no holes, no escapes).
	for _, c := range disk {
		if d := GridDistance(origin, c); d < 3 {
			for _, n := range c.Neighbors() {
				if !seen[n] {
					t.Fatalf("neighbor %s of inner cell %s escaped the disk", n, c)
				}
			}
		}
	}
	// Ring cells are exactly the disk boundary at distance k.
	for _, c := range ring {
		if d := GridDistance(origin, c); d != 3 {
			t.Fatalf("ring cell %s at distance %d, want 3", c, d)
		}
	}
}

func TestNeighborSymmetry(t *testing.T) {
	origin := Encode(39.9042, 116.4074, 8)
	for _, a := range KRingDisk(origin, 2) {
		for _, b := range a.Neighbors() {
			found := false
			for _, bb := range b.Neighbors() {
				if bb == a {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("asymmetric adjacency: %s -> %s", a, b)
			}
		}
	}
}

func TestKRingAcrossFaceEdge(t *testing.T) {
	// Midpoint of the icosahedron edge between vertices 0 and 8.
	verts := icosaVertices()
	mid := verts[0].add(verts[8]).normalized()
	lat, lng := vecToLatLng(mid)
	origin := Encode(lat, lng, 6)
	disk := KRingDisk(origin, 4)
	if len(disk) < 30 {
		t.Fatalf("suspiciously small disk: %d cells", len(disk))
	}
	seen := make(map[Cell]bool, len(disk))
	faceSet := make(map[int]bool)
	for _, c := range disk {
		if seen[c] {
			t.Fatalf("duplicate cell %s", c)
		}
		seen[c] = true
		if !c.Valid() {
			t.Fatalf("invalid cell %s", c)
		}
		faceSet[c.Face()] = true
	}
	if len(faceSet) < 2 {
		t.Fatal("expected disk to straddle at least two faces")
	}
	// Adjacency stays symmetric across the face edge.
	for _, a := range disk {
		for _, b := range a.Neighbors() {
			found := false
			for _, bb := range b.Neighbors() {
				if bb == a {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("asymmetric adjacency across edge: %s -> %s", a, b)
			}
		}
	}
	for _, c := range disk {
		if c == origin {
			continue
		}
		ok := false
		for _, n := range c.Neighbors() {
			if seen[n] {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("hole at %s near face edge", c)
		}
	}
}

func TestKRingNearIcosaVertex(t *testing.T) {
	verts := icosaVertices()
	lat, lng := vecToLatLng(verts[0].normalized())
	origin := Encode(lat, lng, 3)
	disk := KRingDisk(origin, 3)
	seen := make(map[Cell]bool, len(disk))
	minN, maxN := 7, 0
	for _, c := range disk {
		seen[c] = true
		if !c.Valid() {
			t.Fatalf("invalid cell %s", c)
		}
		n := len(c.Neighbors())
		if n < minN {
			minN = n
		}
		if n > maxN {
			maxN = n
		}
	}
	if maxN > 6 || minN < 5 {
		t.Fatalf("neighbor counts in [%d, %d], want within [5, 6]", minN, maxN)
	}
	// Still hole-free around the pentagon defect.
	for _, c := range disk {
		if c == origin {
			continue
		}
		ok := false
		for _, n := range c.Neighbors() {
			if seen[n] {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("hole at %s near icosa vertex", c)
		}
	}
}

func TestGridDistance(t *testing.T) {
	origin := Encode(39.9042, 116.4074, 8)
	if d := GridDistance(origin, origin); d != 0 {
		t.Fatalf("self distance = %d", d)
	}
	for _, n := range origin.Neighbors() {
		if d := GridDistance(origin, n); d != 1 {
			t.Fatalf("neighbor distance = %d, want 1", d)
		}
	}
}

func TestConcurrentEncodeAndTraverse(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				c := Encode(rng.Float64()*170-85, rng.Float64()*360-180, 8)
				disk := KRingDisk(c, 2)
				if len(disk) == 0 {
					t.Error("empty disk")
				}
				if _, err := c.Parent(4); err != nil {
					t.Error(err)
				}
			}
		}(int64(g + 1))
	}
	wg.Wait()
}
