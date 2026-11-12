package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

type point2 struct {
	x, y float64
}

type capEdge struct {
	from, to int
}

func (f planeFrame) project(v Vec3) point2 {
	return point2{v.Dot(f.u), v.Dot(f.v)}
}

// buildCapLoops extracts the exposed plane boundary of the clipped surface.
// Plane edges used in opposite directions belong to a connected retained
// surface and cancel; plane edges covered by a retained coplanar triangle
// cancel in the same way, which is what prevents duplicate caps.
func (c *cutter) buildCapLoops(frame planeFrame) ([]Loop, error) {
	type dirs struct {
		items []capEdge
	}
	byEdge := make(map[Edge]dirs)
	for _, t := range c.surface {
		for _, e := range t.edges() {
			u, v := e[0], e[1]
			if !c.b.onPlane[u] || !c.b.onPlane[v] {
				continue
			}
			key := edgeOf(u, v)
			d := byEdge[key]
			d.items = append(d.items, capEdge{from: u, to: v})
			byEdge[key] = d
		}
	}

	boundary := make([]capEdge, 0, len(byEdge))
	keys := make([]Edge, 0, len(byEdge))
	for key := range byEdge {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].A == keys[j].A {
			return keys[i].B < keys[j].B
		}
		return keys[i].A < keys[j].A
	})
	for _, key := range keys {
		d := byEdge[key]
		switch len(d.items) {
		case 1:
			// Reverse the retained-surface edge to obtain the direction of a
			// cap triangle. Interior then lies to the left in the cap frame.
			boundary = append(boundary, capEdge{from: d.items[0].to, to: d.items[0].from})
		case 2:
			a, b := d.items[0], d.items[1]
			if a.from != b.to || a.to != b.from {
				return nil, fmt.Errorf("taskpkg: plane edge (%d,%d) is inconsistently used", key.A, key.B)
			}
		default:
			return nil, fmt.Errorf("taskpkg: plane edge (%d,%d) has %d kept uses", key.A, key.B, len(d.items))
		}
	}
	if len(boundary) == 0 {
		return nil, nil
	}

	outgoing := make(map[int][]int)
	for _, e := range boundary {
		if e.from == e.to {
			return nil, errors.New("taskpkg: zero-length cap boundary edge")
		}
		outgoing[e.from] = append(outgoing[e.from], e.to)
	}
	for from := range outgoing {
		ns := outgoing[from]
		sort.Slice(ns, func(i, j int) bool {
			ai := angleOf(frame.project(c.b.verts[from]), frame.project(c.b.verts[ns[i]]))
			aj := angleOf(frame.project(c.b.verts[from]), frame.project(c.b.verts[ns[j]]))
			if ai == aj {
				return ns[i] < ns[j]
			}
			return ai < aj
		})
		outgoing[from] = ns
	}

	used := make(map[capEdge]bool, len(boundary))
	var raw [][]int
	for _, start := range boundary {
		if used[start] {
			continue
		}
		loop := []int{start.from}
		cur := start
		for steps := 0; steps <= len(boundary); steps++ {
			if used[cur] {
				return nil, errors.New("taskpkg: cap boundary walk reused an edge")
			}
			used[cur] = true
			loop = append(loop, cur.to)
			if cur.to == start.from {
				break
			}
			next, ok := nextCapEdge(outgoing, c.b.verts, frame, cur)
			if !ok {
				return nil, fmt.Errorf("taskpkg: open cap boundary at vertex %d", cur.to)
			}
			cur = next
			if steps == len(boundary)-1 {
				return nil, errors.New("taskpkg: cap boundary walk did not close")
			}
		}
		if len(loop) < 4 {
			// A triangle needs at least three distinct vertices; loop still
			// carries the repeated first vertex here.
			return nil, errors.New("taskpkg: degenerate cap boundary loop")
		}
		raw = append(raw, loop[:len(loop)-1])
	}

	return classifyLoops(raw, c.b.verts, frame)
}

func angleOf(a, b point2) float64 {
	return math.Atan2(b.y-a.y, b.x-a.x)
}

// nextCapEdge chooses, among the outgoing edges at v, the edge immediately
// clockwise from v->u. With cap interiors on the left, this is the standard
// planar face traversal rule.
func nextCapEdge(outgoing map[int][]int, verts []Vec3, frame planeFrame, e capEdge) (capEdge, bool) {
	ns := outgoing[e.to]
	if len(ns) == 0 {
		return capEdge{}, false
	}
	if len(ns) == 1 {
		return capEdge{from: e.to, to: ns[0]}, true
	}
	v := frame.project(verts[e.to])
	reverse := angleOf(v, frame.project(verts[e.from]))
	angles := make([]float64, len(ns))
	for i, n := range ns {
		angles[i] = angleOf(v, frame.project(verts[n]))
	}
	idx := sort.Search(len(angles), func(i int) bool { return angles[i] > reverse })
	prev := idx - 1
	if prev < 0 {
		prev = len(angles) - 1
	}
	// If the reverse direction itself is present (a spur), skip it.
	for tries := 0; tries < len(ns); tries++ {
		candidate := ns[prev]
		if candidate != e.from {
			return capEdge{from: e.to, to: candidate}, true
		}
		prev = (prev - 1 + len(ns)) % len(ns)
	}
	return capEdge{}, false
}

