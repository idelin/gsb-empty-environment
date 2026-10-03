package hexgrid

import "fmt"

// Violation describes a broken formal invariant.
type Violation struct {
	Invariant string
	Detail    string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s", v.Invariant, v.Detail)
}

// ExpectedKRingSize is the closed-form size of a k-ring that does
// not touch a face boundary: 1 + 3k(k+1).
func ExpectedKRingSize(k int) int {
	if k < 0 {
		k = 0
	}
	return 1 + 3*k*(k+1)
}

// AssertCellValid checks the structural bit-layout invariants of a
// cell identifier.
func AssertCellValid(c Cell) []Violation {
	var out []Violation
	if !c.IsValid() {
		out = append(out, Violation{
			Invariant: "cell-valid",
			Detail:    fmt.Sprintf("cell %s fails structural validation (mode/res/face/index)", c),
		})
	}
	return out
}

// AssertHierarchy checks the aperture-4 hierarchy invariants:
// parent(child) == cell for every child, cell is among its parent's
// children, children are unique, and ancestor chains terminate at a
// resolution-0 root.
func AssertHierarchy(c Cell) []Violation {
	var out []Violation
	if !c.IsValid() {
		return append(out, Violation{Invariant: "hierarchy", Detail: "invalid cell"})
	}
	res := c.Resolution()
	if children, ok := c.Children(); ok {
		seen := make(map[Cell]struct{}, 4)
		for _, ch := range children {
			if !ch.IsValid() {
				out = append(out, Violation{Invariant: "hierarchy/child-valid", Detail: fmt.Sprintf("child %s of %s is invalid", ch, c)})
			}
			if _, dup := seen[ch]; dup {
				out = append(out, Violation{Invariant: "hierarchy/child-unique", Detail: fmt.Sprintf("duplicate child %s of %s", ch, c)})
			}
			seen[ch] = struct{}{}
			if p, _ := ch.Parent(); p != c {
				out = append(out, Violation{Invariant: "hierarchy/parent-of-child", Detail: fmt.Sprintf("parent(%s) != %s", ch, c)})
			}
		}
	} else if res != MaxRes {
		out = append(out, Violation{Invariant: "hierarchy/children-exist", Detail: fmt.Sprintf("res-%d cell %s unexpectedly has no children", res, c)})
	}
	if p, ok := c.Parent(); ok {
		siblings, _ := p.Children()
		found := false
		for _, s := range siblings {
			if s == c {
				found = true
			}
		}
		if !found {
			out = append(out, Violation{Invariant: "hierarchy/child-of-parent", Detail: fmt.Sprintf("%s not among children of its parent %s", c, p)})
		}
	} else if res != 0 {
		out = append(out, Violation{Invariant: "hierarchy/parent-exists", Detail: fmt.Sprintf("res-%d cell %s unexpectedly has no parent", res, c)})
	}
	// Ancestor chain must terminate at a res-0 root on the same face.
	cur := c
	for cur.Resolution() > 0 {
		p, ok := cur.Parent()
		if !ok {
			out = append(out, Violation{Invariant: "hierarchy/root-reachable", Detail: fmt.Sprintf("ancestor chain of %s breaks at %s", c, cur)})
			break
		}
		if p.Face() != c.Face() {
			out = append(out, Violation{Invariant: "hierarchy/face-stable", Detail: fmt.Sprintf("ancestor %s of %s is on a different face", p, c)})
		}
		cur = p
	}
	return out
}

// AssertKRing checks the topological invariants of the k-ring around
// c: no duplicates, contiguity (every non-origin cell has a neighbor
// one step closer to the origin, i.e. the coverage is closed with no
// holes), symmetry of the neighbor relation for ring members, and —
// when the ring stays on a single face — the exact closed-form size
// 1 + 3k(k+1).
func AssertKRing(c Cell, k int) []Violation {
	var out []Violation
	dist := KRingDistances(c, k)

	// Exact-size invariant for single-face rings.
	sameFace := true
	for cell := range dist {
		if cell.Face() != c.Face() {
			sameFace = false
			break
		}
	}
	if sameFace && len(dist) != ExpectedKRingSize(k) {
		out = append(out, Violation{
			Invariant: "kring/size",
			Detail:    fmt.Sprintf("single-face k=%d ring around %s has %d cells, want %d", k, c, len(dist), ExpectedKRingSize(k)),
		})
	}

	// Contiguity / no-holes: every cell at distance d > 0 must be
	// adjacent to a cell at distance d-1.
	for cell, d := range dist {
		if d == 0 {
			continue
		}
		closer := false
		for _, nb := range Neighbors(cell) {
			if nd, ok := dist[nb]; ok && nd == d-1 {
				closer = true
				break
			}
		}
		if !closer {
			out = append(out, Violation{
				Invariant: "kring/contiguous",
				Detail:    fmt.Sprintf("cell %s at distance %d has no neighbor at distance %d (hole in coverage)", cell, d, d-1),
			})
		}
	}

	// Symmetry: for every ring member m adjacent to the origin's
	// neighborhood, membership must be mutual.
	for _, nb := range Neighbors(c) {
		if _, inRing := dist[nb]; !inRing {
			continue
		}
		back := false
		for _, rn := range Neighbors(nb) {
			if rn == c {
				back = true
				break
			}
		}
		if !back {
			out = append(out, Violation{
				Invariant: "kring/symmetric",
				Detail:    fmt.Sprintf("%s lists %s as neighbor but not vice versa", c, nb),
			})
		}
	}
	return out
}

// AssertEncodeRoundTrip checks that encoding is deterministic and
// self-consistent: encoding the same coordinate twice yields the
// same valid cell, and — when the cell's center lies inside its
// face triangle — the center re-encodes to the cell itself. (Cells
// straddling a face seam may legitimately have their center owned
// by a neighboring face, so the center check is conditional.)
func AssertEncodeRoundTrip(lat, lng float64, res int) []Violation {
	var out []Violation
	c1, err1 := LatLngToCell(lat, lng, res)
	c2, err2 := LatLngToCell(lat, lng, res)
	if err1 != nil || err2 != nil {
		return append(out, Violation{Invariant: "encode/valid-input", Detail: fmt.Sprintf("encode failed: %v / %v", err1, err2)})
	}
	if c1 != c2 {
		out = append(out, Violation{Invariant: "encode/deterministic", Detail: fmt.Sprintf("encode(%v,%v) gave %s then %s", lat, lng, c1, c2)})
	}
	if !c1.IsValid() {
		out = append(out, Violation{Invariant: "encode/valid", Detail: fmt.Sprintf("encode(%v,%v) gave invalid cell %s", lat, lng, c1)})
	}
	f, r := c1.Face(), c1.Resolution()
	i, j := c1.IJ()
	if seamDistance(f, r, i, j) <= 0 {
		return out
	}
	clat, clng := c1.CenterLatLng()
	back, err := LatLngToCell(clat, clng, r)
	if err != nil {
		out = append(out, Violation{Invariant: "encode/center-encodable", Detail: err.Error()})
	} else if back != c1 {
		out = append(out, Violation{Invariant: "encode/center-roundtrip", Detail: fmt.Sprintf("center of %s re-encodes to %s", c1, back)})
	}
	return out
}
