package diaglab

import "sort"

// Graph is a directed failure-dependency graph. An edge u -> v means a
// failure originating at u can propagate to v.
type Graph struct {
	adj map[string]map[string]bool
}

func NewGraph() *Graph { return &Graph{adj: make(map[string]map[string]bool)} }

func (g *Graph) AddNode(id string) {
	if _, ok := g.adj[id]; !ok {
		g.adj[id] = make(map[string]bool)
	}
}

func (g *Graph) AddEdge(from, to string) {
	g.AddNode(from)
	g.AddNode(to)
	g.adj[from][to] = true
}

func (g *Graph) Has(id string) bool {
	_, ok := g.adj[id]
	return ok
}

func (g *Graph) Nodes() []string {
	out := make([]string, 0, len(g.adj))
	for n := range g.adj {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) Successors(id string) []string {
	out := make([]string, 0, len(g.adj[id]))
	for m := range g.adj[id] {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// SCCs returns the strongly connected components (Tarjan), each sorted.
func (g *Graph) SCCs() [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var comps [][]string
	counter := 0
	var connect func(v string)
	connect = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range g.Successors(v) {
			if _, seen := index[w]; !seen {
				connect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			comps = append(comps, comp)
		}
	}
	for _, n := range g.Nodes() {
		if _, seen := index[n]; !seen {
			connect(n)
		}
	}
	return comps
}

// Condensation is the DAG obtained by contracting every SCC to one node.
type Condensation struct {
	Comps  [][]string
	CompOf map[string]int
	DAG    map[int][]int
}

func (g *Graph) Condense() *Condensation {
	comps := g.SCCs()
	c := &Condensation{Comps: comps, CompOf: make(map[string]int), DAG: make(map[int][]int)}
	for i, comp := range comps {
		for _, n := range comp {
			c.CompOf[n] = i
		}
	}
	for i, comp := range comps {
		seen := map[int]bool{}
		for _, u := range comp {
			for _, v := range g.Successors(u) {
				j := c.CompOf[v]
				if j != i && !seen[j] {
					seen[j] = true
					c.DAG[i] = append(c.DAG[i], j)
				}
			}
		}
		sort.Ints(c.DAG[i])
	}
	return c
}

// ReachableComps returns all condensation components reachable from comp src
// (including src itself).
func (c *Condensation) ReachableComps(src int) map[int]bool {
	reached := map[int]bool{src: true}
	queue := []int{src}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range c.DAG[u] {
			if !reached[v] {
				reached[v] = true
				queue = append(queue, v)
			}
		}
	}
	return reached
}

// Path finds a shortest directed path from -> to in the original graph.
func (g *Graph) Path(from, to string) ([]string, bool) {
	if from == to {
		return []string{from}, true
	}
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range g.Successors(u) {
			if _, ok := prev[v]; ok {
				continue
			}
			prev[v] = u
			if v == to {
				path := []string{to}
				for cur := to; cur != from; {
					cur = prev[cur]
					path = append([]string{cur}, path...)
				}
				return path, true
			}
			queue = append(queue, v)
		}
	}
	return nil, false
}
