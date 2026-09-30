package main

import "fmt"

type Phase string

const (
	PhaseQueue          Phase = "queue"
	PhaseSerialize      Phase = "serialize"
	PhaseQueueSerialize Phase = "queue+serialize"
	PhasePropagate      Phase = "propagate"
	PhaseRetransmit     Phase = "retransmit"
	PhaseOther          Phase = "other"
)

var PhaseOrder = []Phase{
	PhaseQueue,
	PhaseSerialize,
	PhaseQueueSerialize,
	PhasePropagate,
	PhaseRetransmit,
	PhaseOther,
}

type Event struct {
	ID     int
	Tick   int
	Kind   string
	Packet int
	Copy   int
	Note   string
}

type Edge struct {
	From     int
	To       int
	Phase    Phase
	Lower    int
	Upper    int
	Inferred bool
	RawDelta int
}

type Contradiction struct {
	Edge      Edge
	FromEvent Event
	ToEvent   Event
	Reason    string
}

type DAG struct {
	events         map[int]Event
	edges          []Edge
	parents        map[int][]int
	contradictions []Contradiction
	nextID         int
}

func NewDAG() *DAG {
	return &DAG{events: map[int]Event{}, parents: map[int][]int{}}
}

func (d *DAG) AddEvent(tick int, kind string, packet, copyID int, note string) int {
	id := d.nextID
	d.nextID++
	d.events[id] = Event{ID: id, Tick: tick, Kind: kind, Packet: packet, Copy: copyID, Note: note}
	return id
}

func (d *DAG) Event(id int) Event { return d.events[id] }

func (d *DAG) AddEdge(from, to int, phase Phase) {
	f := d.events[from]
	t := d.events[to]
	delta := t.Tick - f.Tick
	e := Edge{From: from, To: to, Phase: phase, Lower: delta, Upper: delta, RawDelta: delta}
	if delta < 0 {
		e.Lower, e.Upper = 0, 0
		d.contradictions = append(d.contradictions, Contradiction{
			Edge:      e,
			FromEvent: f,
			ToEvent:   t,
			Reason: fmt.Sprintf(
				"child event #%d (%s @tick %d) predates parent event #%d (%s @tick %d)",
				to, t.Kind, t.Tick, from, f.Kind, f.Tick),
		})
	}
	d.appendEdge(e)
}

func (d *DAG) AddInferredEdge(from, to int, phase Phase, lower, upper int) {
	f := d.events[from]
	t := d.events[to]
	d.appendEdge(Edge{
		From: from, To: to, Phase: phase,
		Lower: lower, Upper: upper,
		Inferred: true, RawDelta: t.Tick - f.Tick,
	})
}

func (d *DAG) RemoveEdgesBetween(from, to int) {
	kept := d.edges[:0]
	for _, e := range d.edges {
		if e.From == from && e.To == to {
			continue
		}
		kept = append(kept, e)
	}
	d.edges = kept
	d.parents = map[int][]int{}
	for i, e := range d.edges {
		d.parents[e.To] = append(d.parents[e.To], i)
	}
}

func (d *DAG) appendEdge(e Edge) {
	idx := len(d.edges)
	d.edges = append(d.edges, e)
	d.parents[e.To] = append(d.parents[e.To], idx)
}

func (d *DAG) Contradictions() []Contradiction { return d.contradictions }

func (d *DAG) MaxTickForPacket(packet int) int {
	max := 0
	for _, ev := range d.events {
		if ev.Packet == packet && ev.Tick > max {
			max = ev.Tick
		}
	}
	return max
}

type Analysis struct {
	SinkFound      bool
	Lower          int
	Upper          int
	Path           []int
	Breakdown      map[Phase][2]int
	WorkLower      int
	WorkUpper      int
	Contradictions []Contradiction
}

func (a Analysis) Bottleneck() (Phase, int) {
	best := PhaseOther
	bestV := -1
	for _, p := range PhaseOrder {
		if b, ok := a.Breakdown[p]; ok && b[1] > bestV {
			best, bestV = p, b[1]
		}
	}
	return best, bestV
}

