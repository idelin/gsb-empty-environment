package paretopath

import (
	"container/heap"
	"fmt"
	"sort"
)

type Path struct {
	Time  int64
	Cost  int64
	Edges []Edge
}

func ParetoPaths(g *Graph, src, dst int) ([]Path, error) {
	if err := checkEndpoints(g, src, dst); err != nil {
		return nil, err
	}
	if src == dst {
		return []Path{{}}, nil
	}

	kept := make([][]Label, g.vertices)
	pq := &labelHeap{{At: src, Visited: 1 << uint(src)}}
	heap.Init(pq)

	for pq.Len() > 0 {
		l := heap.Pop(pq).(Label)
		if dominatedByAny(kept[l.At], &l) {
			continue
		}
		kept[l.At] = append(pruneDominated(kept[l.At], &l), l)
		if l.At == dst {
			continue
		}
		for _, e := range g.adj[l.At] {
			bit := uint16(1) << uint(e.To)
			if l.Visited&bit != 0 {
				continue
			}
			edges := make([]Edge, len(l.Edges)+1)
			copy(edges, l.Edges)
			edges[len(l.Edges)] = e
			heap.Push(pq, Label{
				At:      e.To,
				Time:    l.Time + e.Time,
				Cost:    l.Cost + e.Cost,
				Visited: l.Visited | bit,
				Edges:   edges,
			})
		}
	}
	return paretoFront(kept[dst]), nil
}

func dominatedByAny(set []Label, l *Label) bool {
	for i := range set {
		if dominates(&set[i], l) {
			return true
		}
	}
	return false
}

func pruneDominated(set []Label, l *Label) []Label {
	out := set[:0]
	for i := range set {
		if !dominates(l, &set[i]) {
			out = append(out, set[i])
		}
	}
	return out
}

func paretoFront(labels []Label) []Path {
	best := make(map[[2]int64]Label)
	for _, l := range labels {
		key := [2]int64{l.Time, l.Cost}
		cur, ok := best[key]
		if !ok || compareEdgeSeq(l.Edges, cur.Edges) < 0 {
			best[key] = l
		}
	}

	paths := make([]Path, 0, len(best))
	for _, l := range best {
		paths = append(paths, Path{Time: l.Time, Cost: l.Cost, Edges: l.Edges})
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Time != paths[j].Time {
			return paths[i].Time < paths[j].Time
		}
		return paths[i].Cost < paths[j].Cost
	})

	front := paths[:0]
	minCost := int64(-1)
	for _, p := range paths {
		if minCost >= 0 && p.Cost >= minCost {
			continue
		}
		minCost = p.Cost
		front = append(front, p)
	}
	return front
}

func checkEndpoints(g *Graph, src, dst int) error {
	if src < 0 || src >= g.vertices || dst < 0 || dst >= g.vertices {
		return fmt.Errorf("paretopath: endpoints (%d, %d) outside vertex range [0, %d)", src, dst, g.vertices)
	}
	return nil
}
