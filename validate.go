package contour

import "fmt"

// Validate cross-checks an extraction result against two independent
// models and reports every inconsistency it finds:
//
//   - unit-edge counting: each region's perimeter must equal
//     4*area - 2*sharedEdges, and its pixel area must equal the signed
//     loop areas (outer minus holes);
//   - flood-fill background model: background is flood-filled from the
//     image border with 8-connectivity; enclosed background components
//     must be in one-to-one correspondence with the hole loops, and each
//     region's hole count must match;
//   - replay: rasterising the loops must reproduce the source image.
//
// It also verifies loop well-formedness: closed rings of unit
// axis-aligned edges with foreground on the left of every edge.
func Validate(im *Image, res *Result) error {
	var errs []error
	fail := func(format string, args ...interface{}) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	checkLoopGeometry(im, res, fail)
	checkRegionStats(im, res, fail)
	checkBackgroundModel(im, res, fail)

	if replay := Replay(res); !replay.equal(im) {
		fail("replay mismatch: loops do not rasterise back to the source image")
	}

	if len(errs) > 0 {
		return &ValidationError{Errs: errs}
	}
	return nil
}

// ValidationError aggregates every failed cross-check.
type ValidationError struct {
	Errs []error
}

func (e *ValidationError) Error() string {
	s := fmt.Sprintf("contour: %d validation failure(s)", len(e.Errs))
	for _, err := range e.Errs {
		s += "\n  - " + err.Error()
	}
	return s
}

func checkLoopGeometry(im *Image, res *Result, fail func(string, ...interface{})) {
	seen := make(map[edge]bool)
	for li := range res.Loops {
		lp := &res.Loops[li]
		if len(lp.Points) < 5 {
			fail("loop %d: too short (%d vertices)", li, len(lp.Points))
			continue
		}
		if lp.Points[0] != lp.Points[len(lp.Points)-1] {
			fail("loop %d: not closed", li)
		}
		for i := 0; i+1 < len(lp.Points); i++ {
			p, q := lp.Points[i], lp.Points[i+1]
			dx, dy := q.X-p.X, q.Y-p.Y
			dir := -1
			for d, delta := range dirDelta {
				if delta.X == dx && delta.Y == dy {
					dir = d
				}
			}
			if dir < 0 {
				fail("loop %d: edge %d is not a unit axis step: %v -> %v", li, i, p, q)
				continue
			}
			e := edge{p, dir}
			if seen[e] {
				fail("loop %d: directed edge reused at %v", li, p)
			}
			seen[e] = true
			fp := leftPixel(e)
			if !im.At(fp.X, fp.Y) {
				fail("loop %d: edge %d has no foreground on its left", li, i)
			}
			rp := rightPixel(e)
			if im.At(rp.X, rp.Y) {
				fail("loop %d: edge %d has foreground on its right", li, i)
			}
		}
	}
}

// rightPixel returns the background pixel to the right of a directed edge.
func rightPixel(e edge) Point {
	switch e.dir {
	case 0: // east: background south
		return Point{e.from.X, e.from.Y}
	case 1: // south: background west
		return Point{e.from.X - 1, e.from.Y}
	case 2: // west: background north
		return Point{e.from.X - 1, e.from.Y - 1}
	default: // north: background east
		return Point{e.from.X, e.from.Y - 1}
	}
}

