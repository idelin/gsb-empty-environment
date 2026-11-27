package policysafe

import (
	"fmt"
	"sort"
)

// StratificationError describes a non-stratified program: a recursive
// dependency whose cycle traverses a negated edge (including p -> not p).
type StratificationError struct {
	Predicates []string
	Reason     string
}

func (e StratificationError) Error() string {
	return fmt.Sprintf("policysafe: non-stratified negation among %v: %s", e.Predicates, e.Reason)
}

type edge struct {
	to      string
	negated bool
}

type depGraph struct {
	preds []string
	edges map[string][]edge
}

func buildDepGraph(prog *Program) *depGraph {
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
		}
	}
	for _, fact := range prog.Facts {
		add(fact.Pred)
	}
	edges := map[string][]edge{}
	edgeSeen := map[string]map[string]bool{}
	addEdge := func(from string, e edge) {
		add(from)
		add(e.to)
		key := from + "\x00" + e.to
		if edgeSeen[key] == nil {
			edgeSeen[key] = map[string]bool{}
		}
		marker := "p"
		if e.negated {
			marker = "n"
		}
		if edgeSeen[key][marker] {
			return
		}
		edgeSeen[key][marker] = true
		edges[from] = append(edges[from], e)
	}

	for _, rule := range prog.Rules {
		from := rule.Head.Pred
		for _, lit := range rule.Body {
			addEdge(from, edge{to: lit.Atom.Pred, negated: lit.Neg})
		}
	}

	preds := make([]string, 0, len(seen))
	for name := range seen {
		preds = append(preds, name)
	}
	sort.Strings(preds)
	return &depGraph{preds: preds, edges: edges}
}

// checkStratification rejects programs in which some recursive cycle
// contains a negated edge. Positive recursion (Datalog transitive closure)
// remains legal.
func checkStratification(graph *depGraph) *StratificationError {
	indexOf := map[string]int{}
	for i, name := range graph.preds {
		indexOf[name] = i
	}

	type sccState struct {
		index   int
		lowLink int
		onStack bool
		hasSelf bool
		selfNeg bool
	}

	n := len(graph.preds)
	state := make([]*sccState, n)
	var stack []int
	nextIndex := 0
	var violation *StratificationError

	var visit func(int)
	visit = func(v int) {
		state[v] = &sccState{index: nextIndex, lowLink: nextIndex, onStack: true}
		nextIndex++
		stack = append(stack, v)

		for _, e := range graph.edges[graph.preds[v]] {
			w := indexOf[e.to]
			if state[w] == nil {
				visit(w)
				if state[v].lowLink > state[w].lowLink {
					state[v].lowLink = state[w].lowLink
				}
			} else if state[w].onStack {
				if state[v].lowLink > state[w].index {
					state[v].lowLink = state[w].index
				}
			}
			if w == v && e.negated {
				state[v].hasSelf = true
				state[v].selfNeg = true
			} else if w == v {
				state[v].hasSelf = true
			}
		}

		if state[v].lowLink != state[v].index {
			return
		}

		var component []int
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			state[w].onStack = false
			component = append(component, w)
			if w == v {
				break
			}
		}

		if len(component) < 2 && !state[v].hasSelf {
			return
		}

		negated := state[v].selfNeg
		member := map[int]bool{}
		for _, node := range component {
			member[node] = true
		}
		for _, node := range component {
			for _, e := range graph.edges[graph.preds[node]] {
				if member[indexOf[e.to]] && e.negated {
					negated = true
				}
			}
		}
		if negated && violation == nil {
			names := make([]string, 0, len(component))
			for _, node := range component {
				names = append(names, graph.preds[node])
			}
			sort.Strings(names)
			violation = &StratificationError{
				Predicates: names,
				Reason:     "recursive dependency crosses a negated ('not') edge, so no finite stratification exists",
			}
		}
	}

	for i := 0; i < n; i++ {
		if state[i] == nil {
			visit(i)
		}
	}
	return violation
}

// assignStrata computes a stratum number for every predicate. A predicate
// referenced positively may share the head's stratum; a negated reference
// must live in a strictly lower stratum.
func assignStrata(graph *depGraph) map[string]int {
	stratum := map[string]int{}
	for _, name := range graph.preds {
		stratum[name] = 0
	}

	changed := true
	for changed {
		changed = false
		for _, from := range graph.preds {
			base := stratum[from]
			requirement := base
			for _, e := range graph.edges[from] {
				if e.negated {
					requirement = maxInt(requirement, stratum[e.to]+1)
				} else {
					requirement = maxInt(requirement, stratum[e.to])
				}
			}
			if requirement > stratum[from] {
				stratum[from] = requirement
				changed = true
			}
		}
	}
	return stratum
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
