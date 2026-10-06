package contour

import "fmt"

// Directions in image coordinates (x right, y down).
const (
	dirE = iota
	dirS
	dirW
	dirN
)

var dirDelta = [4]Point{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}

func turnLeft(d int) int  { return (d + 3) % 4 }
func turnRight(d int) int { return (d + 1) % 4 }
func turnBack(d int) int  { return (d + 2) % 4 }

// Edge is a directed unit segment of the integer lattice.
type Edge struct {
	From, To Point
}

// LoopKind distinguishes outer boundaries from hole boundaries.
type LoopKind int

const (
	// Outer is a boundary with the exterior background on its right.
	Outer LoopKind = iota
	// Hole is a boundary with an enclosed background component on its right.
	Hole
)

func (k LoopKind) String() string {
	if k == Hole {
		return "hole"
	}
	return "outer"
}

// Loop is a closed, replayable boundary ring of one connected region.
// Points are lattice vertices in traversal order; the ring closes implicitly
// from the last point back to the first. Foreground lies on the left of the
// traversal direction.
type Loop struct {
	Region int      // ID of the source region owning this boundary
	Kind   LoopKind // Outer or Hole
	Points []Point  // vertices in order, not repeating the start
}

// Len returns the number of unit edges (grid perimeter contribution).
func (l Loop) Len() int { return len(l.Points) }

// Edges replays the loop into its directed unit edges, in order.
func (l Loop) Edges() []Edge {
	n := len(l.Points)
	out := make([]Edge, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Edge{l.Points[i], l.Points[(i+1)%n]})
	}
	return out
}

// Moves returns the loop as a compact move string of 'E', 'S', 'W', 'N'
// steps, including the final step closing back to the start.
func (l Loop) Moves() string {
	n := len(l.Points)
	buf := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		a, b := l.Points[i], l.Points[(i+1)%n]
		dx, dy := b.X-a.X, b.Y-a.Y
		switch {
		case dx == 1:
			buf = append(buf, 'E')
		case dx == -1:
			buf = append(buf, 'W')
		case dy == 1:
			buf = append(buf, 'S')
		case dy == -1:
			buf = append(buf, 'N')
		default:
			buf = append(buf, '?')
		}
	}
	return string(buf)
}

// SignedArea2 returns twice the signed shoelace area. Outer loops are
// negative and hole loops positive in image coordinates (y down).
func (l Loop) SignedArea2() int {
	n := len(l.Points)
	s := 0
	for i := 0; i < n; i++ {
		a, b := l.Points[i], l.Points[(i+1)%n]
		s += a.X*b.Y - b.X*a.Y
	}
	return s
}

// Replay walks a move string ('E','S','W','N') from start and returns the
// visited vertices (excluding the repeated final start). It is the inverse
// of Loop.Moves for closed rings.
func Replay(start Point, moves string) ([]Point, error) {
	pts := []Point{start}
	cur := start
	for i := 0; i < len(moves); i++ {
		var d Point
		switch moves[i] {
		case 'E':
			d = dirDelta[dirE]
		case 'S':
			d = dirDelta[dirS]
		case 'W':
			d = dirDelta[dirW]
		case 'N':
			d = dirDelta[dirN]
		default:
			return nil, fmt.Errorf("contour: bad move %q at %d", moves[i], i)
		}
		cur = Point{cur.X + d.X, cur.Y + d.Y}
		if i < len(moves)-1 {
			pts = append(pts, cur)
		} else if cur != start {
			return nil, fmt.Errorf("contour: moves do not close back to start")
		}
	}
	return pts, nil
}

// bedge is a directed boundary unit edge with foreground on its left.
type bedge struct {
	from, to Point
	dir      int
}