func classifyLoops(raw [][]int, verts []Vec3, frame planeFrame) ([]Loop, error) {
	type info struct {
		ids    []int
		area   float64
		abs    float64
		parent int
		depth  int
	}
	infos := make([]info, 0, len(raw))
	for _, ids := range raw {
		area := loopArea(ids, verts, frame)
		a := math.Abs(area)
		if a == 0 {
			continue
		}
		infos = append(infos, info{ids: ids, area: area, abs: a, parent: -1})
	}
	if len(infos) == 0 {
		return nil, nil
	}
	for i := range infos {
		containers := 0
		best := -1
		for j := range infos {
			if i == j {
				continue
			}
			if loopInside(infos[i].ids, infos[j].ids, verts, frame) {
				containers++
				if best < 0 || infos[j].abs < infos[best].abs {
					best = j
				}
			}
		}
		infos[i].parent = best
		infos[i].depth = containers
	}

	loops := make([]Loop, len(infos))
	for i, in := range infos {
		ids := append([]int(nil), in.ids...)
		area := in.area
		if in.depth%2 == 0 {
			if area < 0 {
				ids = reverseInts(ids)
				area = -area
			}
		} else {
			if area > 0 {
				ids = reverseInts(ids)
				area = -area
			}
		}
		loops[i] = Loop{
			Vertices: ids,
			Area:     area,
			Parent:   in.parent,
			Depth:    in.depth,
		}
	}
	return loops, nil
}

func reverseInts(ids []int) []int {
	out := make([]int, len(ids))
	for i := range ids {
		out[i] = ids[len(ids)-1-i]
	}
	return out
}

func loopArea(ids []int, verts []Vec3, frame planeFrame) float64 {
	var area float64
	for i, id := range ids {
		a := frame.project(verts[id])
		b := frame.project(verts[ids[(i+1)%len(ids)]])
		area += a.x*b.y - a.y*b.x
	}
	return area / 2
}

func loopInside(inner, outer []int, verts []Vec3, frame planeFrame) bool {
	for _, id := range inner {
		p := frame.project(verts[id])
		inside, boundary := pointInLoop(p, outer, verts, frame)
		if boundary {
			continue
		}
		return inside
	}
	return false
}

func pointInLoop(p point2, ids []int, verts []Vec3, frame planeFrame) (inside, boundary bool) {
	for i, id := range ids {
		a := frame.project(verts[id])
		b := frame.project(verts[ids[(i+1)%len(ids)]])
		if pointOnSegment(p, a, b, 1e-12) {
			return false, true
		}
		if (a.y > p.y) != (b.y > p.y) {
			x := a.x + (p.y-a.y)*(b.x-a.x)/(b.y-a.y)
			if p.x < x {
				inside = !inside
			}
		}
	}
	return inside, false
}

func pointOnSegment(p, a, b point2, eps float64) bool {
	cross := (b.x-a.x)*(p.y-a.y) - (b.y-a.y)*(p.x-a.x)
	scale := math.Max(1, math.Max(math.Abs(b.x-a.x), math.Abs(b.y-a.y)))
	if math.Abs(cross) > eps*scale*scale {
		return false
	}
	return p.x >= math.Min(a.x, b.x)-eps && p.x <= math.Max(a.x, b.x)+eps &&
		p.y >= math.Min(a.y, b.y)-eps && p.y <= math.Max(a.y, b.y)+eps
}

type capPoint struct {
	id int
	p  point2
}

func triangulateCap(loops []Loop, verts []Vec3, frame planeFrame) ([]Triangle, error) {
	if len(loops) == 0 {
		return nil, nil
	}
	var out []Triangle
	for i := range loops {
		if loops[i].Depth%2 != 0 {
			continue
		}
		outer := makeCapLoop(loops[i].Vertices, verts, frame)
		var holes [][]capPoint
		for j := range loops {
			if loops[j].Parent == i && loops[j].Depth == loops[i].Depth+1 {
				holes = append(holes, makeCapLoop(loops[j].Vertices, verts, frame))
			}
		}
		// Larger holes first keeps later bridges away from already consumed
		// space in most practical cross-sections.
		sort.SliceStable(holes, func(a, b int) bool {
			return math.Abs(capLoopArea(holes[a])) > math.Abs(capLoopArea(holes[b]))
		})
		tris, err := triangulateRegion(outer, holes, frame)
		if err != nil {
			return nil, err
		}
		out = append(out, tris...)
	}
	return out, nil
}

