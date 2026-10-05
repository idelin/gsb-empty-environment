package main

import "fmt"

// verifyAgainstRebuild cross-checks every answer by replaying the operation
// stream from scratch up to each query position and rebuilding the graph at
// that exact moment. It confirms both the connectivity verdict and the
// evidence: paths must use edges alive at that time, and cut certificates
// must equal the rebuilt component of u.
func verifyAgainstRebuild(ops []op, answers []answer) error {
	qi := 0
	count := make(map[edge]int)
	for i, o := range ops {
		switch o.kind {
		case opAdd:
			count[makeEdge(o.u, o.v)]++
		case opDel:
			e := makeEdge(o.u, o.v)
			if count[e] == 0 {
				return &OpError{Line: o.line, Msg: fmt.Sprintf("del %d %d: edge is not present", o.u, o.v)}
			}
			count[e]--
		case opQuery:
			if qi >= len(answers) {
				return fmt.Errorf("internal error: more queries than answers")
			}
			a := answers[qi]
			qi++
			adj := buildAdjFromCounts(count)
			path, comp, ok := bfs(adj, o.u, o.v)
			if ok != a.connected {
				return fmt.Errorf("query #%d (tick=%d %d %d): solver says connected=%v, rebuild says %v",
					qi, o.tick, o.u, o.v, a.connected, ok)
			}
			if ok {
				if !validPath(adj, a.path, o.u, o.v) {
					return fmt.Errorf("query #%d (tick=%d %d %d): path %v uses edges not alive at that time",
						qi, o.tick, o.u, o.v, a.path)
				}
				_ = path
			} else if !sameSet(a.component, comp) {
				return fmt.Errorf("query #%d (tick=%d %d %d): cut %v != rebuilt component %v",
					qi, o.tick, o.u, o.v, a.component, comp)
			}
		}
		_ = i
	}
	if qi != len(answers) {
		return fmt.Errorf("internal error: %d answers for %d queries", len(answers), qi)
	}
	return nil
}

func buildAdjFromCounts(count map[edge]int) map[int][]int {
	active := make(map[edge]struct{})
	for e, c := range count {
		if c > 0 {
			active[e] = struct{}{}
		}
	}
	return buildAdj(active)
}

// validPath checks endpoints and that every consecutive pair is an edge of
// the rebuilt graph.
func validPath(adj map[int][]int, path []int, u, v int) bool {
	if len(path) == 0 || path[0] != u || path[len(path)-1] != v {
		return false
	}
	for i := 0; i+1 < len(path); i++ {
		if !hasNeighbor(adj, path[i], path[i+1]) {
			return false
		}
	}
	return true
}

func hasNeighbor(adj map[int][]int, x, y int) bool {
	for _, n := range adj[x] {
		if n == y {
			return true
		}
	}
	return false
}

func sameSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]int, len(a))
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
		if seen[x] < 0 {
			return false
		}
	}
	return true
}
