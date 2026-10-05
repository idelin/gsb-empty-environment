package segsweep

import (
	"container/heap"
	"math/big"
	"sort"
)

// eventHeap is a min-heap of event points ordered by (x, y). All events
// sharing a coordinate are therefore processed in a well-defined order:
// increasing x, and within the same x, increasing y.
type eventHeap []Point

func (h eventHeap) Len() int           { return len(h) }
func (h eventHeap) Less(i, j int) bool { return h[i].Cmp(h[j]) < 0 }
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)        { *h = append(*h, x.(Point)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	p := old[n-1]
	*h = old[:n-1]
	return p
}

type pointAcc struct {
	p   Point
	ids map[int]bool
}

type sweeper struct {
	// status holds the active non-vertical segments, sorted by
	// (y at cur.X, slope, id), i.e. by position just right of the
	// current event point.
	status   []*seg
	leftAt   map[string][]*seg // normal segments by left endpoint
	psegAt   map[string][]*seg // zero-length segments by location
	vertsAtX map[string][]*seg // vertical segments by x
	events   eventHeap
	pushed   map[string]bool
	points   map[string]*pointAcc
	cur      Point
}

// Sweep computes all intersection points and collinear overlaps of the
// given integer segments using an exact-arithmetic sweep line.
func Sweep(input []IntSegment) Result {
	segs := normalizeSegments(input)
	sw := &sweeper{
		leftAt:   map[string][]*seg{},
		psegAt:   map[string][]*seg{},
		vertsAtX: map[string][]*seg{},
		pushed:   map[string]bool{},
		points:   map[string]*pointAcc{},
	}
	var nonPoints []*seg
	for _, s := range segs {
		switch s.kind {
		case kindNormal:
			sw.leftAt[pointKey(s.a)] = append(sw.leftAt[pointKey(s.a)], s)
			sw.pushEvent(s.a)
			sw.pushEvent(s.b)
			nonPoints = append(nonPoints, s)
		case kindVertical:
			sw.vertsAtX[s.a.X.String()] = append(sw.vertsAtX[s.a.X.String()], s)
			sw.pushEvent(s.a) // ensures the x coordinate is visited
			nonPoints = append(nonPoints, s)
		case kindPoint:
			sw.psegAt[pointKey(s.a)] = append(sw.psegAt[pointKey(s.a)], s)
			sw.pushEvent(s.a)
		}
	}
	for len(sw.events) > 0 {
		x := sw.events[0].X
		// Process every event group at this x, in y order, before
		// handling vertical-segment crossings at this x.
		for len(sw.events) > 0 && sw.events[0].X.Cmp(x) == 0 {
			p := heap.Pop(&sw.events).(Point)
			sw.processGroup(p)
		}
		for _, v := range sw.vertsAtX[x.String()] {
			for _, s := range sw.status {
				y := s.yAt(x)
				if y.Cmp(v.a.Y) >= 0 && y.Cmp(v.b.Y) <= 0 {
					sw.record(Point{new(big.Rat).Set(x), y}, v.id, s.id)
				}
			}
		}
	}
	overlaps := computeOverlaps(nonPoints, sw.recordIDs)
	return assemble(sw.points, overlaps)
}

func (sw *sweeper) pushEvent(p Point) {
	k := pointKey(p)
	if sw.pushed[k] {
		return
	}
	sw.pushed[k] = true
	heap.Push(&sw.events, p)
}

func (sw *sweeper) record(p Point, ids ...int) {
	k := pointKey(p)
	acc := sw.points[k]
	if acc == nil {
		acc = &pointAcc{p: p, ids: map[int]bool{}}
		sw.points[k] = acc
	}
	for _, id := range ids {
		acc.ids[id] = true
	}
}

func (sw *sweeper) recordIDs(p Point, ids []int) { sw.record(p, ids...) }

// processGroup handles all segments meeting at one event point p:
// records the intersection (unless it is a purely collinear case, which
// the overlap pass owns), then updates the sweep status to the order
// just right of p and checks newly adjacent pairs.
func (sw *sweeper) processGroup(p Point) {
	sw.cur = p
	k := pointKey(p)
	starting := sw.leftAt[k]
	psegs := sw.psegAt[k]
	var verts []*seg
	for _, v := range sw.vertsAtX[p.X.String()] {
		if p.Y.Cmp(v.a.Y) >= 0 && p.Y.Cmp(v.b.Y) <= 0 {
			verts = append(verts, v)
		}
	}
	var remaining, removed []*seg
	for _, s := range sw.status {
		if s.contains(p) {
			removed = append(removed, s)
		} else {
			remaining = append(remaining, s)
		}
	}
	ids := map[int]bool{}
	lines := map[string]bool{}
	add := func(s *seg) {
		ids[s.id] = true
		if s.kind != kindPoint {
			lines[s.line] = true
		}
	}
	for _, s := range starting {
		add(s)
	}
	for _, s := range removed {
		add(s)
	}
	for _, s := range verts {
		add(s)
	}
	for _, s := range psegs {
		add(s)
	}
	if len(ids) >= 2 && (len(lines) >= 2 || len(psegs) > 0) {
		list := make([]int, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Ints(list)
		sw.recordIDs(p, list)
	}
	// Status update: segments through p leave and are re-inserted (if
	// continuing) together with the starters, ordered by slope so the
	// status reflects the order just right of p.
	var inserted []*seg
	for _, s := range removed {
		if s.b.Cmp(p) != 0 {
			inserted = append(inserted, s)
		}
	}
	inserted = append(inserted, starting...)
	sort.Slice(inserted, func(i, j int) bool {
		if c := inserted[i].slope.Cmp(inserted[j].slope); c != 0 {
			return c < 0
		}
		return inserted[i].id < inserted[j].id
	})
	pos := 0
	for _, s := range remaining {
		if s.yAt(p.X).Cmp(p.Y) < 0 {
			pos++
		}
	}
	status := make([]*seg, 0, len(remaining)+len(inserted))
	status = append(status, remaining[:pos]...)
	status = append(status, inserted...)
	status = append(status, remaining[pos:]...)
	sw.status = status
	// Check newly adjacent pairs for future intersection events.
	if len(inserted) > 0 {
		if pos > 0 {
			sw.checkPair(status[pos-1], status[pos])
		}
		if hi := pos + len(inserted) - 1; hi+1 < len(status) {
			sw.checkPair(status[hi], status[hi+1])
		}
	} else if pos > 0 && pos < len(status) {
		sw.checkPair(status[pos-1], status[pos])
	}
}

func (sw *sweeper) checkPair(s1, s2 *seg) {
	if s1.line == s2.line {
		return // collinear pairs are handled by the overlap pass
	}
	ip, ok := lineIntersection(s1, s2)
	if !ok {
		return
	}
	if !s1.contains(ip) || !s2.contains(ip) {
		return
	}
	if ip.Cmp(sw.cur) <= 0 {
		return // already processed
	}
	sw.pushEvent(ip)
}

func assemble(points map[string]*pointAcc, overlaps []Overlap) Result {
	res := Result{
		Points:   []IntersectionPoint{},
		Overlaps: []Overlap{},
	}
	for _, acc := range points {
		ids := make([]int, 0, len(acc.ids))
		for id := range acc.ids {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		res.Points = append(res.Points, IntersectionPoint{P: acc.p, IDs: ids})
	}
	sort.Slice(res.Points, func(i, j int) bool {
		return res.Points[i].P.Cmp(res.Points[j].P) < 0
	})
	res.Overlaps = append(res.Overlaps, overlaps...)
	sort.Slice(res.Overlaps, func(i, j int) bool {
		if c := res.Overlaps[i].A.Cmp(res.Overlaps[j].A); c != 0 {
			return c < 0
		}
		return res.Overlaps[i].B.Cmp(res.Overlaps[j].B) < 0
	})
	return res
}
