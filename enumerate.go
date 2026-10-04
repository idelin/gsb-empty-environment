package closestpair

import (
	"math/big"
	"sort"
)

// enumerateZero collects every pair of points sharing identical
// coordinates. With k_i points per distinct coordinate, it emits
// sum(C(k_i, 2)) pairs, which is exactly the quadratic blow-up case.
func enumerateZero(pts []bpoint) []Pair {
	groups := make(map[string][]int64)
	for _, p := range pts {
		key := p.x.String() + "," + p.y.String()
		groups[key] = append(groups[key], p.id)
	}
	var pairs []Pair
	for _, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				pairs = append(pairs, Pair{A: ids[i], B: ids[j]})
			}
		}
	}
	return pairs
}

// cellKey identifies a grid cell. Coordinates are arbitrary-precision,
// so the cell indices are rendered as strings.
type cellKey struct{ x, y string }

// enumeratePositive collects every pair whose squared distance equals
// d (d > 0) using a uniform grid of cell side s = floor(sqrt(d)).
//
// Any pair at squared distance d has integer coordinate differences
// |dx|, |dy| <= floor(sqrt(d)) = s, so the two points land in cells at
// most one apart on each axis; checking the 3x3 neighborhood therefore
// cannot miss a tied pair. Each unordered pair is emitted exactly once
// because only already-inserted points are examined.
func enumeratePositive(pts []bpoint, d *big.Int) []Pair {
	s := new(big.Int).Sqrt(d) // s >= 1
	grid := make(map[cellKey][]int)
	var pairs []Pair
	for i, p := range pts {
		cx := floorDiv(p.x, s)
		cy := floorDiv(p.y, s)
		for ox := int64(-1); ox <= 1; ox++ {
			nx := new(big.Int).Add(cx, big.NewInt(ox))
			for oy := int64(-1); oy <= 1; oy++ {
				ny := new(big.Int).Add(cy, big.NewInt(oy))
				for _, j := range grid[cellKey{nx.String(), ny.String()}] {
					if dist2(p, pts[j]).Cmp(d) == 0 {
						pairs = append(pairs, newPair(p.id, pts[j].id))
					}
				}
			}
		}
		key := cellKey{cx.String(), cy.String()}
		grid[key] = append(grid[key], i)
	}
	return pairs
}

// floorDiv returns floor(v / s) for s > 0 (big.Int QuoRem truncates
// toward zero, which is not floor for negative v).
func floorDiv(v, s *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(v, s, new(big.Int))
	if r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}
