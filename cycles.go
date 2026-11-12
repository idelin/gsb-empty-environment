package netsec

import (
	"sort"
	"strings"
)

const weightInf = int64(1) << 60

func saturatingAdd(a, b int64) int64 {
	if b > weightInf {
		b = weightInf
	}
	if b < -weightInf {
		b = -weightInf
	}
	if a >= weightInf && b > 0 {
		return weightInf
	}
	if a <= -weightInf && b < 0 {
		return -weightInf
	}
	if b > 0 && a > weightInf-b {
		return weightInf
	}
	if b < 0 && a < -weightInf-b {
		return -weightInf
	}
	return a + b
}

// decreasingProof checks every recursive strongly connected component. A
// component is accepted only when every cycle through it decreases the integer
// parameter, i.e. every cycle has a strictly negative total delta.
func (p *preparedProgram) decreasingProof(allowNegative bool) (bool, string) {
	components := p.stronglyConnectedComponents()
	reachable := p.reachableFromEntry()
	for _, comp := range components {
		if !p.componentHasCycle(comp) {
			continue
		}
		if !reachable[comp[0]] {
			continue
		}
		if p.hasNonNegativeCycle(comp) {
			names := make([]string, len(comp))
			for i, idx := range comp {
				names[i] = p.funcs[idx].Name
			}
			sort.Strings(names)
			return false, "recursive cycle cannot be proven to decrease: " + strings.Join(names, " -> ")
		}
		if allowNegative && p.hasNonTerminatingCycle(comp) {
			names := make([]string, len(comp))
			for i, idx := range comp {
				names[i] = p.funcs[idx].Name
			}
			sort.Strings(names)
			return false, "recursive cycle has no terminating lower-bound guard: " + strings.Join(names, " -> ")
		}
	}
	return true, ""
}

func (p *preparedProgram) reachableFromEntry() []bool {
	reachable := make([]bool, len(p.funcs))
	stack := []int{p.entry}
	reachable[p.entry] = true
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, edgeIdx := range p.adj[v] {
			w := p.edgeRefs[edgeIdx].to
			if !reachable[w] {
				reachable[w] = true
				stack = append(stack, w)
			}
		}
	}
	return reachable
}

func terminatingGuard(g Guard) bool {
	switch g.Op {
	case OpGreater, OpGreaterEqual, OpEqual:
		return true
	default:
		return false
	}
}

// hasNonTerminatingCycle reports whether the component contains a cycle in
// which every guard can remain true forever while the parameter keeps
// decreasing. Such a cycle has no well-founded base case.
func (p *preparedProgram) hasNonTerminatingCycle(comp []int) bool {
	inComp := make(map[int]bool, len(comp))
	for _, v := range comp {
		inComp[v] = true
	}
	color := make(map[int]uint8, len(comp))
	var visit func(int) bool
	visit = func(v int) bool {
		color[v] = 1
		for _, edgeIdx := range p.adj[v] {
			ref := p.edgeRefs[edgeIdx]
			if !inComp[ref.to] || terminatingGuard(p.funcs[v].Calls[ref.index].Guard) {
				continue
			}
			switch color[ref.to] {
			case 1:
				return true
			case 0:
				if visit(ref.to) {
					return true
				}
			}
		}
		color[v] = 2
		return false
	}
	for _, v := range comp {
		if color[v] == 0 && visit(v) {
			return true
		}
	}
	return false
}

func (p *preparedProgram) stronglyConnectedComponents() [][]int {
	n := len(p.funcs)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	stack := make([]int, 0, n)
	nextIndex := 0
	components := make([][]int, 0)

	var visit func(int)
	visit = func(v int) {
		index[v] = nextIndex
		low[v] = nextIndex
		nextIndex++
		stack = append(stack, v)
		onStack[v] = true
		for _, edgeIdx := range p.adj[v] {
			w := p.edgeRefs[edgeIdx].to
			if index[w] == -1 {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] != index[v] {
			return
		}
		comp := make([]int, 0)
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			comp = append(comp, w)
			if w == v {
				break
			}
		}
		sort.Ints(comp)
		components = append(components, comp)
	}

	for v := 0; v < n; v++ {
		if index[v] == -1 {
			visit(v)
		}
	}
	return components
}

func (p *preparedProgram) componentHasCycle(comp []int) bool {
	if len(comp) > 1 {
		return true
	}
	v := comp[0]
	for _, edgeIdx := range p.adj[v] {
		if p.edgeRefs[edgeIdx].to == v {
			return true
		}
	}
	return false
}

// hasNonNegativeCycle looks for a closed walk with total delta >= 0. Any
// simple cycle has at most len(comp) vertices, so checking walks of those
// lengths from each start is complete. A non-negative closed walk decomposes
// into simple cycles, at least one of which is also non-negative.
func (p *preparedProgram) hasNonNegativeCycle(comp []int) bool {
	local := make(map[int]int, len(comp))
	for i, v := range comp {
		local[v] = i
	}
	for _, startGlobal := range comp {
		start := local[startGlobal]
		prev := make([]int64, len(comp))
		for i := range prev {
			prev[i] = -weightInf
		}
		prev[start] = 0
		for step := 1; step <= len(comp); step++ {
			cur := make([]int64, len(comp))
			for i := range cur {
				cur[i] = -weightInf
			}
			for u := 0; u < len(comp); u++ {
				if prev[u] <= -weightInf {
					continue
				}
				globalU := comp[u]
				for _, edgeIdx := range p.adj[globalU] {
					ref := p.edgeRefs[edgeIdx]
					v, ok := local[ref.to]
					if !ok {
						continue
					}
					candidate := saturatingAdd(prev[u], ref.delta)
					if candidate > cur[v] {
						cur[v] = candidate
					}
				}
			}
			if cur[start] >= 0 {
				return true
			}
			prev = cur
		}
	}
	return false
}
