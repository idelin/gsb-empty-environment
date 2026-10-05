package main

import (
	"math/bits"
	"sort"
)

// ---------- Report types ----------

type pathStep struct {
	Rule  string            `json:"rule"`
	State map[string]string `json:"state"`
	Note  string            `json:"note,omitempty"`
}

type loopWitness struct {
	Input map[string]string `json:"input"`
	Steps []pathStep        `json:"steps"`
}

type divWitness struct {
	Input  map[string]string `json:"input"`
	QueueA string            `json:"queue_a"`
	QueueB string            `json:"queue_b"`
	PathA  []pathStep        `json:"path_a"`
	PathB  []pathStep        `json:"path_b"`
}

type report struct {
	States     int          `json:"states"`
	Rules      int          `json:"rules"`
	Terminates bool         `json:"terminates"`
	Unique     bool         `json:"unique_final_queue"`
	Loop       *loopWitness `json:"loop,omitempty"`
	Divergence *divWitness  `json:"divergence,omitempty"`
}

// ---------- Strongly connected components (iterative Tarjan) ----------

func sccs(succ [][]int) (compOf []int, comps [][]int) {
	n := len(succ)
	compOf = make([]int, n)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range compOf {
		compOf[i] = -1
		index[i] = -1
	}
	var stack []int
	counter := 0
	type frame struct {
		v, ei int
	}
	for s := 0; s < n; s++ {
		if index[s] != -1 {
			continue
		}
		index[s] = counter
		low[s] = counter
		counter++
		stack = append(stack, s)
		onStack[s] = true
		call := []frame{{v: s}}
		for len(call) > 0 {
			f := &call[len(call)-1]
			if f.ei < len(succ[f.v]) {
				w := succ[f.v][f.ei]
				f.ei++
				if index[w] == -1 {
					index[w] = counter
					low[w] = counter
					counter++
					stack = append(stack, w)
					onStack[w] = true
					call = append(call, frame{v: w})
				} else if onStack[w] && index[w] < low[f.v] {
					low[f.v] = index[w]
				}
			} else {
				v := f.v
				call = call[:len(call)-1]
				if len(call) > 0 {
					p := call[len(call)-1].v
					if low[v] < low[p] {
						low[p] = low[v]
					}
				}
				if low[v] == index[v] {
					var comp []int
					for {
						w := stack[len(stack)-1]
						stack = stack[:len(stack)-1]
						onStack[w] = false
						compOf[w] = len(comps)
						comp = append(comp, w)
						if w == v {
							break
						}
					}
					comps = append(comps, comp)
				}
			}
		}
	}
	return compOf, comps
}

// ---------- Shortest cycle ----------

// shortestCycle finds a cycle with the fewest rule applications among all
// cycles reachable from the given start nodes. Since every state is a legal
// input, the shortest cycle is also the minimal non-termination witness.
func shortestCycle(g *graph, starts []int) (cycleStates []int, cycleRules []int) {
	n := len(g.succ)
	dist := make([]int, n)
	parent := make([]int, n)
	parentRule := make([]int, n)
	visited := make([]bool, n)
	sorted := append([]int(nil), starts...)
	sort.Ints(sorted)
	best := -1
	var touched []int
	reset := func() {
		for _, t := range touched {
			visited[t] = false
		}
		touched = touched[:0]
	}
	for _, s := range sorted {
		if best == 1 {
			break // cannot beat a self-loop
		}
		reset()
		queue := []int{s}
		visited[s] = true
		touched = append(touched, s)
		dist[s] = 0
		for head := 0; head < len(queue); head++ {
			x := queue[head]
			if best > 0 && dist[x]+1 >= best {
				continue
			}
			found := false
			for ei, y := range g.succ[x] {
				if y == s {
					l := dist[x] + 1
					if best < 0 || l < best {
						best = l
						// Reconstruct: walk parents from x back to s.
						revStates := []int{x}
						var revRules []int
						cur := x
						for cur != s {
							revRules = append(revRules, parentRule[cur])
							cur = parent[cur]
							revStates = append(revStates, cur)
						}
						// revStates = [x, ..., s]; reverse to [s, ..., x].
						fwdStates := make([]int, len(revStates))
						for i, v := range revStates {
							fwdStates[len(revStates)-1-i] = v
						}
						fwdRules := make([]int, len(revRules))
						for i, r := range revRules {
							fwdRules[len(revRules)-1-i] = r
						}
						fwdRules = append(fwdRules, g.srule[x][ei]) // closing edge x -> s
						cycleStates = fwdStates
						cycleRules = fwdRules
					}
					found = true
					break
				}
				if !visited[y] {
					visited[y] = true
					touched = append(touched, y)
					dist[y] = dist[x] + 1
					parent[y] = x
					parentRule[y] = g.srule[x][ei]
					queue = append(queue, y)
				}
			}
			if found {
				break
			}
		}
	}
	return cycleStates, cycleRules
}

// ---------- Reverse BFS distances ----------

