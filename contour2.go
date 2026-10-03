package sdf

// Segment2 is a line segment of an extracted 2D iso-contour.
type Segment2 struct {
	A, B Vec2
}

// Contour extracts the iso-contour (zero level set by default) of the
// rasterized field using the marching squares algorithm. Ambiguous
// cells (saddle cases 5 and 10) are resolved with the asymptotic
// decider using the cell-center value.
func (g *Grid2) Contour(iso float64) []Segment2 {
	var segs []Segment2
	for j := 0; j < g.Ny-1; j++ {
		for i := 0; i < g.Nx-1; i++ {
			p0 := g.Position(i, j)
			p1 := g.Position(i+1, j)
			p2 := g.Position(i+1, j+1)
			p3 := g.Position(i, j+1)
			v0 := g.At(i, j)
			v1 := g.At(i+1, j)
			v2 := g.At(i+1, j+1)
			v3 := g.At(i, j+1)

			idx := 0
			if v0 < iso {
				idx |= 1
			}
			if v1 < iso {
				idx |= 2
			}
			if v2 < iso {
				idx |= 4
			}
			if v3 < iso {
				idx |= 8
			}
			if idx == 0 || idx == 15 {
				continue
			}

			// Interpolated crossing points on the four cell edges.
			bottom := p0.Lerp(p1, (iso-v0)/(v1-v0))
			right := p1.Lerp(p2, (iso-v1)/(v2-v1))
			top := p3.Lerp(p2, (iso-v3)/(v2-v3))
			left := p0.Lerp(p3, (iso-v0)/(v3-v0))

			switch idx {
			case 1, 14:
				segs = append(segs, Segment2{left, bottom})
			case 2, 13:
				segs = append(segs, Segment2{bottom, right})
			case 3, 12:
				segs = append(segs, Segment2{left, right})
			case 4, 11:
				segs = append(segs, Segment2{right, top})
			case 6, 9:
				segs = append(segs, Segment2{bottom, top})
			case 7, 8:
				segs = append(segs, Segment2{left, top})
			case 5:
				if (v0+v1+v2+v3)/4 < iso {
					segs = append(segs, Segment2{left, top}, Segment2{bottom, right})
				} else {
					segs = append(segs, Segment2{left, bottom}, Segment2{right, top})
				}
			case 10:
				if (v0+v1+v2+v3)/4 < iso {
					segs = append(segs, Segment2{left, bottom}, Segment2{right, top})
				} else {
					segs = append(segs, Segment2{left, top}, Segment2{bottom, right})
				}
			}
		}
	}
	return segs
}
