package taskpkg

import (
	"fmt"
	"math/big"
	"sort"
)

// ArrangedEdge is an undirected atomic edge of the planar arrangement. A
// collinear overlap is represented once, with all directed occurrences merged
// into Forward and Reverse multiplicities.
type ArrangedEdge struct {
	From      Point
	To        Point
	FromIndex int
	ToIndex   int
	Forward   int
	Reverse   int
	Direction int
	Sources   []EdgeRef
}

// Multiplicity returns the total number of original directed segments (after
// splitting) covering this atomic edge.
func (e ArrangedEdge) Multiplicity() int {
	return e.Forward + e.Reverse
}

// Arrangement is a planar edge arrangement. Every input segment has been
// split at all endpoint and crossing intersections; collinear duplicates are
// represented by multiplicities rather than separate edges.
type Arrangement struct {
	Vertices []Point
	Edges    []ArrangedEdge
}

type sourceSegment struct {
	a, b       int
	operand    int
	ring, edge int
	label      string
}

type pieceOcc struct {
	start, end int
	origin     sourceSegment
}

type atomicEdge struct {
	a, b    int
	forward int
	reverse int
	occ     []pieceOcc
}

type vertexSet struct {
	byKey map[string]int
	pts   []Point
}

func newVertexSet() *vertexSet {
	return &vertexSet{byKey: make(map[string]int)}
}

func (v *vertexSet) id(p Point) int {
	key := pointKey(p)
	if id, ok := v.byKey[key]; ok {
		return id
	}
	id := len(v.pts)
	v.byKey[key] = id
	v.pts = append(v.pts, clonePoint(p))
	return id
}

// BuildArrangement builds the exact arrangement of all regions. Invalid
// points cause a panic; use BuildArrangementE when errors must be returned.
func BuildArrangement(regions ...Region) Arrangement {
	a, err := BuildArrangementE(regions...)
	if err != nil {
		panic(err)
	}
	return a
}

// BuildArrangementE is the error-returning form of BuildArrangement.
func BuildArrangementE(regions ...Region) (Arrangement, error) {
	a, _, err := buildArrangement(regions)
	return a, err
}