func bfsDistances(g *graph, sources []int) []int32 {
	n := len(g.succ)
	d := make([]int32, n)
	for i := range d {
		d[i] = -1
	}
	queue := make([]int, 0, len(sources))
	for _, s := range sources {
		if d[s] < 0 {
			d[s] = 0
			queue = append(queue, s)
		}
	}
	for head := 0; head < len(queue); head++ {
		x := queue[head]
		for _, p := range g.pred[x] {
			if d[p] < 0 {
				d[p] = d[x] + 1
				queue = append(queue, p)
			}
		}
	}
	return d
}

// ---------- Main analysis ----------

func analyze(m *model, g *graph) *report {
	rep := &report{
		States:     m.nStates,
		Rules:      len(m.rules),
		Terminates: true,
		Unique:     true,
	}

	// 1. Termination: any reachable cycle means some input never terminates.
	_, comps := sccs(g.succ)
	var cyclic []int
	for _, comp := range comps {
		if len(comp) > 1 {
			cyclic = append(cyclic, comp...)
			continue
		}
		v := comp[0]
		for _, y := range g.succ[v] {
			if y == v {
				cyclic = append(cyclic, v)
				break
			}
		}
	}
	if len(cyclic) > 0 {
		rep.Terminates = false
		states, rules := shortestCycle(g, cyclic)
		w := &loopWitness{Input: m.stateMap(states[0])}
		for i := range states {
			next := m.nStates // sentinel
			note := ""
			if i+1 < len(states) {
				next = states[i+1]
			} else {
				next = states[0]
				note = "back to the start state; the loop repeats forever"
			}
			w.Steps = append(w.Steps, pathStep{
				Rule:  m.rules[rules[i]].name,
				State: m.stateMap(next),
				Note:  note,
			})
		}
		rep.Loop = w
	}

	// 2. Reachable terminal queues per state (least fixpoint over the graph).
	n := m.nStates
	mask := make([]uint64, n)
	var worklist []int
	for v := 0; v < n; v++ {
		if g.term[v] {
			mask[v] = 1 << uint(m.queueIdxOf(v))
			worklist = append(worklist, v)
		}
	}
	for len(worklist) > 0 {
		x := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		for _, p := range g.pred[x] {
			nm := mask[p] | mask[x]
			if nm != mask[p] {
				mask[p] = nm
				worklist = append(worklist, p)
			}
		}
	}
	divergent := false
	for v := 0; v < n; v++ {
		if bits.OnesCount64(mask[v]) > 1 {
			divergent = true
			break
		}
	}
	if divergent {
		rep.Unique = false
		rep.Divergence = findDivergence(m, g, mask)
	}
	return rep
}

// findDivergence picks the input state and the pair of executions whose
// combined length is minimal among all states that can reach terminals with
// different final queues.
func findDivergence(m *model, g *graph, mask []uint64) *divWitness {
	n := len(g.succ)
	qn := m.sizes[m.queueField]
	dists := map[int][]int32{}
	dist := func(q int) []int32 {
		d, ok := dists[q]
		if !ok {
			var sources []int
			for v := 0; v < n; v++ {
				if g.term[v] && m.queueIdxOf(v) == q {
					sources = append(sources, v)
				}
			}
			d = bfsDistances(g, sources)
			dists[q] = d
		}
		return d
	}
	bestCost := -1
	bestS, bestQ1, bestQ2 := -1, -1, -1
	for s := 0; s < n; s++ {
		if bits.OnesCount64(mask[s]) < 2 {
			continue
		}
		var qs []int
		for q := 0; q < qn; q++ {
			if mask[s]>>uint(q)&1 == 1 {
				qs = append(qs, q)
			}
		}
		for i := 0; i < len(qs); i++ {
			d1 := dist(qs[i])
			for j := i + 1; j < len(qs); j++ {
				d2 := dist(qs[j])
				cost := int(d1[s]) + int(d2[s])
				if bestCost < 0 || cost < bestCost {
					bestCost = cost
					bestS, bestQ1, bestQ2 = s, qs[i], qs[j]
				}
			}
		}
	}
	w := &divWitness{
		Input:  m.stateMap(bestS),
		QueueA: m.queueDisplay(bestQ1),
		QueueB: m.queueDisplay(bestQ2),
		PathA:  buildPath(m, g, dist(bestQ1), bestS),
		PathB:  buildPath(m, g, dist(bestQ2), bestS),
	}
	return w
}

// buildPath greedily follows edges with decreasing distance to a terminal.
func buildPath(m *model, g *graph, d []int32, s int) []pathStep {
	var steps []pathStep
	cur := s
	for d[cur] > 0 {
		advanced := false
		for ei, y := range g.succ[cur] {
			if d[y] == d[cur]-1 {
				steps = append(steps, pathStep{
					Rule:  m.rules[g.srule[cur][ei]].name,
					State: m.stateMap(y),
				})
				cur = y
				advanced = true
				break
			}
		}
		if !advanced {
			break // unreachable in a correct fixpoint; defensive
		}
	}
	return steps
}