func checkRegionStats(im *Image, res *Result, fail func(string, ...interface{})) {
	// Independent unit-edge counting straight from the image.
	area := make([]int, len(res.Regions))
	shared := make([]int, len(res.Regions))
	labels := labelComponents(im)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			l := labels[y*im.W+x]
			if l < 0 {
				continue
			}
			area[l]++
			if x+1 < im.W && labels[y*im.W+x+1] == l {
				shared[l]++
			}
			if y+1 < im.H && labels[(y+1)*im.W+x] == l {
				shared[l]++
			}
		}
	}
	for i := range res.Regions {
		r := &res.Regions[i]
		if r.Area != area[i] {
			fail("region %d: area %d, want %d", i, r.Area, area[i])
		}
		wantPerim := 4*area[i] - 2*shared[i]
		if r.Perimeter != wantPerim {
			fail("region %d: perimeter %d, unit-edge count says %d", i, r.Perimeter, wantPerim)
		}
		loopEdges, signedArea := 0, 0
		holes := 0
		for _, li := range r.Loops {
			lp := &res.Loops[li]
			if lp.Region != i {
				fail("loop %d: listed under region %d but claims region %d", li, i, lp.Region)
			}
			loopEdges += lp.Len()
			signedArea += lp.SignedArea2()
			if lp.Hole {
				holes++
			}
		}
		if loopEdges != r.Perimeter {
			fail("region %d: perimeter %d, loops carry %d edges", i, r.Perimeter, loopEdges)
		}
		if -signedArea/2 != r.Area {
			fail("region %d: area %d, loop polygon area says %d", i, r.Area, -signedArea/2)
		}
		if holes != r.Holes {
			fail("region %d: holes %d, hole loops %d", i, r.Holes, holes)
		}
	}
}

// checkBackgroundModel flood-fills the background from the image border
// with 8-connectivity and matches enclosed background components against
// the hole loops one-to-one.
func checkBackgroundModel(im *Image, res *Result, fail func(string, ...interface{})) {
	const (
		unvisited = -1
		outside   = -2
	)
	comp := make([]int, im.W*im.H)
	for i := range comp {
		comp[i] = unvisited
	}
	var queue []int
	push := func(x, y int) {
		if x < 0 || y < 0 || x >= im.W || y >= im.H {
			return
		}
		i := y*im.W + x
		if !im.Pix[i] && comp[i] == unvisited {
			comp[i] = outside
			queue = append(queue, i)
		}
	}
	for x := 0; x < im.W; x++ {
		push(x, 0)
		push(x, im.H-1)
	}
	for y := 0; y < im.H; y++ {
		push(0, y)
		push(im.W-1, y)
	}
	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		cx, cy := cur%im.W, cur/im.W
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if dx != 0 || dy != 0 {
					push(cx+dx, cy+dy)
				}
			}
		}
	}

	// Label enclosed background components (8-connected).
	enclosed := 0
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			i := y*im.W + x
			if im.Pix[i] || comp[i] != unvisited {
				continue
			}
			queue = append(queue, i)
			comp[i] = enclosed
			for len(queue) > 0 {
				cur := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				cx, cy := cur%im.W, cur/im.W
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := cx+dx, cy+dy
						if nx < 0 || ny < 0 || nx >= im.W || ny >= im.H || (dx == 0 && dy == 0) {
							continue
						}
						ni := ny*im.W + nx
						if !im.Pix[ni] && comp[ni] == unvisited {
							comp[ni] = enclosed
							queue = append(queue, ni)
						}
					}
				}
			}
			enclosed++
		}
	}

	// Every hole loop must sit on a distinct enclosed component.
	loopComp := make(map[int]int) // enclosed component -> loop index
	holeLoops := 0
	for li := range res.Loops {
		lp := &res.Loops[li]
		if !lp.Hole {
			continue
		}
		holeLoops++
		p0, p1 := lp.Points[0], lp.Points[1]
		dir := -1
		for d, delta := range dirDelta {
			if p1.X-p0.X == delta.X && p1.Y-p0.Y == delta.Y {
				dir = d
			}
		}
		if dir < 0 {
			continue // already reported by geometry check
		}
		bp := rightPixel(edge{p0, dir})
		if bp.X < 0 || bp.Y < 0 || bp.X >= im.W || bp.Y >= im.H {
			fail("hole loop %d: interior pixel out of bounds", li)
			continue
		}
		c := comp[bp.Y*im.W+bp.X]
		if c < 0 {
			fail("hole loop %d: interior is not an enclosed background component", li)
			continue
		}
		if prev, dup := loopComp[c]; dup {
			fail("hole loops %d and %d bound the same background component", prev, li)
		}
		loopComp[c] = li
	}
	if holeLoops != enclosed {
		fail("flood fill found %d enclosed background component(s), extraction found %d hole loop(s)", enclosed, holeLoops)
	}
}