func buildArrangement(regions []Region) (Arrangement, *vertexSet, error) {
	verts := newVertexSet()
	segments, err := collectSegments(regions, verts)
	if err != nil {
		return Arrangement{}, nil, err
	}
	pieces := splitSegments(segments, verts)

	byKey := make(map[[2]int]*atomicEdge)
	for _, piece := range pieces {
		a, b := piece.start, piece.end
		if a > b {
			a, b = b, a
		}
		key := [2]int{a, b}
		edge := byKey[key]
		if edge == nil {
			edge = &atomicEdge{a: a, b: b}
			byKey[key] = edge
		}
		if piece.start == a && piece.end == b {
			edge.forward++
		} else {
			edge.reverse++
		}
		edge.occ = append(edge.occ, piece)
	}

	keys := make([][2]int, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	out := Arrangement{Vertices: clonePoints(verts.pts)}
	out.Edges = make([]ArrangedEdge, 0, len(keys))
	for _, key := range keys {
		edge := byKey[key]
		arranged := ArrangedEdge{
			From:      clonePoint(verts.pts[edge.a]),
			To:        clonePoint(verts.pts[edge.b]),
			FromIndex: edge.a,
			ToIndex:   edge.b,
			Forward:   edge.forward,
			Reverse:   edge.reverse,
			Direction: edge.forward - edge.reverse,
		}
		arranged.Sources = make([]EdgeRef, 0, len(edge.occ))
		for _, occ := range edge.occ {
			dir := -1
			if occ.start == edge.a && occ.end == edge.b {
				dir = 1
			}
			arranged.Sources = append(arranged.Sources, EdgeRef{
				Operand:   occ.origin.operand,
				Ring:      occ.origin.ring,
				Edge:      occ.origin.edge,
				Label:     occ.origin.label,
				Direction: dir,
			})
		}
		sortEdgeRefs(arranged.Sources)
		out.Edges = append(out.Edges, arranged)
	}
	return out, verts, nil
}

func collectSegments(regions []Region, verts *vertexSet) ([]sourceSegment, error) {
	var out []sourceSegment
	for operand, region := range regions {
		for ringIndex, ring := range region.Rings {
			for _, p := range ring.Points {
				if !validPoint(p) {
					return nil, fmt.Errorf("taskpkg: operand %d ring %d contains a nil coordinate", operand, ringIndex)
				}
			}
			n := len(ring.Points)
			if n < 2 {
				continue
			}
			for i := 0; i < n; i++ {
				from := ring.Points[i]
				to := ring.Points[(i+1)%n]
				if from.Equal(to) {
					continue
				}
				fromID := verts.id(from)
				toID := verts.id(to)
				out = append(out, sourceSegment{
					a:       fromID,
					b:       toID,
					operand: operand,
					ring:    ringIndex,
					edge:    i,
					label:   ring.Label,
				})
			}
		}
	}
	return out, nil
}

func splitSegments(segments []sourceSegment, verts *vertexSet) []pieceOcc {
	split := make([]map[int]struct{}, len(segments))
	for i, seg := range segments {
		split[i] = map[int]struct{}{seg.a: {}, seg.b: {}}
	}

	for i := 0; i < len(segments); i++ {
		for j := i + 1; j < len(segments); j++ {
			p1 := verts.pts[segments[i].a]
			p2 := verts.pts[segments[i].b]
			p3 := verts.pts[segments[j].a]
			p4 := verts.pts[segments[j].b]
			for _, p := range segmentIntersections(p1, p2, p3, p4) {
				if !pointOnSegment(p1, p2, p) || !pointOnSegment(p3, p4, p) {
					continue
				}
				id := verts.id(p)
				split[i][id] = struct{}{}
				split[j][id] = struct{}{}
			}
		}
	}

	var pieces []pieceOcc
	for i, seg := range segments {
		ids := make([]int, 0, len(split[i]))
		for id := range split[i] {
			ids = append(ids, id)
		}
		a := verts.pts[seg.a]
		b := verts.pts[seg.b]
		sort.Slice(ids, func(x, y int) bool {
			tx := segmentParam(a, b, verts.pts[ids[x]])
			ty := segmentParam(a, b, verts.pts[ids[y]])
			return tx.Cmp(ty) < 0
		})
		for k := 0; k+1 < len(ids); k++ {
			if ids[k] == ids[k+1] {
				continue
			}
			pieces = append(pieces, pieceOcc{
				start:  ids[k],
				end:    ids[k+1],
				origin: seg,
			})
		}
	}
	return pieces
}

func segmentParam(a, b, q Point) *big.Rat {
	dx := ratSub(b.X, a.X)
	dy := ratSub(b.Y, a.Y)
	qx := ratSub(q.X, a.X)
	qy := ratSub(q.Y, a.Y)
	num := ratMul(qx, dx)
	num.Add(num, ratMul(qy, dy))
	den := ratMul(dx, dx)
	den.Add(den, ratMul(dy, dy))
	if den.Sign() == 0 {
		return newRat()
	}
	return ratQuo(num, den)
}

// segmentIntersections returns exact intersection points. For collinear
// segments it returns only the endpoints of the overlap. Point-only overlap
// is included, while disjoint parallel segments produce nothing.
func segmentIntersections(a, b, c, d Point) []Point {
	rx := ratSub(b.X, a.X)
	ry := ratSub(b.Y, a.Y)
	sx := ratSub(d.X, c.X)
	sy := ratSub(d.Y, c.Y)
	den := crossRat(rx, ry, sx, sy)
	qpx := ratSub(c.X, a.X)
	qpy := ratSub(c.Y, a.Y)

	if den.Sign() != 0 {
		t := ratQuo(crossRat(qpx, qpy, sx, sy), den)
		u := ratQuo(crossRat(qpx, qpy, rx, ry), den)
		if t.Sign() >= 0 && t.Cmp(big.NewRat(1, 1)) <= 0 &&
			u.Sign() >= 0 && u.Cmp(big.NewRat(1, 1)) <= 0 {
			return []Point{{
				X: ratAdd(a.X, ratMul(t, rx)),
				Y: ratAdd(a.Y, ratMul(t, ry)),
			}}
		}
		return nil
	}

	if crossRat(qpx, qpy, rx, ry).Sign() != 0 {
		return nil
	}

	var out []Point
	seen := make(map[string]struct{})
	add := func(p Point) {
		key := pointKey(p)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, clonePoint(p))
	}
	if pointOnSegment(c, d, a) {
		add(a)
	}
	if pointOnSegment(c, d, b) {
		add(b)
	}
	if pointOnSegment(a, b, c) {
		add(c)
	}
	if pointOnSegment(a, b, d) {
		add(d)
	}
	return out
}

func clonePoints(points []Point) []Point {
	out := make([]Point, len(points))
	for i, p := range points {
		out[i] = clonePoint(p)
	}
	return out
}

func sortEdgeRefs(refs []EdgeRef) {
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Operand != b.Operand {
			return a.Operand < b.Operand
		}
		if a.Ring != b.Ring {
			return a.Ring < b.Ring
		}
		if a.Edge != b.Edge {
			return a.Edge < b.Edge
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Direction < b.Direction
	})
}
