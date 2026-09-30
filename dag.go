package main

import "sort"

// Conflict records a causal edge whose child tick is earlier than its parent
// tick. The original edge endpoints are kept as evidence.
type Conflict struct {
	FromID   int
	ToID     int
	FromTick int64
	ToTick   int64
	Detail   string
}

// MissingRef records an edge pointing at an event that is not in the trace.
type MissingRef struct {
	EventID  int
	ParentID int
}

type dagEdge struct {
	from, to int
	w        int64
}

// buildEdges derives weighted causal edges. Edge weight is the wall-clock
// delta child.Tick - parent.Tick; contradictory (negative) deltas are
// reported as conflicts and clamped to 0 so metrics stay well-defined.
func buildEdges(tr *Trace) (edges []dagEdge, missing []MissingRef, conflicts []Conflict) {
	for _, e := range tr.Events() {
		for _, p := range e.Parents {
			pe, ok := tr.Get(p)
			if !ok {
				missing = append(missing, MissingRef{EventID: e.ID, ParentID: p})
				continue
			}
			w := e.Tick - pe.Tick
			if w < 0 {
				conflicts = append(conflicts, Conflict{
					FromID: p, ToID: e.ID,
					FromTick: pe.Tick, ToTick: e.Tick,
					Detail: "child event predates its causal parent",
				})
				w = 0
			}
			edges = append(edges, dagEdge{from: p, to: e.ID, w: w})
		}
	}
	return edges, missing, conflicts
}

// CheckTrace reports contradictory edges and dangling parent references.
func CheckTrace(tr *Trace) (conflicts []Conflict, missing []MissingRef) {
	_, missing, conflicts = buildEdges(tr)
	return conflicts, missing
}

func adjacency(tr *Trace) (adj map[int][]dagEdge, indeg map[int]int) {
	edges, _, _ := buildEdges(tr)
	adj = make(map[int][]dagEdge)
	indeg = make(map[int]int)
	for _, e := range tr.Events() {
		indeg[e.ID] = 0
	}
	for _, ed := range edges {
		adj[ed.from] = append(adj[ed.from], ed)
		indeg[ed.to]++
	}
	return adj, indeg
}

// CriticalPath returns the end-to-end wall-clock metric: the longest causal
// chain of tick deltas. Parallel branches overlap on the wall clock, so they
// are never summed. It also returns one witness path as event IDs.
func CriticalPath(tr *Trace) (int64, []int) {
	adj, indeg := adjacency(tr)
	dist := make(map[int]int64, len(indeg))
	prev := make(map[int]int, len(indeg))
	for id := range indeg {
		dist[id] = -1
	}

	var queue []int
	for id, d := range indeg {
		if d == 0 {
			dist[id] = 0
			queue = append(queue, id)
		}
	}
	sort.Ints(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, ed := range adj[id] {
			if dist[id]+ed.w > dist[ed.to] {
				dist[ed.to] = dist[id] + ed.w
				prev[ed.to] = id
			}
			indeg[ed.to]--
			if indeg[ed.to] == 0 {
				queue = append(queue, ed.to)
			}
		}
	}

	bestID, bestW := 0, int64(-1)
	bestSink := false
	for _, e := range tr.Events() {
		w := dist[e.ID]
		sink := len(adj[e.ID]) == 0
		if w > bestW || (w == bestW && sink && !bestSink) || (w == bestW && sink == bestSink && e.ID < bestID) {
			bestW, bestID, bestSink = w, e.ID, sink
		}
	}
	if bestID == 0 {
		return 0, nil
	}
	var path []int
	for id := bestID; id != 0; id = prev[id] {
		path = append(path, id)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return bestW, path
}

// TotalWork is the cumulative-work metric: the sum of every causal edge's
// duration. Overlapping (parallel) activity is counted in full here, unlike
// in the critical path, so TotalWork >= CriticalPath always holds.
func TotalWork(tr *Trace) int64 {
	edges, _, _ := buildEdges(tr)
	var sum int64
	for _, e := range edges {
		sum += e.w
	}
	return sum
}

// EnumerateCriticalPath exhaustively enumerates every source-to-sink path and
// returns the maximum weight. It is a reference implementation used to verify
// that the DP critical path never double-adds parallel branches. ok is false
// when the path count exceeds maxPaths (DAG too large to enumerate).
func EnumerateCriticalPath(tr *Trace, maxPaths int64) (best int64, paths int64, ok bool) {
	adj, indeg := adjacency(tr)
	var sources []int
	for id, d := range indeg {
		if d == 0 {
			sources = append(sources, id)
		}
	}
	sort.Ints(sources)
	var dfs func(id int, acc int64)
	dfs = func(id int, acc int64) {
		if paths > maxPaths {
			return
		}
		next := adj[id]
		if len(next) == 0 {
			paths++
			if acc > best {
				best = acc
			}
			return
		}
		for _, ed := range next {
			dfs(ed.to, acc+ed.w)
		}
	}
	for _, s := range sources {
		dfs(s, 0)
	}
	return best, paths, paths <= maxPaths
}
