package main

import (
	"fmt"
	"math"
	"sort"
)

const eps = 1e-9

// nodeKind classifies the clock events of the model.
type nodeKind int

const (
	kindOrigin nodeKind = iota
	kindReady
	kindStart
	kindEnd
	kindMakespan
)

// edgeKind classifies precedence constraints so elapsed time on a critical
// chain can be attributed to compute vs. queueing.
type edgeKind int

const (
	edgeLeadIn edgeKind = iota // origin -> ready
	edgeDep                    // dep.end -> ready
	edgeWait                   // ready -> start
	edgeRun                    // start -> end
	edgeSink                   // end -> makespan
)

type node struct {
	label string
	task  string
	kind  nodeKind
	lo    float64
	hi    float64
}

type edge struct {
	u, v  int
	kind  edgeKind
	task  string
	label string
}

type graph struct {
	nodes  []node
	edges  []edge
	origin int
	tNode  int
}

// Conflict pinpoints a set of mutually contradictory time constraints.
type Conflict struct {
	Kind    string   `json:"kind"` // "input" | "bound" | "cycle"
	Summary string   `json:"summary"`
	Chain   []string `json:"chain"`
}

// ChainStep is one hop of a critical chain explanation.
type ChainStep struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Kind  string  `json:"kind"` // "compute" | "queue" | "makespan"
	Task  string  `json:"task,omitempty"`
	Delta float64 `json:"delta"`
	Note  string  `json:"note"`
}

// EventTime is one realized timestamp of a witness scenario.
type EventTime struct {
	Event string  `json:"event"`
	Time  float64 `json:"time"`
}

// Scenario is one fully realizable explanation (shortest or longest).
type Scenario struct {
	Makespan float64     `json:"makespan"`
	Compute  float64     `json:"compute"`
	Queue    float64     `json:"queue"`
	Verdict  string      `json:"verdict"`
	Chain    []ChainStep `json:"chain"`
	Timeline []EventTime `json:"timeline"`
}

// Report is the outcome of Analyze.
type Report struct {
	Feasible bool      `json:"feasible"`
	Conflict *Conflict `json:"conflict,omitempty"`
	Min      *Scenario `json:"shortest,omitempty"`
	Max      *Scenario `json:"longest,omitempty"`
}

func buildGraph(in *Input) (*graph, *Conflict) {
	g := &graph{}
	if len(in.Tasks) == 0 {
		return nil, &Conflict{Kind: "input", Summary: "input contains no tasks"}
	}
	bad := func(what string, iv Interval) *Conflict {
		return &Conflict{
			Kind:    "input",
			Summary: fmt.Sprintf("%s: empty interval [%.6g, %.6g] (lo > hi)", what, iv.Lo, iv.Hi),
		}
	}
	if in.Origin.Lo > in.Origin.Hi+eps {
		return nil, bad("origin", in.Origin)
	}
	add := func(label, task string, kind nodeKind, lo, hi float64) int {
		g.nodes = append(g.nodes, node{label: label, task: task, kind: kind, lo: lo, hi: hi})
		return len(g.nodes) - 1
	}
	g.origin = add("origin", "", kindOrigin, in.Origin.Lo, in.Origin.Hi)

	type taskNodes struct{ ready, start, end int }
	tn := map[string]taskNodes{}
	for _, t := range in.Tasks {
		if _, dup := tn[t.ID]; dup {
			return nil, &Conflict{Kind: "input", Summary: fmt.Sprintf("duplicate task id %q", t.ID)}
		}
		if t.Ready.Lo > t.Ready.Hi+eps {
			return nil, bad(t.ID+".ready", t.Ready)
		}
		if t.Start.Lo > t.Start.Hi+eps {
			return nil, bad(t.ID+".start", t.Start)
		}
		if t.End.Lo > t.End.Hi+eps {
			return nil, bad(t.ID+".end", t.End)
		}
		r := add(t.ID+".ready", t.ID, kindReady, t.Ready.Lo, t.Ready.Hi)
		s := add(t.ID+".start", t.ID, kindStart, t.Start.Lo, t.Start.Hi)
		e := add(t.ID+".end", t.ID, kindEnd, t.End.Lo, t.End.Hi)
		tn[t.ID] = taskNodes{r, s, e}
		g.edges = append(g.edges,
			edge{g.origin, r, edgeLeadIn, t.ID, t.ID + " queued after project origin"},
			edge{r, s, edgeWait, t.ID, t.ID + ": ready -> start (resource queue wait)"},
			edge{s, e, edgeRun, t.ID, t.ID + ": start -> end (compute)"},
		)
	}
	for _, t := range in.Tasks {
		for _, d := range t.Deps {
			dn, ok := tn[d]
			if !ok {
				return nil, &Conflict{
					Kind:    "input",
					Summary: fmt.Sprintf("task %q depends on unknown task %q", t.ID, d),
				}
			}
			g.edges = append(g.edges, edge{dn.end, tn[t.ID].ready, edgeDep, t.ID,
				fmt.Sprintf("%s: dependency %s completed -> ready", t.ID, d)})
		}
	}
	g.tNode = add("makespan", "", kindMakespan, math.Inf(-1), math.Inf(1))
	for _, t := range in.Tasks {
		g.edges = append(g.edges, edge{tn[t.ID].end, g.tNode, edgeSink, t.ID,
			"makespan >= " + t.ID + ".end"})
	}
	return g, nil
}