func makeCapLoop(ids []int, verts []Vec3, frame planeFrame) []capPoint {
	out := make([]capPoint, len(ids))
	for i, id := range ids {
		out[i] = capPoint{id: id, p: frame.project(verts[id])}
	}
	return out
}

func capLoopArea(poly []capPoint) float64 {
	var area float64
	for i, a := range poly {
		b := poly[(i+1)%len(poly)]
		area += a.p.x*b.p.y - a.p.y*b.p.x
	}
	return area / 2
}

func triangulateRegion(outer []capPoint, holes [][]capPoint, frame planeFrame) ([]Triangle, error) {
	if len(outer) < 3 {
		return nil, errors.New("taskpkg: cap outer loop has fewer than three vertices")
	}
	combined := append([]capPoint(nil), outer...)
	remaining := append([][]capPoint(nil), holes...)
	for len(remaining) > 0 {
		hole := remaining[0]
		pi, hi, ok := findBridge(combined, hole, outer, holes)
		if !ok {
			return nil, fmt.Errorf("taskpkg: no visible bridge for cap hole with %d vertices", len(hole))
		}
		combined = spliceHole(combined, pi, hole, hi)
		remaining = remaining[1:]
	}
	flat := earClip(combined)
	if len(flat) == 0 {
		return nil, errors.New("taskpkg: cap triangulation produced no triangles")
	}
	pos := make(map[int]point2)
	for _, p := range outer {
		pos[p.id] = p.p
	}
	for _, hole := range holes {
		for _, p := range hole {
			pos[p.id] = p.p
		}
	}
	out := make([]Triangle, 0, len(flat))
	for _, t := range flat {
		if t[0] == t[1] || t[1] == t[2] || t[2] == t[0] {
			continue
		}
		a, b, c := pos[t[0]], pos[t[1]], pos[t[2]]
		// The polygon is CCW in the cap frame, so ears are CCW as well. This
		// check turns isolated numerical reversals into explicit failures
		// rather than silently wrong cap normals.
		cross := (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
		if cross < 0 {
			return nil, errors.New("taskpkg: internal cap triangle orientation is inconsistent")
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, errors.New("taskpkg: cap triangulation produced only degenerate triangles")
	}
	return out, nil
}

func findBridge(poly []capPoint, hole []capPoint, outer []capPoint, allHoles [][]capPoint) (int, int, bool) {
	// Pick the hole vertex with greatest x (then y), a standard choice that
	// is likely to see the outside of the hole.
	hi := 0
	for i := range hole {
		if hole[i].p.x > hole[hi].p.x ||
			(hole[i].p.x == hole[hi].p.x && hole[i].p.y > hole[hi].p.y) {
			hi = i
		}
	}
	best := -1
	bestDist := math.Inf(1)
	for pi := range poly {
		if poly[pi].id == hole[hi].id {
			continue
		}
		if !bridgeVisible(hole[hi], poly[pi], poly, outer, allHoles) {
			continue
		}
		dx := poly[pi].p.x - hole[hi].p.x
		dy := poly[pi].p.y - hole[hi].p.y
		d := dx*dx + dy*dy
		if d < bestDist {
			bestDist = d
			best = pi
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, hi, true
}

func bridgeVisible(h, o capPoint, combined []capPoint, outer []capPoint, holes [][]capPoint) bool {
	if h.p == o.p {
		return false
	}
	mid := point2{(h.p.x + o.p.x) / 2, (h.p.y + o.p.y) / 2}
	if !pointInCapPolygon(mid, outer) {
		return false
	}
	for _, hole := range holes {
		// A bridge endpoint on a hole boundary is allowed; crossing the
		// interior of any hole is not.
		if inside, boundary := pointInCapPolygonBoundary(mid, hole); inside && !boundary {
			return false
		}
	}
	for i := range combined {
		a := combined[i]
		b := combined[(i+1)%len(combined)]
		if segmentBlocked(h, o, a, b) {
			return false
		}
	}
	for _, hole := range holes {
		for i := range hole {
			a := hole[i]
			b := hole[(i+1)%len(hole)]
			if segmentBlocked(h, o, a, b) {
				return false
			}
		}
	}
	return true
}

func pointInCapPolygon(p point2, poly []capPoint) bool {
	inside, boundary := pointInCapPolygonBoundary(p, poly)
	return inside || boundary
}

func pointInCapPolygonBoundary(p point2, poly []capPoint) (inside, boundary bool) {
	for i := range poly {
		a := poly[i].p
		b := poly[(i+1)%len(poly)].p
		if pointOnSegment(p, a, b, 1e-9) {
			return false, true
		}
		if (a.y > p.y) != (b.y > p.y) {
			x := a.x + (p.y-a.y)*(b.x-a.x)/(b.y-a.y)
			if p.x < x {
				inside = !inside
			}
		}
	}
	return inside, false
}

func segmentBlocked(a, b, c, d capPoint) bool {
	// Allow intersections at shared endpoints.
	if a.id == c.id || a.id == d.id || b.id == c.id || b.id == d.id {
		return false
	}
	if pointsEqual(a.p, c.p) || pointsEqual(a.p, d.p) || pointsEqual(b.p, c.p) || pointsEqual(b.p, d.p) {
		return false
	}
	return segmentsIntersect(a.p, b.p, c.p, d.p)
}

func pointsEqual(a, b point2) bool {
	return math.Abs(a.x-b.x) < 1e-12 && math.Abs(a.y-b.y) < 1e-12
}

func segmentsIntersect(a, b, c, d point2) bool {
	o1 := signOrientation(a, b, c)
	o2 := signOrientation(a, b, d)
	o3 := signOrientation(c, d, a)
	o4 := signOrientation(c, d, b)
	if o1 != o2 && o3 != o4 && o1 != 0 && o2 != 0 && o3 != 0 && o4 != 0 {
		return true
	}
	if o1 == 0 && pointOnSegment(c, a, b, 1e-9) {
		return !pointsEqual(c, a) && !pointsEqual(c, b)
	}
	if o2 == 0 && pointOnSegment(d, a, b, 1e-9) {
		return !pointsEqual(d, a) && !pointsEqual(d, b)
	}
	if o3 == 0 && pointOnSegment(a, c, d, 1e-9) {
		return !pointsEqual(a, c) && !pointsEqual(a, d)
	}
	if o4 == 0 && pointOnSegment(b, c, d, 1e-9) {
		return !pointsEqual(b, c) && !pointsEqual(b, d)
	}
	return false
}

func signOrientation(a, b, c point2) int {
	v := (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
	scale := math.Max(1, math.Max(math.Abs(b.x-a.x)+math.Abs(b.y-a.y), math.Abs(c.x-a.x)+math.Abs(c.y-a.y)))
	if math.Abs(v) <= 1e-12*scale*scale {
		return 0
	}
	if v > 0 {
		return 1
	}
	return -1
}

func spliceHole(poly []capPoint, pi int, hole []capPoint, hi int) []capPoint {
	out := make([]capPoint, 0, len(poly)+len(hole)+2)
	out = append(out, poly[:pi+1]...)
	out = append(out, hole[hi:]...)
	out = append(out, hole[:hi+1]...)
	out = append(out, poly[pi:]...)
	return out
}

func earClip(poly []capPoint) []Triangle {
	work := append([]capPoint(nil), poly...)
	var tris []Triangle
	for len(work) > 3 {
		ear := -1
		for i := range work {
			prev := work[(i-1+len(work))%len(work)]
			cur := work[i]
			next := work[(i+1)%len(work)]
			if signOrientation(prev.p, cur.p, next.p) <= 0 {
				continue
			}
			if earContains(prev, cur, next, work) {
				continue
			}
			ear = i
			tris = append(tris, Triangle{prev.id, cur.id, next.id})
			break
		}
		if ear >= 0 {
			work = append(work[:ear], work[ear+1:]...)
			continue
		}
		// A collinear vertex contributes no area. Removing it lets clipping
		// proceed across exact-on-plane vertices without emitting slivers.
		removed := false
		for i := range work {
			prev := work[(i-1+len(work))%len(work)]
			cur := work[i]
			next := work[(i+1)%len(work)]
			if signOrientation(prev.p, cur.p, next.p) == 0 {
				work = append(work[:i], work[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
	if len(work) == 3 && signOrientation(work[0].p, work[1].p, work[2].p) > 0 {
		tris = append(tris, Triangle{work[0].id, work[1].id, work[2].id})
	}
	return tris
}

func earContains(a, b, c capPoint, poly []capPoint) bool {
	for _, p := range poly {
		if p.id == a.id || p.id == b.id || p.id == c.id {
			continue
		}
		if pointsEqual(p.p, a.p) || pointsEqual(p.p, b.p) || pointsEqual(p.p, c.p) {
			continue
		}
		if pointInTriangle(p.p, a.p, b.p, c.p) {
			return true
		}
	}
	return false
}

func pointInTriangle(p, a, b, c point2) bool {
	o1 := signOrientation(a, b, p)
	o2 := signOrientation(b, c, p)
	o3 := signOrientation(c, a, p)
	return o1 >= 0 && o2 >= 0 && o3 >= 0
}
