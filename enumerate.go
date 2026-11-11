package netsec

// enumCounts holds, for one use/revoke pair, how many feasible assignments
// place the usage strictly before, strictly after, or at the same tick as
// the revocation.
type enumCounts struct {
	before int64
	after  int64
	same   int64
}

// enumerateGroup exhaustively assigns every event a tick inside its narrowed
// window, rejecting assignments that violate any trusted sequence edge. Each
// feasible assignment updates the per-pair counts, so the counts give an
// independent verdict that does not trust the analytic bound reasoning.
//
// skipped is true when the configured node/assignment budget is exceeded or
// the group contains an infeasible node; in that case no counts are returned
// and the analytic verdict stands alone.
func enumerateGroup(g *constraintGraph, pairs []pairIndices, o Options) (counts []enumCounts, skipped bool) {
	if len(g.nodes) > o.MaxEnumNodes {
		return nil, true
	}

	order, ok := g.topoOrder()
	if !ok {
		return nil, true
	}
	for _, n := range g.nodes {
		if !n.feasible {
			return nil, true
		}
	}

	// Cheap pre-check on the search-space size.
	domainSize := int64(1)
	for _, i := range order {
		domainSize = mulCap(domainSize,
			windowWidth(g.nodes[i].window.Lo, g.nodes[i].window.Hi),
			o.MaxEnumAssignments)
		if domainSize > o.MaxEnumAssignments {
			return nil, true
		}
	}

	counts = make([]enumCounts, len(pairs))
	ticks := make([]int64, len(g.nodes))
	var explored int64

	var backtrack func(pos int) bool
	backtrack = func(pos int) bool {
		if pos == len(order) {
			explored++
			for k, p := range pairs {
				switch {
				case ticks[p.use] < ticks[p.revoke]:
					counts[k].before++
				case ticks[p.use] > ticks[p.revoke]:
					counts[k].after++
				default:
					counts[k].same++
				}
			}
			return explored < o.MaxEnumAssignments
		}

		i := order[pos]
		n := g.nodes[i]
		// Count down: decrementing Lo cannot wrap, whereas t++ would
		// overflow at MaxInt64 and loop forever.
		for t := n.window.Hi; t >= n.window.Lo; t-- {
			valid := true
			for _, p := range n.pred {
				if ticks[p] >= t {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			ticks[i] = t
			if !backtrack(pos + 1) {
				return false
			}
		}
		return true
	}

	if !backtrack(0) {
		return nil, true
	}
	return counts, false
}
