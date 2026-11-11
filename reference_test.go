package pathfill

import "math"

// referenceMask renders the path with an N x N regular supersampling grid per
// pixel using point-sampled winding numbers. It is intentionally simple and
// independent of the analytic accumulator, serving as ground truth.
func referenceMask(width, height, samples int, rule FillRule, p *Path) []float64 {
	contours := flatten(p, 0.05)
	out := make([]float64, width*height)
	inv := 1.0 / float64(samples)

	for y := 0; y < height; y++ {
		for sy := 0; sy < samples; sy++ {
			py := float64(y) + (float64(sy)+0.5)*inv
			for x := 0; x < width; x++ {
				hits := 0
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)*inv
					w := winding(contours, px, py)
					if interior(w, rule) {
						hits++
					}
				}
				out[y*width+x] += float64(hits) * inv
			}
		}
	}

	for i := range out {
		out[i] /= float64(samples)
	}
	return out
}

// winding returns the integer winding number of point (px, py); the fractional
// result only arises when a sample lies exactly on an edge, which the jittered
// grid avoids in practice.
func winding(contours [][]Point, px, py float64) int {
	w := 0
	for _, c := range contours {
		for i := 0; i+1 < len(c); i++ {
			a, b := c[i], c[i+1]
			if a.Y == b.Y {
				continue
			}
			if a.Y <= py && b.Y > py {
				x := a.X + (py-a.Y)*(b.X-a.X)/(b.Y-a.Y)
				if x > px {
					w++
				}
			} else if b.Y <= py && a.Y > py {
				x := a.X + (py-a.Y)*(b.X-a.X)/(b.Y-a.Y)
				if x > px {
					w--
				}
			}
		}
	}
	return w
}

func interior(w int, rule FillRule) bool {
	switch rule {
	case FillEvenOdd:
		return w&1 != 0
	default:
		return w != 0
	}
}

type maskStats struct {
	mae, maxErr, meanSq float64
	badPixels           int
}

func compareMask(mask []byte, ref []float64) maskStats {
	var s maskStats
	for i, r := range ref {
		got := float64(mask[i]) / 255
		d := math.Abs(got - r)
		s.mae += d
		s.meanSq += d * d
		if d > s.maxErr {
			s.maxErr = d
		}
		if d > 0.15 {
			s.badPixels++
		}
	}
	n := float64(len(ref))
	s.mae /= n
	s.meanSq /= n
	return s
}
