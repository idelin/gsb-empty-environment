package trace

import (
	"fmt"
	"sort"
)

type edge struct{ from, to string }

// Analyze builds the causal DAG from a trace, validates it, and
// computes wall-clock latency, accumulated work, and the critical
// path. Malformed spans and edges are excluded from the graph and
// reported instead of silently skewing the result.
func Analyze(t *Trace) Report {
	r := Report{TraceID: t.ID}

	spans := map[string]Span{}
	order := []string{}
	for _, s := range t.Spans {
		if s.End < s.Start {
			r.NegativeDurations = append(r.NegativeDurations, s.ID)
			continue
		}
		if _, dup := spans[s.ID]; dup {
			r.CausalityViolations = append(r.CausalityViolations,
				fmt.Sprintf("duplicate span id %q ignored", s.ID))
			continue
		}
		spans[s.ID] = s
		order = append(order, s.ID)
	}
	r.SpanCount = len(spans)

	var edges []edge
	for _, s := range spans {
		if s.Parent == "" {
			continue
		}
		p, ok := spans[s.Parent]
		if !ok {
			r.CausalityViolations = append(r.CausalityViolations,
				fmt.Sprintf("span %q has unknown parent %q", s.ID, s.Parent))
			continue
		}
		if s.Start < p.Start || s.End > p.End {
			r.CausalityViolations = append(r.CausalityViolations,
				fmt.Sprintf("child %q [%d,%d] escapes parent %q [%d,%d]",
					s.ID, s.Start, s.End, p.ID, p.Start, p.End))
			continue
		}
		edges = append(edges, edge{from: p.ID, to: s.ID})
	}
	for _, d := range t.Dependencies {
		from, okFrom := spans[d.From]
		to, okTo := spans[d.To]
		if !okFrom || !okTo {
			r.CausalityViolations = append(r.CausalityViolations,
				fmt.Sprintf("dependency %q -> %q references unknown span", d.From, d.To))
			continue
		}
		if to.Start < from.End {
			r.CausalityViolations = append(r.CausalityViolations,
				fmt.Sprintf("dependency %q -> %q violated: successor starts at %d before predecessor ends at %d",
					d.From, d.To, to.Start, from.End))
			continue
		}
		edges = append(edges, edge{from: d.From, to: d.To})
	}

	// Wall clock and accumulated work over unique valid spans.
	first := true
	var minStart, maxEnd int64
	for _, id := range order {
		s := spans[id]
		if first || s.Start < minStart {
			minStart = s.Start
		}
		if first || s.End > maxEnd {
			maxEnd = s.End
		}
		first = false
		r.TotalWork += s.Duration()
	}
	if !first {
		r.WallClock = maxEnd - minStart
	}

	topo, cyclic := topoSort(order, edges)
	r.Cycles = cyclic
	if len(cyclic) > 0 {
		return r // no fabricated critical path on a cyclic graph
	}
	r.CriticalPath = criticalPath(spans, edges, topo)
	return r
}

// topoSort runs Kahn's algorithm and returns a topological order
// plus the sorted set of nodes that remain in cycles.
func topoSort(nodes []string, edges []edge) (order, cyclic []string) {
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, n := range nodes {
		indeg[n] = 0
	}
	seen := map[edge]bool{}
	for _, e := range edges {
		if seen[e] {
			continue
		}
		seen[e] = true
		adj[e.from] = append(adj[e.from], e.to)
		indeg[e.to]++
	}
	queue := []string{}
	for _, n := range nodes {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	for n, d := range indeg {
		if d > 0 {
			cyclic = append(cyclic, n)
		}
	}
	sort.Strings(cyclic)
	return order, cyclic
}

// criticalPath finds the causal chain that explains the wall-clock
// latency. For each span it keeps the chain ending there with the
// earliest chain start (ties broken by more accumulated work); the
// answer is the chain maximizing chain.end - chain.start, where the
// chain end is the latest End among its members (a parent outlives
// its children). Work on the chain is the union of member intervals,
// so nested spans are not double counted and work never exceeds
// wall-clock on a single chain.
func criticalPath(spans map[string]Span, edges []edge, topo []string) *CriticalPath {
	preds := map[string][]string{}
	for _, e := range edges {
		preds[e.to] = append(preds[e.to], e.from)
	}
	type chain struct {
		start  int64
		end    int64 // latest End among members
		dpWork int64 // sum of durations, used only for tie-breaking
		path   []string
	}
	best := map[string]chain{}
	var result *chain
	better := func(c chain, r *chain) bool {
		lat, resLat := c.end-c.start, r.end-r.start
		return lat > resLat || (lat == resLat && c.dpWork > r.dpWork)
	}
	for _, id := range topo {
		s := spans[id]
		c := chain{start: s.Start, end: s.End, dpWork: s.Duration(), path: []string{id}}
		for _, p := range preds[id] {
			pc := best[p]
			cand := chain{
				start:  pc.start,
				end:    max(pc.end, s.End),
				dpWork: pc.dpWork + s.Duration(),
				path:   append(append([]string{}, pc.path...), id),
			}
			if cand.start < c.start || (cand.start == c.start && cand.dpWork > c.dpWork) {
				c = cand
			}
		}
		best[id] = c
		if result == nil || better(c, result) {
			cp := c
			result = &cp
		}
	}
	if result == nil {
		return nil
	}
	return &CriticalPath{
		Spans:     result.path,
		WallClock: result.end - result.start,
		Work:      unionLength(spans, result.path),
	}
}

// unionLength returns the total time covered by the given spans,
// merging overlapping (nested) intervals.
func unionLength(spans map[string]Span, ids []string) int64 {
	intervals := make([][2]int64, 0, len(ids))
	for _, id := range ids {
		intervals = append(intervals, [2]int64{spans[id].Start, spans[id].End})
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
	var total, curStart, curEnd int64
	for i, iv := range intervals {
		if i == 0 || iv[0] > curEnd {
			if i > 0 {
				total += curEnd - curStart
			}
			curStart, curEnd = iv[0], iv[1]
		} else if iv[1] > curEnd {
			curEnd = iv[1]
		}
	}
	return total + curEnd - curStart
}
