package pathfill

import (
	"math"
	"sort"
)

// Rasterizer accumulates edges and produces an 8-bit alpha coverage mask.
//
// Coverage is computed analytically: every edge contributes the signed area
// of the trapezoid it spans across each pixel row, so overlapping and
// self-intersecting subpaths combine through their winding numbers.
type Rasterizer struct {
	Width  int
	Height int
	// Flatness is the maximum permitted deviation (in pixels) of a flattened
	// Bézier from its chord. Zero defaults to 0.25.
	Flatness float64

	rows []rowEvents
}

// NewRasterizer creates a rasterizer for a width x height canvas. Coordinates
// outside [0,width] x [0,height] are clipped against the canvas.
func NewRasterizer(width, height int) *Rasterizer {
	if width < 0 || height < 0 {
		panic("pathfill: negative rasterizer dimensions")
	}
	r := &Rasterizer{Width: width, Height: height, Flatness: 0.25}
	r.rows = make([]rowEvents, height)
	return r
}

// Reset drops all accumulated edges while retaining the canvas size.
func (r *Rasterizer) Reset() {
	for i := range r.rows {
		r.rows[i] = r.rows[i][:0]
	}
}

// AddPath flattens and rasterizes all subpaths of p into the accumulator.
// Multiple paths may be added before reading the mask; their winding values
// add together.
func (r *Rasterizer) AddPath(p *Path) {
	tol := r.Flatness
	if tol <= 0 {
		tol = 0.25
	}
	for _, contour := range flatten(p, tol) {
		for i := 0; i+1 < len(contour); i++ {
			r.addEdge(contour[i], contour[i+1])
		}
	}
}

// AddContour adds a single closed polyline (the last point is connected back
// to the first if it is not already identical).
func (r *Rasterizer) AddContour(points []Point) {
	if len(points) < 2 {
		return
	}
	for i := 0; i+1 < len(points); i++ {
		r.addEdge(points[i], points[i+1])
	}
	if points[0] != points[len(points)-1] {
		r.addEdge(points[len(points)-1], points[0])
	}
}

// event describes a kink of the winding integral f(x) on one pixel row.
// At position X the function jumps by step and its slope changes by slope.
type event struct {
	x     float64
	step  float64
	slope float64
}

type rowEvents []event

// addEdge integrates one oriented segment against all pixel rows it crosses.
//
// On row j (band [j, j+1]) the edge's winding number is a step for a vertical
// edge or a linear ramp for a slanted one: zero to the left of the edge,
// equal to the signed band height to its right. Each kink becomes an event;
// kinks left of the canvas are folded into the initial state at x=0.
// Horizontal edges enclose no area and are skipped.
func (r *Rasterizer) addEdge(a, b Point) {
	if math.IsNaN(a.X) || math.IsNaN(a.Y) || math.IsNaN(b.X) || math.IsNaN(b.Y) {
		return
	}
	if math.IsInf(a.X, 0) || math.IsInf(a.Y, 0) || math.IsInf(b.X, 0) || math.IsInf(b.Y, 0) {
		return
	}
	if a.Y == b.Y {
		return
	}

	lo, hi := a.Y, b.Y
	dir := 1.0
	if lo > hi {
		lo, hi = hi, lo
		dir = -1.0
	}

	top := float64(r.Height)
	if lo >= top || hi <= 0 {
		return
	}
	if lo < 0 {
		lo = 0
	}
	if hi > top {
		hi = top
	}

	j0 := int(math.Floor(lo))
	jTop := int(math.Ceil(hi))
	dxdy := (b.X - a.X) / (b.Y - a.Y)

	for j := j0; j < jTop; j++ {
		yLow := float64(j)
		yHigh := yLow + 1
		if yLow < lo {
			yLow = lo
		}
		if yHigh > hi {
			yHigh = hi
		}
		band := yHigh - yLow
		if band <= 0 {
			continue
		}

		xl := a.X + (yLow-a.Y)*dxdy
		xr := a.X + (yHigh-a.Y)*dxdy
		c0, c1 := xl, xr
		if c0 > c1 {
			c0, c1 = c1, c0
		}

		widthF := float64(r.Width)
		w := dir * band

		if c1 <= c0 {
			// Vertical edge: winding jumps by w at c0, i.e. f(x)=w for x>c0.
			if c0 > 0 && c0 < widthF {
				r.rows[j] = append(r.rows[j], event{x: c0, step: w})
			} else if c0 <= 0 {
				r.rows[j] = append(r.rows[j], event{x: 0, step: w})
			}
			continue
		}

		// Winding integral of this edge on the row:
		//   f(x) = 0                 for x <= c0
		//        = w*(x-c0)/(c1-c0)  for c0 < x < c1
		//        = w                 for x >= c1
		// i.e. zero to the left of the edge, ramping to the full winding to
		// its right.
		m := w / (c1 - c0)
		var f0, s0 float64
		switch {
		case c1 <= 0:
			// Entirely to the left of the canvas: full winding everywhere.
			f0 = w
		case c0 >= widthF:
			// Entirely to the right: no contribution on the canvas.
			continue
		case c0 <= 0 && c1 > 0:
			// Ramp already in progress at the canvas boundary.
			f0 = m * (0 - c0)
			s0 = m
		default:
			// Ramp starts inside the canvas; f(0) = 0.
		}

		var evs []event
		if f0 != 0 || s0 != 0 {
			evs = append(evs, event{x: 0, step: f0, slope: s0})
		}
		if c0 > 0 && c0 < widthF && s0 == 0 {
			evs = append(evs, event{x: c0, slope: m})
		}
		if c1 > 0 && c1 < widthF {
			evs = append(evs, event{x: c1, slope: -m})
		}
		r.rows[j] = append(r.rows[j], evs...)
	}
}

