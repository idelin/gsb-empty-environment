package contour

// Replay rasterises boundary loops back into a binary image. For every
// region its outer loops are filled and its hole loops cleared, so
// Replay(Extract(im)) reproduces im exactly (see Validate). Pixel centers
// sit at half-integer coordinates and never lie on the integer grid
// boundary, which keeps point-in-polygon tests exact even for
// self-touching loops along slits.
func Replay(res *Result) *Image {
	im := NewImage(res.W, res.H)
	for _, r := range res.Regions {
		for y := 0; y < res.H; y++ {
			for x := 0; x < res.W; x++ {
				inside := false
				for _, li := range r.Loops {
					lp := &res.Loops[li]
					if loopContains(lp, x, y) {
						inside = !lp.Hole
					}
				}
				if inside {
					im.Set(x, y, true)
				}
			}
		}
	}
	return im
}

// loopContains reports whether the center of pixel (x, y) lies inside the
// loop, using even-odd ray casting toward +x. Only vertical edges cross
// the horizontal ray; the half-open row interval [y1, y2) makes each
// crossing count exactly once.
func loopContains(l *Loop, x, y int) bool {
	inside := false
	for i := 0; i+1 < len(l.Points); i++ {
		p, q := l.Points[i], l.Points[i+1]
		if p.X != q.X { // skip horizontal edges
			continue
		}
		y1, y2 := p.Y, q.Y
		if y1 > y2 {
			y1, y2 = y2, y1
		}
		if y1 <= y && y < y2 && p.X > x {
			inside = !inside
		}
	}
	return inside
}
