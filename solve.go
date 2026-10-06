package csp

// Solve performs complete depth-first backtracking search and returns all
// solutions (up to limit; limit <= 0 means no limit). Each solution maps
// variable name to its assigned value.
//
// Solve is the authoritative satisfiability check: an empty result proves
// unsatisfiability, whereas Propagate returning true does not prove
// satisfiability. Domains are fully restored before Solve returns, so the
// problem can be reused.
func (p *Problem) Solve(limit int) []map[string]int {
	entry := p.Snapshot()
	defer p.Restore(entry)

	if !p.Propagate() {
		return nil
	}
	var sols []map[string]int
	p.search(&sols, limit)
	return sols
}

func (p *Problem) search(sols *[]map[string]int, limit int) {
	if limit > 0 && len(*sols) >= limit {
		return
	}
	v := p.pickVar()
	if v == nil {
		// Complete assignment: accept only if every constraint holds.
		// Propagators are deliberately weak, so this check is the
		// soundness net.
		assign := make(map[*Var]int, len(p.vars))
		for _, pv := range p.vars {
			for val := range pv.Dom {
				assign[pv] = val
			}
		}
		for _, c := range p.cons {
			if !c.Holds(assign) {
				return
			}
		}
		sol := make(map[string]int, len(p.vars))
		for _, pv := range p.vars {
			for val := range pv.Dom {
				sol[pv.Name] = val
			}
		}
		*sols = append(*sols, sol)
		return
	}
	for _, val := range sortedValues(v.Dom) {
		snap := p.Snapshot()
		v.Dom = map[int]struct{}{val: {}}
		if p.Propagate() {
			p.search(sols, limit)
		}
		p.Restore(snap)
		if limit > 0 && len(*sols) >= limit {
			return
		}
	}
}

// pickVar selects the unassigned variable with the smallest domain
// (minimum remaining values heuristic). Returns nil when all variables
// are singletons.
func (p *Problem) pickVar() *Var {
	var best *Var
	for _, v := range p.vars {
		if len(v.Dom) > 1 && (best == nil || len(v.Dom) < len(best.Dom)) {
			best = v
		}
	}
	return best
}
