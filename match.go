package spaneval

import "errors"

// errTooComplex guards the exhaustive mapper: it is a reference
// implementation for small trees, not a large-scale scheduler.
var errTooComplex = errors.New("spaneval: tree too complex for exhaustive matching")

// outcome accumulates the cost of a (partial) slot<->span mapping.
type outcome struct {
	missing    int // required slots left unmatched (subtree-weighted)
	dup        int // spans matched to no slot
	unfinished int // matched spans that never ended
	activated  int // required descendants of matched optional slots
	matched    []string
}

// cost orders outcomes: missing+duplicate dominate; unfinished only breaks
// ties (a finished span is preferred over an open one for the same slot).
func (o outcome) cost() int { return (o.missing+o.dup)*1000 + o.unfinished }

type matcher struct {
	kids  map[string][]*Span
	steps int
	limit int
}

// matchSiblings enumerates every injective assignment of spans to sibling
// slots and returns the minimum-cost one. A span may only match a slot
// beneath the slot instance its own parent matched, so parent-child
// structure and stable identity are enforced by construction — a
// correct-name span under the wrong parent can never cancel a missing
// slot, it becomes missing+duplicate instead.
func (m *matcher) matchSiblings(slots []*slot, spans []*Span) (outcome, []int, error) {
	best := outcome{missing: 1 << 40, dup: 1 << 40} // large but overflow-safe
	bestAssign := make([]int, len(slots))
	cur := make([]int, len(slots))
	used := make([]bool, len(spans))

	var rec func(i int, acc outcome) error
	rec = func(i int, acc outcome) error {
		m.steps++
		if m.steps > m.limit {
			return errTooComplex
		}
		if acc.cost() >= best.cost() {
			return nil
		}
		if i == len(slots) {
			for _, u := range used {
				if !u {
					acc.dup++
				}
			}
			if acc.cost() < best.cost() {
				best = acc
				copy(bestAssign, cur)
			}
			return nil
		}
		s := slots[i]
		// Option 1: leave the slot unmatched.
		cur[i] = -1
		skip := acc
		skip.missing += requiredSize(s)
		if err := rec(i+1, skip); err != nil {
			return err
		}
		// Option 2: match the slot to a compatible span.
		for j, sp := range spans {
			if used[j] {
				continue
			}
			sub, ok, err := m.matchSlotSpan(s, sp)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			used[j] = true
			cur[i] = j
			comb := outcome{
				missing:    acc.missing + sub.missing,
				dup:        acc.dup + sub.dup,
				unfinished: acc.unfinished + sub.unfinished,
				activated:  acc.activated + sub.activated,
				matched:    append(append([]string{}, acc.matched...), sub.matched...),
			}
			err = rec(i+1, comb)
			used[j] = false
			if err != nil {
				return err
			}
		}
		cur[i] = -1
		return nil
	}
	if err := rec(0, outcome{}); err != nil {
		return outcome{}, nil, err
	}
	return best, bestAssign, nil
}

// matchSlotSpan matches one slot to one span and recurses into the span's
// children. For a choice slot the branch is selected by the span name.
func (m *matcher) matchSlotSpan(s *slot, sp *Span) (outcome, bool, error) {
	var sub outcome
	if s.choice {
		found := false
		var bestSub outcome
		for _, b := range s.branches {
			if b.name != sp.Name {
				continue
			}
			o, _, err := m.matchSiblings(b.children, m.kids[sp.SpanID])
			if err != nil {
				return outcome{}, false, err
			}
			if !found || o.cost() < bestSub.cost() {
				bestSub, found = o, true
			}
		}
		if !found {
			return outcome{}, false, nil
		}
		sub = bestSub
	} else {
		if s.name != sp.Name {
			return outcome{}, false, nil
		}
		o, _, err := m.matchSiblings(s.children, m.kids[sp.SpanID])
		if err != nil {
			return outcome{}, false, err
		}
		sub = o
	}
	if !sp.Ended {
		sub.unfinished++
	}
	if s.optional {
		// Matching an optional slot activates its required descendants:
		// they join the denominator so the ratios stay coherent.
		sub.activated += requiredSizeForced(s) - 1
	}
	sub.matched = append(sub.matched, sp.SpanID)
	return sub, true, nil
}
