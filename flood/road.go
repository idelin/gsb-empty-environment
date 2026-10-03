package flood

// Road is a polyline in grid coordinates (cell units).
type Road struct {
	ID     string
	Points []Point
}

// Breakpoint describes one maximal flooded segment of a road: the stretch
// between Enter and Exit is blocked by water at the queried tick.
type Breakpoint struct {
	Tick   int
	RoadID string
	Enter  Point
	Exit   Point
	Cells  int // number of distinct flooded grid cells under the segment
}

// BlockagesAt samples every road polyline against the water-depth state
// after tick t and returns the list of flooded (blocked) segments.
func (s *Simulator) BlockagesAt(t int, roads []Road) []Breakpoint {
	return FindBlockages(s.DepthAt(t), s.Cfg.BlockDepth, t, roads)
}

// FindBlockages reports blocked segments of roads on an arbitrary depth
// grid, using threshold as the blocking depth.
func FindBlockages(depth *Grid, threshold float64, tick int, roads []Road) []Breakpoint {
	var out []Breakpoint
	for _, r := range roads {
		out = append(out, roadBlockages(depth, threshold, tick, r)...)
	}
	return out
}

const sampleStep = 0.25 // polyline sampling step, in cell units

func roadBlockages(depth *Grid, threshold float64, tick int, road Road) []Breakpoint {
	var bps []Breakpoint
	var open *Breakpoint
	seen := map[Cell]bool{}
	walkPolyline(road.Points, sampleStep, func(p Point) {
		cx, cy := int(p.X), int(p.Y)
		if p.X < 0 {
			cx--
		}
		if p.Y < 0 {
			cy--
		}
		flooded := depth.InBounds(cx, cy) && depth.At(cx, cy) >= threshold
		if flooded {
			if open == nil {
				open = &Breakpoint{Tick: tick, RoadID: road.ID, Enter: p}
				seen = map[Cell]bool{}
			}
			open.Exit = p
			c := Cell{cx, cy}
			if !seen[c] {
				seen[c] = true
				open.Cells++
			}
		} else if open != nil {
			bps = append(bps, *open)
			open = nil
		}
	})
	if open != nil {
		bps = append(bps, *open)
	}
	return bps
}

// walkPolyline invokes fn for points sampled along the polyline at
// intervals of at most step (endpoints included).
func walkPolyline(pts []Point, step float64, fn func(Point)) {
	if len(pts) == 0 {
		return
	}
	fn(pts[0])
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		dx, dy := b.X-a.X, b.Y-a.Y
		dist := dx*dx + dy*dy
		if dist == 0 {
			continue
		}
		// dist currently holds squared distance.
		var n int
		for n = 1; ; n++ {
			if float64(n)*step*float64(n)*step >= dist {
				break
			}
		}
		for k := 1; k <= n; k++ {
			f := float64(k) / float64(n)
			fn(Point{X: a.X + dx*f, Y: a.Y + dy*f})
		}
	}
}
