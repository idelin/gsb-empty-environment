package closestpair

import (
	"math/big"
	"sort"
)

// minDist2 returns the exact minimum squared distance over all pairs
// of pts. It first checks for duplicate coordinates (distance zero),
// then runs a divide-and-conquer scan. All arithmetic uses big.Int so
// squared differences of int64-range coordinates cannot overflow.
func minDist2(pts []bpoint) *big.Int {
	byCoord := make([]bpoint, len(pts))
	copy(byCoord, pts)
	sort.Slice(byCoord, func(i, j int) bool {
		if c := byCoord[i].x.Cmp(byCoord[j].x); c != 0 {
			return c < 0
		}
		return byCoord[i].y.Cmp(byCoord[j].y) < 0
	})
	for i := 0; i+1 < len(byCoord); i++ {
		if byCoord[i].x.Cmp(byCoord[i+1].x) == 0 &&
			byCoord[i].y.Cmp(byCoord[i+1].y) == 0 {
			return new(big.Int)
		}
	}

	px := make([]bpoint, len(pts))
	copy(px, pts)
	sort.Slice(px, func(i, j int) bool {
		if c := px[i].x.Cmp(px[j].x); c != 0 {
			return c < 0
		}
		return px[i].y.Cmp(px[j].y) < 0
	})
	py := make([]bpoint, len(pts))
	copy(py, pts)
	sort.Slice(py, func(i, j int) bool {
		if c := py[i].y.Cmp(py[j].y); c != 0 {
			return c < 0
		}
		return py[i].x.Cmp(py[j].x) < 0
	})
	return conquer(px, py)
}

// conquer computes the minimum squared distance among px (sorted by x),
// with py holding the same points sorted by y.
func conquer(px, py []bpoint) *big.Int {
	n := len(px)
	if n <= 3 {
		d := dist2(px[0], px[1])
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if t := dist2(px[i], px[j]); t.Cmp(d) < 0 {
					d = t
				}
			}
		}
		return d
	}

	mid := n / 2
	midX := px[mid].x
	inLeft := make(map[int64]bool, mid)
	for _, p := range px[:mid] {
		inLeft[p.id] = true
	}
	pyl := make([]bpoint, 0, mid)
	pyr := make([]bpoint, 0, n-mid)
	for _, p := range py {
		if inLeft[p.id] {
			pyl = append(pyl, p)
		} else {
			pyr = append(pyr, p)
		}
	}

	d := conquer(px[:mid], pyl)
	if dr := conquer(px[mid:], pyr); dr.Cmp(d) < 0 {
		d = dr
	}

	// Only pairs with squared distance < d can improve d, and such a
	// cross pair must lie within (x-midX)^2 < d of the split line.
	tmp := new(big.Int)
	var strip []bpoint
	for _, p := range py {
		tmp.Sub(p.x, midX)
		tmp.Mul(tmp, tmp)
		if tmp.Cmp(d) < 0 {
			strip = append(strip, p)
		}
	}
	for i := 0; i < len(strip); i++ {
		for j := i + 1; j < len(strip); j++ {
			tmp.Sub(strip[j].y, strip[i].y)
			tmp.Mul(tmp, tmp)
			if tmp.Cmp(d) >= 0 {
				break
			}
			if t := dist2(strip[i], strip[j]); t.Cmp(d) < 0 {
				d = t
			}
		}
	}
	return d
}
