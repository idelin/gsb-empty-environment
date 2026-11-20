package projection

import "math/big"

type probabilityAtom struct {
	label Label
	state int
	mass  *big.Rat
}

type flowEdge struct {
	to   int
	rev  int
	cap  *big.Rat
	orig *big.Rat
}

type flowGraph struct {
	adj [][]flowEdge
}

func newFlowGraph(nodes int) *flowGraph {
	return &flowGraph{adj: make([][]flowEdge, nodes)}
}

func (g *flowGraph) addEdge(from, to int, capacity *big.Rat) {
	forward := flowEdge{
		to:   to,
		rev:  len(g.adj[to]),
		cap:  new(big.Rat).Set(capacity),
		orig: new(big.Rat).Set(capacity),
	}
	backward := flowEdge{
		to:   from,
		rev:  len(g.adj[from]),
		cap:  new(big.Rat),
		orig: new(big.Rat),
	}
	g.adj[from] = append(g.adj[from], forward)
	g.adj[to] = append(g.adj[to], backward)
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

func (g *flowGraph) maxFlow(source, sink int) *big.Rat {
	total := new(big.Rat)
	for {
		level := make([]int, len(g.adj))
		for i := range level {
			level[i] = -1
		}
		level[source] = 0
		queue := []int{source}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, edge := range g.adj[v] {
				if edge.cap.Sign() > 0 && level[edge.to] < 0 {
					level[edge.to] = level[v] + 1
					queue = append(queue, edge.to)
				}
			}
		}
		if level[sink] < 0 {
			return total
		}
		work := make([]int, len(g.adj))
		for {
			limit := new(big.Rat)
			for _, edge := range g.adj[source] {
				limit = addRat(limit, edge.cap)
			}
			if limit.Sign() == 0 {
				break
			}
			pushed := g.dfs(source, sink, limit, level, work)
			if pushed.Sign() == 0 {
				break
			}
			total = addRat(total, pushed)
		}
	}
}

func (g *flowGraph) dfs(v, sink int, limit *big.Rat, level, work []int) *big.Rat {
	if v == sink {
		return new(big.Rat).Set(limit)
	}
	for work[v] < len(g.adj[v]) {
		edge := &g.adj[v][work[v]]
		if edge.cap.Sign() > 0 && level[edge.to] == level[v]+1 {
			pushed := g.dfs(edge.to, sink, minRat(limit, edge.cap), level, work)
			if pushed.Sign() > 0 {
				edge.cap.Sub(edge.cap, pushed)
				reverse := &g.adj[edge.to][edge.rev]
				reverse.cap.Add(reverse.cap, pushed)
				return pushed
			}
		}
		work[v]++
	}
	return new(big.Rat)
}

// coupleAtoms returns an exact transport plan with the supplied marginals.
// When sameLabel is true, mass may move only between atoms carrying the same
// projected label.  Every positive cell must also respect relation.
func coupleAtoms(left, right []probabilityAtom, relation [][]bool, states []StateID, sameLabel bool) ([]CouplingCell, bool) {
	var leftTotal, rightTotal big.Rat
	for _, atom := range left {
		if atom.mass.Sign() > 0 {
			leftTotal.Add(&leftTotal, atom.mass)
		}
	}
	for _, atom := range right {
		if atom.mass.Sign() > 0 {
			rightTotal.Add(&rightTotal, atom.mass)
		}
	}
	if leftTotal.Cmp(&rightTotal) != 0 {
		return nil, false
	}
	if leftTotal.Sign() == 0 {
		return nil, true
	}

	leftAtoms := make([]probabilityAtom, 0, len(left))
	for _, atom := range left {
		if atom.mass.Sign() > 0 {
			leftAtoms = append(leftAtoms, atom)
		}
	}
	rightAtoms := make([]probabilityAtom, 0, len(right))
	for _, atom := range right {
		if atom.mass.Sign() > 0 {
			rightAtoms = append(rightAtoms, atom)
		}
	}

	source := 0
	leftBase := 1
	rightBase := leftBase + len(leftAtoms)
	sink := rightBase + len(rightAtoms)
	graph := newFlowGraph(sink + 1)

	for i, atom := range leftAtoms {
		graph.addEdge(source, leftBase+i, atom.mass)
	}
	for j, atom := range rightAtoms {
		graph.addEdge(rightBase+j, sink, atom.mass)
	}

	infinite := new(big.Rat).Set(&leftTotal)
	leftEdgeIndex := make([][]int, len(leftAtoms))
	for i, a := range leftAtoms {
		for j, b := range rightAtoms {
			if sameLabel && a.label != b.label {
				continue
			}
			if a.state < 0 || b.state < 0 || a.state >= len(relation) || b.state >= len(relation[a.state]) {
				continue
			}
			if !relation[a.state][b.state] {
				continue
			}
			leftEdgeIndex[i] = append(leftEdgeIndex[i], j)
			graph.addEdge(leftBase+i, rightBase+j, infinite)
		}
	}
	if graph.maxFlow(source, sink).Cmp(&leftTotal) != 0 {
		return nil, false
	}

	cells := make([]CouplingCell, 0)
	for i, a := range leftAtoms {
		used := new(big.Rat)
		for _, edge := range graph.adj[leftBase+i] {
			if edge.to < rightBase || edge.to >= sink {
				continue
			}
			amount := new(big.Rat).Sub(edge.orig, edge.cap)
			if amount.Sign() > 0 {
				j := edge.to - rightBase
				b := rightAtoms[j]
				cells = append(cells, CouplingCell{
					From:        states[a.state],
					To:          states[b.state],
					FromLabel:   a.label,
					ToLabel:     b.label,
					Probability: RatString(amount),
				})
				used.Add(used, amount)
			}
		}
		if used.Cmp(a.mass) != 0 {
			// A mismatch here means the flow or extraction above is broken.
			return nil, false
		}
	}
	return cells, true
}
