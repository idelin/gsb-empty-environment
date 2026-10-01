package eval

import (
	"fmt"
	"math"
)

// Span is an actual span reconstructed from replayed events. Identity is
// the span ID; parent-child structure comes from Parent IDs, never names.
type Span struct {
	ID     string
	Name   string
	Parent string
	Attrs  map[string]string
	Ended  bool
}

// slot is one expected occurrence of a span rule after repeat expansion.
type slot struct {
	node     *Node
	required bool
	label    string // stable, ID-based: "<nodeID>#<occurrence>"
}

func slotLabel(n *Node, i int) string {
	return fmt.Sprintf("%s#%d", n.ID, i+1)
}

// subResult is the outcome of matching one slot list against one
// candidate span list. It is a value type; slices are copied before
// being shared so enumeration paths never alias each other.
type subResult struct {
	penalty       float64
	counts        Counts
	lines         []string
	missingLabels []string
}

func (r subResult) clone() subResult {
	r.lines = append([]string(nil), r.lines...)
	r.missingLabels = append([]string(nil), r.missingLabels...)
	return r
}

func (r *subResult) absorb(o subResult) {
	r.penalty += o.penalty
	r.counts = r.counts.Add(o.counts)
	r.lines = append(r.lines, o.lines...)
	r.missingLabels = append(r.missingLabels, o.missingLabels...)
}

// matcher performs an exhaustive (reference) enumeration of all mappings
// between expected slots and actual spans, subject to parent-child and
// identity constraints, and keeps the minimum-penalty mapping. The state
// budget guards the exponential search: once exhausted, no score may be
// emitted.
type matcher struct {
	traceAttrs map[string]string
	childrenOf map[string][]*Span
	nameInTree map[string]bool
	budget     int
	states     int
	exceeded   bool
	memo       map[string]subResult
}

