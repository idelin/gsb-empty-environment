package msglang

import (
	"container/heap"
)

// trace is a compact proof tree carrying the witness message tree.
type trace struct {
	leaf     bool
	leafRule *LeafRule
	rule     *Rule
	children []*trace
	state    string
	node     *Node
	cost     int
	height   int
}

type candidate struct {
	state    string
	cost     int
	height   int
	tieBreak uint64
	tr       *trace
	index    int
}

type candidateHeap []*candidate

func (h candidateHeap) Len() int { return len(h) }

func (h candidateHeap) Less(i, j int) bool {
	if h[i].cost != h[j].cost {
		return h[i].cost < h[j].cost
	}
	if h[i].height != h[j].height {
		return h[i].height < h[j].height
	}
	return h[i].tieBreak < h[j].tieBreak
}

func (h candidateHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *candidateHeap) Push(x any) {
	item := x.(*candidate)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

func better(candidate, current *candidate) bool {
	if candidate.cost != current.cost {
		return candidate.cost < current.cost
	}
	if candidate.height != current.height {
		return candidate.height < current.height
	}
	return candidate.tieBreak < current.tieBreak
}

func canonicalValue(rule *LeafRule) []byte {
	if rule.Exact {
		return append([]byte(nil), rule.Want...)
	}
	return defaultLeafValue(rule.Kind)
}

func defaultLeafValue(kind LeafKind) []byte {
	switch kind {
	case Principal:
		return []byte("anon")
	case Nonce:
		return []byte{0x00}
	default:
		return []byte{0x01}
	}
}

// minimalTree finds the minimum witness tree reaching every state of a
// validated automaton. The fixpoint terminates because the state set and the
// ranked alphabet are finite.
func minimalTree(a *Automaton) (map[string]*candidate, error) {
	if err := a.ensureCompiled(); err != nil {
		return nil, err
	}
	best := make(map[string]*candidate)
	waiting := make(map[string][]*Rule)
	pending := make(map[*Rule]int)

	for i := range a.Rules {
		rule := &a.Rules[i]
		if len(rule.Child) == 0 {
			pending[rule] = 0
			continue
		}
		pending[rule] = len(rule.Child)
		for _, child := range rule.Child {
			waiting[child] = append(waiting[child], rule)
		}
	}

	var work candidateHeap
	heap.Init(&work)
	var seq uint64
	push := func(c *candidate) {
		c.tieBreak = seq
		seq++
		heap.Push(&work, c)
	}

	// Seed from typed leaf rules (nullary symbols).
	for i := range a.Leaves {
		rule := &a.Leaves[i]
		value := canonicalValue(rule)
		tr := &trace{
			leaf:     true,
			leafRule: rule,
			state:    rule.State,
			node:     NewLeaf(rule.Label, rule.Kind, value),
			cost:     1,
			height:   0,
		}
		push(&candidate{state: rule.State, cost: 1, height: 0, tr: tr})
	}

	relax := func(rule *Rule, parts []*candidate) {
		cost := 1
		height := -1
		children := make([]*trace, len(parts))
		nodes := make([]*Node, len(parts))
		for i, part := range parts {
			cost += part.cost
			if part.height > height {
				height = part.height
			}
			children[i] = part.tr
			nodes[i] = part.tr.node
		}
		height++
		tr := &trace{
			leaf:     false,
			rule:     rule,
			children: children,
			state:    rule.State,
			node:     NewNode(rule.Label, nodes...),
			cost:     cost,
			height:   height,
		}
		push(&candidate{state: rule.State, cost: cost, height: height, tr: tr})
	}

	// Nullary internal rules are ready immediately.
	for rule, count := range pending {
		if count == 0 {
			relax(rule, nil)
		}
	}

	for work.Len() > 0 {
		current := heap.Pop(&work).(*candidate)
		if previous, ok := best[current.state]; ok && !better(current, previous) {
			continue
		}
		best[current.state] = current
		for _, rule := range waiting[current.state] {
			pending[rule]--
			if pending[rule] != 0 {
				continue
			}
			parts := make([]*candidate, len(rule.Child))
			for i, child := range rule.Child {
				parts[i] = best[child]
			}
			relax(rule, parts)
		}
	}
	return best, nil
}

func selectFinal(a *Automaton, best map[string]*candidate) *candidate {
	var winner *candidate
	for _, state := range a.Final {
		candidate, ok := best[state]
		if !ok {
			continue
		}
		if winner == nil || better(candidate, winner) {
			winner = candidate
		}
	}
	return winner
}