// boundaryEdges generates every directed unit edge separating foreground
// (left side) from background or the image exterior (right side).
func boundaryEdges(im *Image) ([]bedge, map[Point][]int) {
	var edges []bedge
	byStart := make(map[Point][]int)
	add := func(from Point, dir int) {
		d := dirDelta[dir]
		idx := len(edges)
		edges = append(edges, bedge{from: from, to: Point{from.X + d.X, from.Y + d.Y}, dir: dir})
		byStart[from] = append(byStart[from], idx)
	}
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			if !im.At(x, y) {
				continue
			}
			if !im.At(x, y-1) { // top side, walk west
				add(Point{x + 1, y}, dirW)
			}
			if !im.At(x+1, y) { // right side, walk north
				add(Point{x + 1, y + 1}, dirN)
			}
			if !im.At(x, y+1) { // bottom side, walk east
				add(Point{x, y + 1}, dirE)
			}
			if !im.At(x-1, y) { // left side, walk south
				add(Point{x, y}, dirS)
			}
		}
	}
	return edges, byStart
}

// leftCell returns the foreground cell on the left of the edge starting at
// p with direction d.
func leftCell(p Point, d int) Point {
	switch d {
	case dirE:
		return Point{p.X, p.Y - 1}
	case dirS:
		return Point{p.X, p.Y}
	case dirW:
		return Point{p.X - 1, p.Y}
	default: // dirN
		return Point{p.X - 1, p.Y - 1}
	}
}

// rightCell returns the background cell on the right of the edge starting
// at p with direction d.
func rightCell(p Point, d int) Point {
	switch d {
	case dirE:
		return Point{p.X, p.Y}
	case dirS:
		return Point{p.X - 1, p.Y}
	case dirW:
		return Point{p.X - 1, p.Y - 1}
	default: // dirN
		return Point{p.X, p.Y - 1}
	}
}

// successor returns the index of the edge that follows edge ei at its end
// vertex. Every vertex has out-degree 1, except diagonal self-contact
// vertices (two foreground cells touching at a corner) which have
// out-degree 2. There the pairing depends on region connectivity:
//
//   - different regions: pair around the foreground quadrant (left turn),
//     so corner-touching regions keep their own separate loops;
//   - same region: pair around the background quadrant (right turn), so
//     the outer boundary and any hole boundary stay separate loops.
//
// The pairing is a fixed permutation of the boundary edges, independent of
// traversal order.
func successor(im *Image, labels []int, edges []bedge, byStart map[Point][]int, ei int) int {
	e := edges[ei]
	cands := byStart[e.to]
	if len(cands) == 1 {
		return cands[0]
	}
	// Diagonal vertex: the two foreground quadrant cells decide.
	c1 := leftCell(e.from, e.dir)
	v := e.to
	c2 := Point{2*v.X - c1.X - 1, 2*v.Y - c1.Y - 1}
	prefer := turnLeft(e.dir)
	if im.At(c1.X, c1.Y) && im.At(c2.X, c2.Y) &&
		labels[c1.Y*im.W+c1.X] == labels[c2.Y*im.W+c2.X] {
		prefer = turnRight(e.dir)
	}
	for _, c := range cands {
		if edges[c].dir == prefer {
			return c
		}
	}
	return cands[0] // unreachable for a consistent boundary set
}

// traceLoops decomposes the boundary edges into closed loops by following
// the successor permutation.
func traceLoops(im *Image, labels []int) []Loop {
	edges, byStart := boundaryEdges(im)
	used := make([]bool, len(edges))
	var loops []Loop
	for e0 := range edges {
		if used[e0] {
			continue
		}
		var pts []Point
		for ei := e0; !used[ei]; ei = successor(im, labels, edges, byStart, ei) {
			used[ei] = true
			pts = append(pts, edges[ei].from)
		}
		loops = append(loops, newLoop(im, labels, pts, edges[e0]))
	}
	return loops
}

func newLoop(im *Image, labels []int, pts []Point, first bedge) Loop {
	l := Loop{Points: pts}
	fc := leftCell(pts[0], first.dir)
	if im.At(fc.X, fc.Y) {
		l.Region = labels[fc.Y*im.W+fc.X]
	}
	if l.SignedArea2() > 0 {
		l.Kind = Hole
	}
	return l
}
