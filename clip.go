package gridray

import "math/big"

// ClipSegmentCell clips the segment P(t) = p0 + t*(p1-p0), t in [0,1],
// against the closed cell square using exact Liang-Barsky clipping.
// It reports whether the intersection is non-empty (a single touching
// point counts) and, if so, the entry parameter in [0,1].
func ClipSegmentCell(p0, p1 Point, c Cell) (enter *big.Rat, ok bool) {
	u1 := big.NewRat(0, 1)
	u2 := big.NewRat(1, 1)
	axes := [][4]*big.Rat{
		{p0.X, subRat(p1.X, p0.X), big.NewRat(c.X, 1), big.NewRat(c.X+1, 1)},
		{p0.Y, subRat(p1.Y, p0.Y), big.NewRat(c.Y, 1), big.NewRat(c.Y+1, 1)},
	}
	for _, a := range axes {
		start, d, lo, hi := a[0], a[1], a[2], a[3]
		// Half-planes: -d*t <= start-lo and d*t <= hi-start.
		constraints := [][2]*big.Rat{
			{new(big.Rat).Neg(d), subRat(start, lo)},
			{d, subRat(hi, start)},
		}
		for _, con := range constraints {
			p, q := con[0], con[1]
			switch p.Sign() {
			case 0:
				if q.Sign() < 0 {
					return nil, false
				}
			default:
				r := new(big.Rat).Quo(q, p)
				if p.Sign() < 0 {
					if r.Cmp(u1) > 0 {
						u1 = r
					}
				} else {
					if r.Cmp(u2) < 0 {
						u2 = r
					}
				}
				if u1.Cmp(u2) > 0 {
					return nil, false
				}
			}
		}
	}
	return u1, true
}

// TraverseByClipping is the reference oracle: it clips the segment against
// every cell in the bounding box of the endpoints (expanded by one cell)
// and returns the touched cells sorted like TraverseSegment.
func TraverseByClipping(p0, p1 Point) []Visit {
	loX := floorRat(minRat(p0.X, p1.X)) - 1
	hiX := floorRat(maxRat(p0.X, p1.X)) + 1
	loY := floorRat(minRat(p0.Y, p1.Y)) - 1
	hiY := floorRat(maxRat(p0.Y, p1.Y)) + 1
	var visits []Visit
	for x := loX; x <= hiX; x++ {
		for y := loY; y <= hiY; y++ {
			if t, ok := ClipSegmentCell(p0, p1, Cell{x, y}); ok {
				visits = append(visits, Visit{Cell: Cell{x, y}, T: t})
			}
		}
	}
	SortVisits(visits)
	return visits
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}