// Alpha resolves all accumulated edges into an 8-bit coverage mask of length
// Width*Height, row-major. Pixel (x, y) is at index y*Width+x. A fully covered
// pixel is 255; the value is premultiplied by the fill rule's interior
// fraction of the pixel area.
func (r *Rasterizer) Alpha(rule FillRule) []byte {
	mask := make([]byte, r.Width*r.Height)
	coverage := make([]float64, r.Width)
	width := float64(r.Width)

	for j, evs := range r.rows {
		for i := range coverage {
			coverage[i] = 0
		}
		if len(evs) == 0 {
			continue
		}

		sort.SliceStable(evs, func(i, k int) bool { return evs[i].x < evs[k].x })

		f, slope := 0.0, 0.0
		prev := 0.0
		idx := 0
		for idx < len(evs) {
			c := evs[idx].x
			if c < 0 {
				c = 0
			}
			if c > width {
				c = width
			}
			if c > prev && slope != 0 {
				r.integrate(coverage, prev, c, f, slope)
			} else if c > prev && f != 0 {
				r.integrate(coverage, prev, c, f, 0)
			}
			f += (c - prev) * slope

			next := idx
			for next < len(evs) && evs[next].x <= c+1e-10 {
				f += evs[next].step
				slope += evs[next].slope
				next++
			}
			prev = c
			idx = next
			if prev >= width {
				break
			}
		}
		if prev < width && (f != 0 || slope != 0) {
			r.integrate(coverage, prev, width, f, slope)
		}

		rowBase := j * r.Width
		for i, area := range coverage {
			mask[rowBase+i] = coverageByte(area, rule)
		}
	}
	return mask
}

// integrate adds the integral of the linear function f(x)=f0+slope*(x-a) over
// [a, b) to the per-pixel signed-area accumulators.
func (r *Rasterizer) integrate(coverage []float64, a, b, f0, slope float64) {
	if b <= a {
		return
	}
	first := int(math.Floor(a))
	last := int(math.Floor(b - 1e-12))
	if first < 0 {
		first = 0
	}
	if last >= r.Width {
		last = r.Width - 1
	}
	for i := first; i <= last; i++ {
		lo := float64(i)
		hi := lo + 1
		if lo < a {
			lo = a
		}
		if hi > b {
			hi = b
		}
		if hi <= lo {
			continue
		}
		fa := f0 + slope*(lo-a)
		fb := f0 + slope*(hi-a)
		coverage[i] += 0.5 * (fa + fb) * (hi - lo)
	}
}

func coverageByte(signedArea float64, rule FillRule) byte {
	// signedArea is the pixel-area fraction weighted by winding, so its
	// magnitude for a single covering contour is at most 1.
	var alpha float64
	switch rule {
	case FillNonZero:
		alpha = math.Abs(signedArea)
	default:
		// Even-odd: fold the winding into alternating 0/1 bands. Folding the
		// accumulated signed area applies the parity rule to the winding
		// contributed within the pixel.
		alpha = parityFold(signedArea)
	}
	if alpha <= 0 {
		return 0
	}
	if alpha >= 1 {
		return 255
	}
	return byte(math.Round(alpha * 255))
}

func parityFold(v float64) float64 {
	if v < 0 {
		v = -v
	}
	band := math.Mod(v, 2)
	if band > 1 {
		return 2 - band
	}
	return band
}
