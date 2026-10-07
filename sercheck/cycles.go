package sercheck

import (
	"fmt"
	"sort"
	"strings"
)

// Hop is one step of a cycle: all dependency edges between two
// consecutive transactions, with their operation evidence.
type Hop struct {
	From  TxnID
	To    TxnID
	Edges []Edge
}

// Cycle is an elementary cycle in the dependency graph: a
// non-serializable core with full operation evidence.
type Cycle struct {
	// Txns lists the transactions in cycle order; Txns[0] is the
	// smallest ID and is not repeated at the end.
	Txns []TxnID
	Hops []Hop // Hops[i] goes from Txns[i] to Txns[(i+1)%len]
}

func (c Cycle) String() string {
	var b strings.Builder
	for i, t := range c.Txns {
		if i > 0 {
			b.WriteString("  ")
		}
		var types []string
		for _, e := range c.Hops[i].Edges {
			types = append(types, fmt.Sprintf("%s %s", e.Type, e.Key))
		}
		fmt.Fprintf(&b, "T%d -[%s]->", t, strings.Join(types, ","))
	}
	fmt.Fprintf(&b, " T%d", c.Txns[0])
	return b.String()
}

// Cycles enumerates all elementary cycles in the graph. The history is
// conflict-serializable iff the result is empty.
func (g *Graph) Cycles() []Cycle {
	var cycles []Cycle
	for _, scc := range g.sccs() {
		if len(scc) == 1 {
			if len(g.EdgesBetween(scc[0], scc[0])) > 0 {
				cycles = append(cycles, g.buildCycle([]TxnID{scc[0]}))
			}
			continue
		}
		cycles = append(cycles, g.cyclesInSCC(scc)...)
	}
	sort.SliceStable(cycles, func(i, j int) bool {
		a, b := cycles[i].Txns, cycles[j].Txns
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return cycles
}

// cyclesInSCC enumerates elementary cycles within one strongly
// connected component: for each start node (in sorted order) it DFSes
// through nodes not smaller than the start, so every elementary cycle
// is found exactly once, rooted at its smallest node.
func (g *Graph) cyclesInSCC(scc []TxnID) []Cycle {
	inSCC := make(map[TxnID]int, len(scc)) // txn -> rank in scc
	for i, t := range scc {
		inSCC[t] = i
	}
	var cycles []Cycle
	for startIdx, start := range scc {
		visited := map[TxnID]bool{start: true}
		path := []TxnID{start}
		var dfs func(cur TxnID)
		dfs = func(cur TxnID) {
			for _, e := range g.Adj[cur] {
				rank, ok := inSCC[e.To]
				if !ok || rank < startIdx {
					continue
				}
				if e.To == start {
					cycles = append(cycles, g.buildCycle(append([]TxnID(nil), path...)))
					continue
				}
				if visited[e.To] {
					continue
				}
				visited[e.To] = true
				path = append(path, e.To)
				dfs(e.To)
				path = path[:len(path)-1]
				delete(visited, e.To)
			}
		}
		dfs(start)
	}
	return cycles
}

func (g *Graph) buildCycle(txns []TxnID) Cycle {
	c := Cycle{Txns: txns}
	for i, t := range txns {
		next := txns[(i+1)%len(txns)]
		c.Hops = append(c.Hops, Hop{From: t, To: next, Edges: g.EdgesBetween(t, next)})
	}
	return c
}

// sccs returns the strongly connected components (Tarjan), each as a
// sorted slice of transaction IDs.
func (g *Graph) sccs() [][]TxnID {
	index := make(map[TxnID]int)
	low := make(map[TxnID]int)
	onStack := make(map[TxnID]bool)
	var stack []TxnID
	counter := 0
	var sccs [][]TxnID

	var strongconnect func(v TxnID)
	strongconnect = func(v TxnID) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, e := range g.Adj[v] {
			w := e.To
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if index[w] < low[v] {
					low[v] = index[w]
				}
			}
		}
		if low[v] == index[v] {
			var scc []TxnID
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Slice(scc, func(i, j int) bool { return scc[i] < scc[j] })
			sccs = append(sccs, scc)
		}
	}

	for _, v := range g.Nodes() {
		if _, seen := index[v]; !seen {
			strongconnect(v)
		}
	}
	return sccs
}
