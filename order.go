package taskpkg

import (
	"fmt"
)

func (p *preparedInput) geometricConflicts() []Conflict {
	var conflicts []Conflict
	for _, item := range p.items {
		for _, divider := range p.selected {
			if !crossesDivider(item.box, divider) {
				continue
			}
			reason := ReasonCrossesDivider
			if item.heading {
				reason = ReasonHeadingSpansDivider
			}
			conflicts = append(conflicts, Conflict{
				BlockID: item.id,
				Reason:  reason,
				Divider: divider,
				Detail:  fmt.Sprintf("block %q crosses selected divider x=%g", item.id, divider),
			})
		}
	}
	return conflicts
}

type orderGraph struct {
	edges map[[2]int]struct{}
	next  [][]int
}

func newOrderGraph(n int) *orderGraph {
	return &orderGraph{
		edges: make(map[[2]int]struct{}),
		next:  make([][]int, n),
	}
}

func (g *orderGraph) add(from, to int) {
	if from == to {
		return
	}
	edge := [2]int{from, to}
	if _, exists := g.edges[edge]; exists {
		return
	}
	g.edges[edge] = struct{}{}
	g.next[from] = append(g.next[from], to)
}

func (p *preparedInput) structuralOrder() (order []string, cycle bool) {
	n := len(p.items)
	graph := newOrderGraph(n)

	// Explicit structure wins over geometry.
	for ancestor := 0; ancestor < n; ancestor++ {
		for descendant := 0; descendant < n; descendant++ {
			if p.knownAncestor[ancestor][descendant] {
				graph.add(ancestor, descendant)
			}
		}
	}

	// A heading spanning selected column lines is a full-width flow boundary.
	// Blocks strictly above it must precede it, and blocks strictly below it
	// must follow it. Contradictory ancestor edges are never overridden.
	for spanning := range p.items {
		item := p.items[spanning]
		if !item.heading || !p.crossesAnySelectedDivider(item.box) {
			continue
		}
		for other := range p.items {
			if other == spanning {
				continue
			}
			candidate := p.items[other]
			if candidate.box.bottom <= item.box.top {
				p.addUnlessAncestorContradicts(graph, other, spanning)
			}
			if candidate.box.top >= item.box.bottom {
				p.addUnlessAncestorContradicts(graph, spanning, other)
			}
		}
	}

	indegree := make([]int, n)
	for _, targets := range graph.next {
		for _, target := range targets {
			indegree[target]++
		}
	}

	remaining := make([]bool, n)
	for i := range remaining {
		remaining[i] = true
	}
	orderIndexes := make([]int, 0, n)
	for len(orderIndexes) < n {
		best := -1
		for candidate := 0; candidate < n; candidate++ {
			if !remaining[candidate] || indegree[candidate] != 0 {
				continue
			}
			if best < 0 || p.baseLess(candidate, best) {
				best = candidate
			}
		}
		if best < 0 {
			cycle = true
			break
		}
		remaining[best] = false
		orderIndexes = append(orderIndexes, best)
		for _, target := range graph.next[best] {
			indegree[target]--
		}
	}

	if cycle {
		orderIndexes = p.structuralFallback()
	}

	if cycle {
		order = make([]string, 0, n)
		for _, index := range orderIndexes {
			order = append(order, p.items[index].id)
		}
		return order, true
	}

	order = make([]string, 0, n)
	for _, index := range orderIndexes {
		order = append(order, p.items[index].id)
	}
	return order, false
}

func (p *preparedInput) structuralConflicts() []Conflict {
	var conflicts []Conflict
	for ancestor := range p.items {
		for descendant := range p.items {
			if !p.knownAncestor[ancestor][descendant] || p.baseLess(ancestor, descendant) {
				continue
			}
			conflicts = append(conflicts, Conflict{
				BlockID:    p.items[descendant].id,
				Reason:     ReasonAncestorOrder,
				AncestorID: p.items[ancestor].id,
				Detail: fmt.Sprintf("ancestor %q precedes descendant %q despite the vertical base order",
					p.items[ancestor].id, p.items[descendant].id),
			})
		}
	}
	for _, item := range p.items {
		if !item.columnOverride {
			continue
		}
		conflicts = append(conflicts, Conflict{
			BlockID:    item.id,
			Reason:     ReasonAncestorColumn,
			AncestorID: p.items[item.topRoot].id,
			Detail: fmt.Sprintf("block %q follows ancestor %q instead of its geometric column",
				item.id, p.items[item.topRoot].id),
		})
	}
	return conflicts
}

func (p *preparedInput) addUnlessAncestorContradicts(graph *orderGraph, from, to int) {
	// Adding from -> to is invalid if structure already requires to before from.
	if p.knownAncestor[to][from] {
		return
	}
	graph.add(from, to)
}

func (p *preparedInput) crossesAnySelectedDivider(r box) bool {
	for _, divider := range p.selected {
		if crossesDivider(r, divider) {
			return true
		}
	}
	return false
}

func (p *preparedInput) baseLess(a, b int) bool {
	left := p.items[a]
	right := p.items[b]

	leftRank := p.columnRank(left.column)
	rightRank := p.columnRank(right.column)
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if left.box.top != right.box.top {
		return left.box.top < right.box.top
	}
	return left.id < right.id
}

func (p *preparedInput) columnRank(column int) int {
	if p.rtl {
		return p.columnCount - 1 - column
	}
	return column
}

func (p *preparedInput) structuralFallback() []int {
	n := len(p.items)
	indegree := make([]int, n)
	next := make([][]int, n)
	for ancestor := 0; ancestor < n; ancestor++ {
		for descendant := 0; descendant < n; descendant++ {
			if p.knownAncestor[ancestor][descendant] {
				next[ancestor] = append(next[ancestor], descendant)
				indegree[descendant]++
			}
		}
	}

	done := make([]bool, n)
	result := make([]int, 0, n)
	for len(result) < n {
		best := -1
		for i := 0; i < n; i++ {
			if done[i] || indegree[i] != 0 {
				continue
			}
			if best < 0 || p.baseLess(i, best) {
				best = i
			}
		}
		if best < 0 {
			for i := 0; i < n && best < 0; i++ {
				if !done[i] {
					best = i
				}
			}
		}
		done[best] = true
		result = append(result, best)
		for _, target := range next[best] {
			indegree[target]--
		}
	}
	return result
}