func (a Analysis) BreakdownSum() (int, int) {
	lo, hi := 0, 0
	for _, b := range a.Breakdown {
		lo += b[0]
		hi += b[1]
	}
	return lo, hi
}

func (d *DAG) Analyze(sources []int, sink int) Analysis {
	a := Analysis{Breakdown: map[Phase][2]int{}, Contradictions: d.contradictions}
	if _, ok := d.events[sink]; !ok {
		return a
	}
	a.SinkFound = true

	children := map[int][]int{}
	for i, e := range d.edges {
		children[e.From] = append(children[e.From], i)
	}
	reachable := map[int]bool{}
	queue := append([]int(nil), sources...)
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if reachable[v] {
			continue
		}
		reachable[v] = true
		for _, ei := range children[v] {
			a.WorkLower += d.edges[ei].Lower
			a.WorkUpper += d.edges[ei].Upper
			queue = append(queue, d.edges[ei].To)
		}
	}

	src := map[int]bool{}
	for _, s := range sources {
		src[s] = true
	}
	memo := map[int][2]int{}
	choice := map[int]int{}
	visiting := map[int]bool{}
	var solve func(v int) [2]int
	solve = func(v int) [2]int {
		if src[v] {
			return [2]int{0, 0}
		}
		if m, ok := memo[v]; ok {
			return m
		}
		if visiting[v] {
			return [2]int{0, 0}
		}
		visiting[v] = true
		best := [2]int{0, 0}
		bestIdx := -1
		for _, idx := range d.parents[v] {
			e := d.edges[idx]
			p := solve(e.From)
			cand := [2]int{p[0] + e.Lower, p[1] + e.Upper}
			if bestIdx == -1 || cand[1] > best[1] || (cand[1] == best[1] && cand[0] > best[0]) {
				best = cand
				bestIdx = idx
			}
		}
		visiting[v] = false
		memo[v] = best
		choice[v] = bestIdx
		return best
	}
	res := solve(sink)
	a.Lower, a.Upper = res[0], res[1]

	path := []int{}
	for v := sink; ; {
		path = append([]int{v}, path...)
		if src[v] {
			break
		}
		idx, ok := choice[v]
		if !ok || idx < 0 {
			break
		}
		e := d.edges[idx]
		prev := a.Breakdown[e.Phase]
		a.Breakdown[e.Phase] = [2]int{prev[0] + e.Lower, prev[1] + e.Upper}
		v = e.From
	}
	a.Path = path
	return a
}

func (d *DAG) EnumeratePaths(sources []int, sink, cap int) ([][]int, error) {
	children := map[int][]int{}
	for _, e := range d.edges {
		children[e.From] = append(children[e.From], e.To)
	}
	var paths [][]int
	onPath := map[int]bool{}
	var dfs func(v int, cur []int) error
	dfs = func(v int, cur []int) error {
		if onPath[v] {
			return fmt.Errorf("cycle detected at event #%d", v)
		}
		cur = append(cur, v)
		if v == sink {
			paths = append(paths, append([]int(nil), cur...))
			if len(paths) > cap {
				return fmt.Errorf("path count exceeds cap %d", cap)
			}
			return nil
		}
		onPath[v] = true
		for _, c := range children[v] {
			if err := dfs(c, cur); err != nil {
				return err
			}
		}
		onPath[v] = false
		return nil
	}
	for _, s := range sources {
		if err := dfs(s, nil); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func (d *DAG) PathWeight(path []int, useUpper bool) int {
	sum := 0
	for i := 0; i+1 < len(path); i++ {
		best := 0
		for _, idx := range d.parents[path[i+1]] {
			e := d.edges[idx]
			if e.From != path[i] {
				continue
			}
			w := e.Lower
			if useUpper {
				w = e.Upper
			}
			if w > best {
				best = w
			}
		}
		sum += best
	}
	return sum
}
