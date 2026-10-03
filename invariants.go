package hexgrid

import "fmt"

// AssertCell verifies the bit-level invariant of one cell index.
func AssertCell(c Cell) error {
	if !c.IsValid() {
		return fmt.Errorf("hexgrid: cell %016x violates bit-layout invariant", uint64(c))
	}
	return nil
}

// AssertHierarchy verifies parent/child consistency for a cell:
// parent round-trip (if res > 0) and 7 distinct children whose parent
// is the cell (if res < MaxRes).
func AssertHierarchy(c Cell) error {
	if err := AssertCell(c); err != nil {
		return err
	}
	res := c.Res()
	if res > 0 {
		p, err := c.Parent()
		if err != nil {
			return err
		}
		if p.Res() != res-1 {
			return fmt.Errorf("hexgrid: parent resolution %d, want %d", p.Res(), res-1)
		}
		back, err := p.Child(c.Digit(res))
		if err != nil {
			return err
		}
		if back != c {
			return fmt.Errorf("hexgrid: parent/child round trip broke: %s -> %s -> %s", c, p, back)
		}
	}
	if res < MaxRes {
		kids, err := c.Children()
		if err != nil {
			return err
		}
		if len(kids) != 7 {
			return fmt.Errorf("hexgrid: %d children, want 7", len(kids))
		}
		seen := make(map[Cell]bool, 7)
		for _, k := range kids {
			if seen[k] {
				return fmt.Errorf("hexgrid: duplicate child %s", k)
			}
			seen[k] = true
			kp, err := k.Parent()
			if err != nil {
				return err
			}
			if kp != c {
				return fmt.Errorf("hexgrid: child %s has parent %s, want %s", k, kp, c)
			}
		}
	}
	return nil
}

// AssertKRing verifies the full K-ring invariant battery for a center
// cell and radius k:
//
//   - cardinality: exactly 1 + 3k(k+1) cells, all unique, same
//     resolution and base cell, center included;
//   - completeness (no holes): every lattice point within distance k
//     maps to a cell present in the ring;
//   - contiguity: every non-center cell has a neighbor strictly closer
//     to the center inside the set;
//   - symmetry: every member's own radius-k ring contains the center;
//   - closed coverage: hexagon boundaries form exactly one closed loop.
func AssertKRing(center Cell, k int) error {
	cells, err := KRing(center, k)
	if err != nil {
		return err
	}
	want := 1 + 3*k*(k+1)
	if len(cells) != want {
		return fmt.Errorf("hexgrid: k-ring(%d) has %d cells, want %d", k, len(cells), want)
	}
	set := make(map[Cell]bool, len(cells))
	for _, c := range cells {
		if set[c] {
			return fmt.Errorf("hexgrid: duplicate cell %s in k-ring", c)
		}
		set[c] = true
		if c.Res() != center.Res() || c.BaseCell() != center.BaseCell() {
			return fmt.Errorf("hexgrid: ring cell %s differs in resolution/base cell", c)
		}
	}
	if !set[center] {
		return fmt.Errorf("hexgrid: k-ring does not contain its center %s", center)
	}

	// Completeness: BFS the IJ lattice ball of radius k and require a
	// 1:1 correspondence with the returned cells.
	cij := center.ij()
	seen := map[IJ]bool{cij: true}
	frontier := []IJ{cij}
	for d := 0; d < k; d++ {
		var next []IJ
		for _, p := range frontier {
			for _, dir := range ijDirections {
				q := IJ{p.I + dir.I, p.J + dir.J}
				if !seen[q] {
					seen[q] = true
					next = append(next, q)
				}
			}
		}
		frontier = next
	}
	if len(seen) != want {
		return fmt.Errorf("hexgrid: lattice ball has %d points, ring has %d", len(seen), want)
	}
	for p := range seen {
		cell, err := cellFromIJ(center.BaseCell(), center.Res(), p)
		if err != nil {
			return fmt.Errorf("hexgrid: lattice point %v within radius %d unrepresentable: %v", p, k, err)
		}
		if !set[cell] {
			return fmt.Errorf("hexgrid: hole: cell %s within radius %d missing from ring", cell, k)
		}
	}

	// Contiguity: every non-center cell touches a cell one step closer.
	for _, c := range cells {
		if c == center {
			continue
		}
		d, err := RingDistance(center, c)
		if err != nil {
			return err
		}
		nbs, err := Neighbors(c)
		if err != nil {
			return err
		}
		ok := false
		for _, nb := range nbs {
			if !set[nb] {
				continue
			}
			nd, err := RingDistance(center, nb)
			if err != nil {
				return err
			}
			if nd == d-1 {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("hexgrid: cell %s at distance %d has no inward neighbor (gap)", c, d)
		}
	}

	// Symmetry: center must appear in every member's radius-k ring.
	for _, c := range cells {
		back, err := KRing(c, k)
		if err != nil {
			return fmt.Errorf("hexgrid: reverse ring from %s: %v", c, err)
		}
		found := false
		for _, b := range back {
			if b == center {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("hexgrid: asymmetric ring: %s in ring(%s) but not vice versa", c, center)
		}
	}

	if err := VerifyClosedCoverage(cells); err != nil {
		return fmt.Errorf("hexgrid: coverage broken: %v", err)
	}
	return nil
}