func newMatcher(traceAttrs map[string]string, childrenOf map[string][]*Span, root *Node, budget int) *matcher {
	m := &matcher{
		traceAttrs: traceAttrs,
		childrenOf: childrenOf,
		nameInTree: map[string]bool{},
		budget:     budget,
		memo:       map[string]subResult{},
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if !m.active(n) {
			return
		}
		if n.Kind == KindSpan {
			m.nameInTree[n.Name] = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return m
}

// active reports whether a node's When conditions hold for this trace.
func (m *matcher) active(n *Node) bool {
	for k, v := range n.When {
		if m.traceAttrs[k] != v {
			return false
		}
	}
	return true
}

// expandGroup expands a list of rule nodes into alternative slot lists.
// Repeat ranges become Max slots (the first Min required); choice nodes
// fork the alternatives; inactive nodes vanish.
func (m *matcher) expandGroup(children []*Node) [][]slot {
	alts := [][]slot{{}}
	for _, c := range children {
		var calts [][]slot
		switch {
		case !m.active(c):
			calts = [][]slot{{}}
		case c.Kind == KindSpan:
			slots := make([]slot, 0, c.Max)
			for i := 0; i < c.Max; i++ {
				slots = append(slots, slot{node: c, required: i < c.Min, label: slotLabel(c, i)})
			}
			calts = [][]slot{slots}
		case c.Kind == KindAll:
			calts = m.expandGroup(c.Children)
		case c.Kind == KindChoice:
			for _, b := range c.Children {
				if !m.active(b) {
					continue
				}
				calts = append(calts, m.expandGroup([]*Node{b})...)
			}
			if len(calts) == 0 {
				calts = [][]slot{{}}
			}
		}
		alts = crossConcat(alts, calts)
	}
	return alts
}

func crossConcat(a, b [][]slot) [][]slot {
	out := make([][]slot, 0, len(a)*len(b))
	for _, x := range a {
		for _, y := range b {
			merged := make([]slot, 0, len(x)+len(y))
			merged = append(merged, x...)
			merged = append(merged, y...)
			out = append(out, merged)
		}
	}
	return out
}

// matchNodeToSpan returns the minimum-penalty outcome for the subtree of
// span-rule n given that actual span s is mapped to it. Memoized: the
// result depends only on (node, span) identity.
func (m *matcher) matchNodeToSpan(n *Node, s *Span) subResult {
	key := n.ID + "|" + s.ID
	if r, ok := m.memo[key]; ok {
		return r
	}
	res := subResult{}
	if !s.Ended {
		res.penalty += WUnclosed
		res.counts.Unclosed = 1
		res.lines = append(res.lines, fmt.Sprintf("span %s (%s) matched %q but never ended -> unclosed", s.ID, s.Name, n.Name))
	}
	best := subResult{penalty: math.Inf(1)}
	for _, alt := range m.expandGroup(n.Children) {
		r := m.matchSlots(alt, m.childrenOf[s.ID])
		if r.penalty < best.penalty {
			best = r
		}
	}
	if math.IsInf(best.penalty, 1) {
		best = subResult{}
	}
	res.absorb(best)
	m.memo[key] = res
	return res
}

// matchSlots enumerates every assignment between slots and candidate
// spans (each span used at most once, each slot at most one span) and
// returns the minimum-penalty outcome. This is the reference
// implementation: correct by exhaustion, guarded by the state budget.
func (m *matcher) matchSlots(slots []slot, candidates []*Span) subResult {
	required := 0
	for _, s := range slots {
		if s.required {
			required++
		}
	}
	best := subResult{penalty: math.Inf(1)}
	used := make([]bool, len(candidates))

	var recur func(i int, acc subResult)
	recur = func(i int, acc subResult) {
		m.states++
		if m.states > m.budget {
			m.exceeded = true
			return
		}
		if i == len(slots) {
			acc = acc.clone()
			for j, c := range candidates {
				if used[j] {
					continue
				}
				switch {
				case nameInSlots(c.Name, slots):
					acc.penalty += WDuplicate
					acc.counts.Duplicates++
					acc.lines = append(acc.lines, fmt.Sprintf("span %s (%s) exceeds repeat budget -> duplicate", c.ID, c.Name))
				case m.nameInTree[c.Name]:
					acc.penalty += WMisplaced
					acc.counts.Misplaced++
					acc.lines = append(acc.lines, fmt.Sprintf("span %s (%s) not expected under this parent -> misplaced", c.ID, c.Name))
				default:
					acc.penalty += WUnexpected
					acc.counts.Unexpected++
					acc.lines = append(acc.lines, fmt.Sprintf("span %s (%s) unknown to expected graph -> unexpected", c.ID, c.Name))
				}
			}
			if acc.penalty < best.penalty {
				best = acc.clone()
			}
			return
		}
		s := slots[i]
		// Collect compatible unused candidates for this slot.
		var free []int
		for j, c := range candidates {
			if !used[j] && c.Name == s.node.Name {
				free = append(free, j)
			}
		}
		// Option 1: leave the slot unmatched. A required slot may only be
		// declared missing when no compatible actual span exists: presence
		// of a mappable span is a hard identity constraint, so a trace can
		// never buy a cheaper verdict by ignoring spans it emitted.
		if !s.required || len(free) == 0 {
			skip := acc.clone()
			if s.required {
				skip.penalty += WMissing
				skip.counts.Missing++
				skip.missingLabels = append(skip.missingLabels, s.label)
				skip.lines = append(skip.lines, fmt.Sprintf("slot %s (%s) has no span -> missing", s.label, s.node.Name))
			}
			recur(i+1, skip)
		}
		// Option 2: map each compatible unused candidate onto the slot.
		for _, j := range free {
			c := candidates[j]
			used[j] = true
			sub := m.matchNodeToSpan(s.node, c)
			next := acc.clone()
			if s.required {
				next.counts.Matched++
			}
			next.lines = append(next.lines, fmt.Sprintf("slot %s <- span %s (%s)", s.label, c.ID, c.Name))
			next.absorb(sub)
			recur(i+1, next)
			used[j] = false
		}
	}
	recur(0, subResult{counts: Counts{RequiredTotal: required}})
	if math.IsInf(best.penalty, 1) {
		return subResult{counts: Counts{RequiredTotal: required}}
	}
	return best
}

func nameInSlots(name string, slots []slot) bool {
	for _, s := range slots {
		if s.node.Name == name {
			return true
		}
	}
	return false
}

// buildSpans replays events into spans and indexes them by parent ID.
// Orphans (parent ID never seen) are treated as roots; duplicate start
// events for the same ID keep the first occurrence (stable identity).
func buildSpans(events []Event) (childrenOf map[string][]*Span, roots []*Span) {
	spans := map[string]*Span{}
	var order []*Span
	for _, ev := range events {
		switch ev.Type {
		case "start":
			if _, ok := spans[ev.ID]; ok {
				continue
			}
			s := &Span{ID: ev.ID, Name: ev.Name, Parent: ev.Parent, Attrs: ev.Attrs}
			spans[s.ID] = s
			order = append(order, s)
		case "end":
			if s, ok := spans[ev.ID]; ok {
				s.Ended = true
			}
		}
	}
	childrenOf = map[string][]*Span{}
	for _, s := range order {
		if s.Parent == "" {
			roots = append(roots, s)
			continue
		}
		if _, ok := spans[s.Parent]; !ok {
			roots = append(roots, s) // orphan: parent span never observed
			continue
		}
		childrenOf[s.Parent] = append(childrenOf[s.Parent], s)
	}
	return childrenOf, roots
}
