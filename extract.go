package contour

// Point is an integer grid vertex. Pixel (x, y) spans [x, x+1] x [y, y+1].
type Point struct {
	X, Y int
}

// Loop is a closed, replayable boundary ring: an ordered list of grid
// vertices connected by unit axis-aligned edges, with Points[0] repeated
// at the end. Edges are oriented with foreground on the left.
type Loop struct {
	// Region is the ID of the source region this loop belongs to.
	Region int
	// Hole is true when the loop bounds a background hole of the region.
	Hole bool
	// Points holds the closed vertex ring: Points[len(Points)-1] == Points[0].
	Points []Point
}

// Len is the loop length in unit edges (its grid perimeter contribution).
func (l *Loop) Len() int { return len(l.Points) - 1 }

// SignedArea2 returns twice the signed polygon area. With y growing
// downward, outer loops are negative and hole loops positive.
func (l *Loop) SignedArea2() int {
	a := 0
	for i := 0; i+1 < len(l.Points); i++ {
		p, q := l.Points[i], l.Points[i+1]
		a += p.X*q.Y - q.X*p.Y
	}
	return a
}

// SelfTouches returns the grid vertices the loop visits more than once
// (excluding the closing repeat of the start vertex). A loop self-touches
// where the boundary pinches through a single vertex, e.g. along a
// diagonal background slit.
func (l *Loop) SelfTouches() []Point {
	seen := make(map[Point]int)
	for _, p := range l.Points[:len(l.Points)-1] {
		seen[p]++
	}
	var out []Point
	for _, p := range l.Points[:len(l.Points)-1] {
		if seen[p] > 1 {
			out = append(out, p)
			seen[p] = 0
		}
	}
	return out
}

// Region describes one 4-connected foreground component.
type Region struct {
	ID int
	// Area is the pixel count.
	Area int
	// Perimeter is the boundary length in unit edges.
	Perimeter int
	// Holes is the number of enclosed background components.
	Holes int
	// Loops indexes Result.Loops: one outer loop plus one loop per hole.
	Loops []int
}

// Result is the output of Extract.
type Result struct {
	W, H    int
	Regions []Region
	Loops   []Loop
}

// direction encoding: 0=E, 1=S, 2=W, 3=N (y grows downward).
var dirDelta = [4]Point{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}

// edge is a directed unit boundary edge.
type edge struct {
	from Point
	dir  int
}

// labelComponents assigns each foreground pixel a 4-connected region ID.
func labelComponents(im *Image) []int {
	labels := make([]int, im.W*im.H)
	for i := range labels {
		labels[i] = -1
	}
	next := 0
	queue := make([]int, 0, 64)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			idx := y*im.W + x
			if !im.Pix[idx] || labels[idx] >= 0 {
				continue
			}
			queue = queue[:0]
			queue = append(queue, idx)
			labels[idx] = next
			for len(queue) > 0 {
				cur := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				cx, cy := cur%im.W, cur/im.W
				for _, d := range dirDelta {
					nx, ny := cx+d.X, cy+d.Y
					if nx < 0 || ny < 0 || nx >= im.W || ny >= im.H {
						continue
					}
					ni := ny*im.W + nx
					if im.Pix[ni] && labels[ni] < 0 {
						labels[ni] = next
						queue = append(queue, ni)
					}
				}
			}
			next++
		}
	}
	return labels
}

// boundaryEdges emits every unit edge between a foreground pixel and
// background/outside, directed so foreground is on the left:
//
//	top:    (x+1, y)   -> (x, y)     west
//	right:  (x+1, y+1) -> (x+1, y)   north
//	bottom: (x, y+1)   -> (x+1, y+1) east
//	left:   (x, y)     -> (x, y+1)   south
func boundaryEdges(im *Image) ([]edge, map[Point]uint8) {
	var edges []edge
	out := make(map[Point]uint8)
	emit := func(x, y, dir int) {
		from := Point{x, y}
		edges = append(edges, edge{from, dir})
		out[from] |= 1 << uint(dir)
	}
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			if !im.At(x, y) {
				continue
			}
			if !im.At(x, y-1) { // top
				emit(x+1, y, 2)
			}
			if !im.At(x+1, y) { // right
				emit(x+1, y+1, 3)
			}
			if !im.At(x, y+1) { // bottom
				emit(x, y+1, 0)
			}
			if !im.At(x-1, y) { // left
				emit(x, y, 1)
			}
		}
	}
	return edges, out
}

// successor picks the next direction at a vertex after arriving along
// dir, preferring the sharpest left turn, then straight, right, back.
// At a diagonal foreground pinch this keeps each region's loop separate;
// at a diagonal background pinch it keeps the slit on a single loop.
func successor(dir int, mask uint8) int {
	for _, step := range []int{3, 0, 1, 2} {
		cand := (dir + step) % 4
		if mask&(1<<uint(cand)) != 0 {
			return cand
		}
	}
	return -1 // unreachable for a valid boundary graph
}

// leftPixel returns the foreground pixel to the left of a directed edge.
func leftPixel(e edge) Point {
	switch e.dir {
	case 0: // east: foreground north
		return Point{e.from.X, e.from.Y - 1}
	case 1: // south: foreground east
		return Point{e.from.X, e.from.Y}
	case 2: // west: foreground south
		return Point{e.from.X - 1, e.from.Y}
	default: // north: foreground west
		return Point{e.from.X - 1, e.from.Y - 1}
	}
}

// traceLoops decomposes the boundary graph into closed loops. The
// successor relation is a static bijection, so each edge lies on
// exactly one loop.
func traceLoops(edges []edge, out map[Point]uint8, labels []int, w int) []Loop {
	visited := make(map[edge]bool, len(edges))
	var loops []Loop
	for _, start := range edges {
		if visited[start] {
			continue
		}
		loop := Loop{Points: []Point{start.from}}
		e := start
		for {
			visited[e] = true
			to := Point{e.from.X + dirDelta[e.dir].X, e.from.Y + dirDelta[e.dir].Y}
			loop.Points = append(loop.Points, to)
			next := edge{to, successor(e.dir, out[to])}
			if next == start {
				break
			}
			e = next
		}
		fp := leftPixel(start)
		loop.Region = labels[fp.Y*w+fp.X]
		loop.Hole = loop.SignedArea2() > 0
		loops = append(loops, loop)
	}
	return loops
}

// Extract computes 4-connected regions, their replayable boundary loops,
// and per-region area, hole count and grid perimeter.
func Extract(im *Image) *Result {
	res := &Result{W: im.W, H: im.H}
	labels := labelComponents(im)

	n := 0
	for _, l := range labels {
		if l+1 > n {
			n = l + 1
		}
	}
	res.Regions = make([]Region, n)
	for i := range res.Regions {
		res.Regions[i].ID = i
	}
	for _, l := range labels {
		if l >= 0 {
			res.Regions[l].Area++
		}
	}

	edges, out := boundaryEdges(im)
	res.Loops = traceLoops(edges, out, labels, im.W)
	for i := range res.Loops {
		lp := &res.Loops[i]
		r := &res.Regions[lp.Region]
		r.Loops = append(r.Loops, i)
		r.Perimeter += lp.Len()
		if lp.Hole {
			r.Holes++
		}
	}
	return res
}
