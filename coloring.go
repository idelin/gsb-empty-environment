package scheduler

// Schedule assigns every live edge to a round and returns the rounds as
// lists of edge IDs. The result satisfies:
//
//   - the number of rounds equals MaxDegree (optimal for bipartite graphs);
//   - each round is a matching: no two edges share a left or right endpoint;
//   - every live edge appears in exactly one round.
//
// Round indices are normalized by first appearance in edge-ID order: the
// round containing the smallest edge ID is 0, the next distinct round met
// while scanning IDs upward is 1, and so on. This makes the output
// deterministic and independent of internal color numbering.
//
// The coloring is recomputed from scratch on every call, so deleting and
// re-adding edges can never leave stale color indices behind.
func (s *Scheduler) Schedule() [][]int {
	delta := s.MaxDegree()
	if delta == 0 {
		return nil
	}
	colorOf := s.colorEdges(delta)

	// Normalize colors to round indices by first appearance in edge-ID order.
	remap := make(map[int]int, delta)
	var rounds [][]int
	for _, id := range s.sortedIDs() {
		r, ok := remap[colorOf[id]]
		if !ok {
			r = len(rounds)
			remap[colorOf[id]] = r
			rounds = append(rounds, nil)
		}
		rounds[r] = append(rounds[r], id)
	}
	return rounds
}

// colorEdges computes a proper edge coloring with colors 0..delta-1 using
// augmenting recoloring. Edges are colored one at a time in ID order. For
// each new edge (u,v) pick a color cu free at u and cv free at v; if they
// differ, flip the alternating cv/cu path starting at u, which frees cv at
// u (the path can never reach v: entering the right side requires a cv
// edge, and v has none). Then assign cv to the new edge.
func (s *Scheduler) colorEdges(delta int) map[int]int {
	colorOf := make(map[int]int, len(s.edges))
	// edgeAt[vertex][color] = incident edge ID with that color, or -1.
	leftAt := make(map[int][]int)
	rightAt := make(map[int][]int)
	slots := func(m map[int][]int, v int) []int {
		sl, ok := m[v]
		if !ok {
			sl = make([]int, delta)
			for i := range sl {
				sl[i] = -1
			}
			m[v] = sl
		}
		return sl
	}
	firstFree := func(sl []int) int {
		for c, eid := range sl {
			if eid == -1 {
				return c
			}
		}
		return -1 // unreachable: degree < delta
	}

	for _, id := range s.sortedIDs() {
		e := s.edges[id]
		cu := firstFree(slots(leftAt, e.U))
		cv := firstFree(slots(rightAt, e.V))
		if cu != cv {
			// Collect the alternating path from u with colors cv, cu, ...
			// without mutating anything, then flip it in a second pass so
			// table updates cannot corrupt the walk.
			var path []int
			cur, onLeft := e.U, true
			c, o := cv, cu
			for {
				var sl []int
				if onLeft {
					sl = slots(leftAt, cur)
				} else {
					sl = slots(rightAt, cur)
				}
				eid := sl[c]
				if eid == -1 {
					break
				}
				path = append(path, eid)
				pe := s.edges[eid]
				if onLeft {
					cur = pe.V
				} else {
					cur = pe.U
				}
				onLeft = !onLeft
				c, o = o, c
			}
			// Flip: path edges alternate cv, cu, cv, ...; each takes the
			// other color. Both endpoint tables stay consistent because
			// the path is simple. Clear every old slot before writing any
			// new one: an interior vertex holds one edge of each color, so
			// interleaving clears and writes would clobber fresh entries.
			for _, eid := range path {
				pe := s.edges[eid]
				slots(leftAt, pe.U)[colorOf[eid]] = -1
				slots(rightAt, pe.V)[colorOf[eid]] = -1
			}
			for i, eid := range path {
				newColor := cu
				if i%2 == 1 {
					newColor = cv
				}
				pe := s.edges[eid]
				slots(leftAt, pe.U)[newColor] = eid
				slots(rightAt, pe.V)[newColor] = eid
				colorOf[eid] = newColor
			}
		}
		slots(leftAt, e.U)[cv] = id
		slots(rightAt, e.V)[cv] = id
		colorOf[id] = cv
	}
	return colorOf
}
