package cfgdom

// Dominators is the result of a dominator analysis. The entry node is
// recorded separately in Entry; Idom maps every other reachable node to
// its immediate dominator. Nodes unreachable from the entry have no
// dominator and do not appear in Idom.
type Dominators struct {
	Entry int
	Idom  map[int]int
}

// ImmediateDominator returns the immediate dominator of n. ok is false
// when n is the entry node or is unreachable from the entry.
func (d *Dominators) ImmediateDominator(n int) (idom int, ok bool) {
	idom, ok = d.Idom[n]
	return idom, ok
}

// Reachable reports whether n is reachable from the entry node.
func (d *Dominators) Reachable(n int) bool {
	if n == d.Entry {
		return true
	}
	_, ok := d.Idom[n]
	return ok
}

// Analyze computes the immediate dominators of the graph's current state
// using the Cooper-Harvey-Kennedy iterative algorithm. It can be called
// again after edges or nodes are added; each call is independent.
func (g *Graph) Analyze() *Dominators {
	rpo := reversePostOrder(g)
	rpoNum := make(map[int]int, len(rpo))
	for i, n := range rpo {
		rpoNum[n] = i
	}

	// Internal working map; the entry maps to itself while iterating.
	idom := make(map[int]int, len(rpo))
	if len(rpo) > 0 {
		idom[g.entry] = g.entry
	}

	intersect := func(a, b int) int {
		for a != b {
			for rpoNum[a] > rpoNum[b] {
				a = idom[a]
			}
			for rpoNum[b] > rpoNum[a] {
				b = idom[b]
			}
		}
		return a
	}

	changed := true
	for changed {
		changed = false
		for _, n := range rpo[1:] {
			newIDom := -1
			for _, p := range g.pred[n] {
				if _, ok := idom[p]; !ok {
					continue // not yet processed or unreachable
				}
				if newIDom == -1 {
					newIDom = p
				} else {
					newIDom = intersect(p, newIDom)
				}
			}
			if newIDom == -1 {
				continue // no processed predecessors yet
			}
			if cur, ok := idom[n]; !ok || cur != newIDom {
				idom[n] = newIDom
				changed = true
			}
		}
	}

	d := &Dominators{Entry: g.entry, Idom: make(map[int]int, len(idom)-1)}
	for n, p := range idom {
		if n != g.entry {
			d.Idom[n] = p
		}
	}
	return d
}

// reversePostOrder returns the nodes reachable from the entry in reverse
// postorder.
func reversePostOrder(g *Graph) []int {
	visited := make(map[int]bool, len(g.nodes))
	var post []int
	type frame struct {
		node  int
		succs []int
		next  int
	}
	var stack []frame
	if _, ok := g.nodes[g.entry]; ok {
		visited[g.entry] = true
		stack = append(stack, frame{node: g.entry, succs: g.succ[g.entry]})
	}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next < len(f.succs) {
			s := f.succs[f.next]
			f.next++
			if !visited[s] {
				visited[s] = true
				stack = append(stack, frame{node: s, succs: g.succ[s]})
			}
		} else {
			post = append(post, f.node)
			stack = stack[:len(stack)-1]
		}
	}
	for i, j := 0, len(post)-1; i < j; i, j = i+1, j-1 {
		post[i], post[j] = post[j], post[i]
	}
	return post
}