// lowerClosure computes the componentwise-minimal feasible assignment
// (longest paths from the observation lower bounds). loOverride pins
// individual nodes to a higher lower bound. A non-nil Conflict is returned
// when the constraints are jointly unsatisfiable, with the responsible
// constraint chain located.
func (g *graph) lowerClosure(loOverride map[int]float64) ([]float64, []int, *Conflict) {
	n := len(g.nodes)
	dist := make([]float64, n)
	for i, nd := range g.nodes {
		dist[i] = nd.lo
	}
	for idx, v := range loOverride {
		dist[idx] = v
	}
	pred := make([]int, n)
	for i := range pred {
		pred[i] = -1
	}
	last := -1
	for iter := 0; iter < n; iter++ {
		last = -1
		for ei, e := range g.edges {
			if dist[e.u] > dist[e.v]+eps {
				dist[e.v] = dist[e.u]
				last = e.v
				pred[e.v] = ei
			}
		}
		if last == -1 {
			break
		}
		if iter == n-1 {
			return dist, pred, g.cycleConflict(pred, last)
		}
	}
	for i, nd := range g.nodes {
		if dist[i] > nd.hi+eps {
			return dist, pred, g.boundConflict(i, dist, pred)
		}
	}
	return dist, pred, nil
}

// upperClosure computes the tightest feasible upper bound of every event by
// propagating observed upper bounds backwards along precedence edges.
func (g *graph) upperClosure() []float64 {
	n := len(g.nodes)
	ub := make([]float64, n)
	for i, nd := range g.nodes {
		ub[i] = nd.hi
	}
	for iter := 0; iter < n; iter++ {
		changed := false
		for _, e := range g.edges {
			if ub[e.v] < ub[e.u]-eps {
				ub[e.u] = ub[e.v]
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return ub
}

func (g *graph) boundConflict(v int, dist []float64, pred []int) *Conflict {
	var steps []string
	cur := v
	for pred[cur] != -1 {
		e := g.edges[pred[cur]]
		steps = append(steps, fmt.Sprintf("%s -> %s  (%s)",
			g.nodes[e.u].label, g.nodes[e.v].label, e.label))
		cur = e.u
	}
	chain := []string{fmt.Sprintf("%s >= %.6g (observed lower bound)", g.nodes[cur].label, dist[cur])}
	for i := len(steps) - 1; i >= 0; i-- {
		chain = append(chain, steps[i])
	}
	chain = append(chain, fmt.Sprintf("%s <= %.6g (observed upper bound)  <-- contradiction",
		g.nodes[v].label, g.nodes[v].hi))
	return &Conflict{
		Kind: "bound",
		Summary: fmt.Sprintf("constraint chain forces %s >= %.6g, but its observed upper bound is %.6g",
			g.nodes[v].label, dist[v], g.nodes[v].hi),
		Chain: chain,
	}
}

func (g *graph) cycleConflict(pred []int, start int) *Conflict {
	n := len(g.nodes)
	x := start
	for i := 0; i < n; i++ {
		x = g.edges[pred[x]].u
	}
	var labels []string
	cur := x
	for {
		e := g.edges[pred[cur]]
		labels = append(labels, fmt.Sprintf("%s -> %s  (%s)",
			g.nodes[e.u].label, g.nodes[e.v].label, e.label))
		cur = e.u
		if cur == x {
			break
		}
	}
	chain := make([]string, 0, len(labels))
	for i := len(labels) - 1; i >= 0; i-- {
		chain = append(chain, labels[i])
	}
	return &Conflict{
		Kind:    "cycle",
		Summary: "cyclic precedence constraints are jointly unsatisfiable",
		Chain:   chain,
	}
}

// traceChain reconstructs one critical chain for the given assignment by
// walking the structural edges backwards from the makespan node, always
// following the latest predecessor. Elapsed time along any such path
// telescopes to the makespan, and each hop is attributed to compute or
// queueing by its edge kind.
func (g *graph) traceChain(t []float64) []ChainStep {
	runEdge := map[int]int{}
	waitEdge := map[int]int{}
	intoReady := map[int][]int{}
	var sinkEdges []int
	for ei, e := range g.edges {
		switch e.kind {
		case edgeRun:
			runEdge[e.v] = ei
		case edgeWait:
			waitEdge[e.v] = ei
		case edgeDep, edgeLeadIn:
			intoReady[e.v] = append(intoReady[e.v], ei)
		case edgeSink:
			sinkEdges = append(sinkEdges, ei)
		}
	}
	latest := func(cands []int) int {
		best := cands[0]
		for _, ci := range cands[1:] {
			if t[g.edges[ci].u] > t[g.edges[best].u]+eps {
				best = ci
			}
		}
		return best
	}
	var rev []ChainStep
	cur := g.tNode
	for cur != g.origin {
		var ei int
		switch g.nodes[cur].kind {
		case kindMakespan:
			ei = latest(sinkEdges)
		case kindEnd:
			ei = runEdge[cur]
		case kindStart:
			ei = waitEdge[cur]
		default: // kindReady
			ei = latest(intoReady[cur])
		}
		e := g.edges[ei]
		kind := "queue"
		switch e.kind {
		case edgeRun:
			kind = "compute"
		case edgeSink:
			kind = "makespan"
		}
		note := e.label
		nd := g.nodes[e.v]
		switch {
		case math.Abs(t[e.v]-nd.lo) <= 1e-6 && math.Abs(nd.lo-nd.hi) > 1e-6:
			note += "; " + nd.label + " sits at its observed lower bound"
		case math.Abs(t[e.v]-nd.hi) <= 1e-6 && math.Abs(nd.lo-nd.hi) > 1e-6:
			note += "; " + nd.label + " sits at its observed upper bound"
		}
		rev = append(rev, ChainStep{
			From:  g.nodes[e.u].label,
			To:    g.nodes[e.v].label,
			Kind:  kind,
			Task:  e.task,
			Delta: t[e.v] - t[e.u],
			Note:  note,
		})
		cur = e.u
	}
	chain := make([]ChainStep, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		chain = append(chain, rev[i])
	}
	return chain
}

func (g *graph) scenario(t []float64) *Scenario {
	chain := g.traceChain(t)
	var compute, queue float64
	for _, s := range chain {
		switch s.Kind {
		case "compute":
			compute += s.Delta
		case "queue":
			queue += s.Delta
		}
	}
	verdict := "compute-bound (task execution dominates the critical path)"
	if queue > compute+eps {
		verdict = "queue-bound (resource waiting / dependency handoff dominates the critical path)"
	}
	timeline := make([]EventTime, 0, len(g.nodes))
	for i, nd := range g.nodes {
		if nd.kind == kindMakespan {
			continue
		}
		timeline = append(timeline, EventTime{Event: nd.label, Time: t[i]})
	}
	sort.Slice(timeline, func(i, j int) bool {
		if math.Abs(timeline[i].Time-timeline[j].Time) > eps {
			return timeline[i].Time < timeline[j].Time
		}
		return timeline[i].Event < timeline[j].Event
	})
	return &Scenario{
		Makespan: t[g.tNode] - t[g.origin],
		Compute:  compute,
		Queue:    queue,
		Verdict:  verdict,
		Chain:    chain,
		Timeline: timeline,
	}
}

// Analyze computes the feasible range of the total critical-path duration
// under interval-uncertain clocks, plus one realizable witness for each end
// of the range. Contradictory constraints are located, never silently
// subtracted into negative durations.
func Analyze(in *Input) *Report {
	g, conf := buildGraph(in)
	if conf != nil {
		return &Report{Feasible: false, Conflict: conf}
	}
	if _, _, conf = g.lowerClosure(nil); conf != nil {
		return &Report{Feasible: false, Conflict: conf}
	}
	ub := g.upperClosure()

	// Shortest explanation: push the origin to its latest feasible value
	// (durations shrink when the reference clock starts late), then take the
	// componentwise-minimal assignment.
	shortest, _, conf := g.lowerClosure(map[int]float64{g.origin: ub[g.origin]})
	if conf != nil {
		return &Report{Feasible: false, Conflict: conf}
	}

	// Longest explanation: the componentwise-maximal assignment (the upper
	// closure itself) is feasible, because ub[u] <= ub[v] holds along every
	// precedence edge. The makespan is the largest feasible task end; the
	// origin is moved to its earliest observed value to maximize the span.
	maxEnd := math.Inf(-1)
	for _, e := range g.edges {
		if e.kind == edgeSink && ub[e.u] > maxEnd {
			maxEnd = ub[e.u]
		}
	}
	longest := make([]float64, len(ub))
	copy(longest, ub)
	longest[g.origin] = g.nodes[g.origin].lo
	longest[g.tNode] = maxEnd

	return &Report{
		Feasible: true,
		Min:      g.scenario(shortest),
		Max:      g.scenario(longest),
	}
}
