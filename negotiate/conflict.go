package negotiate

import "fmt"

// Negotiate runs a full negotiation between profiles a and b. budget bounds
// the search nodes of the main solve (budget < 0 means unlimited); budget
// exhaustion is reported as StatusBudgetExceeded, never as infeasibility.
func Negotiate(a, b *Profile, budget int) Result {
	if err := a.Validate(); err != nil {
		return Result{Status: StatusInfeasible, Reasons: []string{err.Error()}}
	}
	if err := b.Validate(); err != nil {
		return Result{Status: StatusInfeasible, Reasons: []string{err.Error()}}
	}
	prob, reasons := buildProblem(a, b)
	if len(reasons) > 0 {
		core, pending := minimalCore(a, b, budget)
		return Result{Status: StatusInfeasible, Reasons: reasons, Core: core, CorePending: pending}
	}
	sel, score, status, nodes := prob.solve(budget)
	switch status {
	case StatusOK:
		return Result{Status: StatusOK, Selection: sel, Score: score, Nodes: nodes}
	case StatusBudgetExceeded:
		return Result{Status: StatusBudgetExceeded, Nodes: nodes}
	default:
		core, pending := minimalCore(a, b, budget)
		return Result{
			Status:      StatusInfeasible,
			Reasons:     []string{"no feature combination satisfies both parties' conditions"},
			Core:        core,
			CorePending: pending,
			Nodes:       nodes,
		}
	}
}

// Certificate binds a successful result to both profiles for later
// decoding. Only valid when r.Status == StatusOK.
func (r Result) Certificate(a, b *Profile) Certificate {
	return NewCertificate(a, b, r.Selection)
}

// atom is one removable declared condition, identified by side, kind and
// position inside the profile.
type atom struct {
	desc    string
	side    int // 0 -> a, 1 -> b
	kind    int
	feature string
	idx     int // dep or conflict index inside the feature declaration
}

const (
	atomRequired = iota
	atomSupport
	atomDep
	atomConflict
)

func buildAtoms(a, b *Profile) []atom {
	var atoms []atom
	for side, p := range []*Profile{a, b} {
		for _, f := range p.Features {
			if f.Required {
				atoms = append(atoms, atom{side: side, kind: atomRequired, feature: f.Name,
					desc: fmt.Sprintf("%s requires feature %q", p.Name, f.Name)})
			}
			atoms = append(atoms, atom{side: side, kind: atomSupport, feature: f.Name,
				desc: fmt.Sprintf("%s supports %q at %s", p.Name, f.Name, f.Versions)})
			for i, d := range f.Depends {
				atoms = append(atoms, atom{side: side, kind: atomDep, feature: f.Name, idx: i,
					desc: fmt.Sprintf("%s: %q depends on %s", p.Name, f.Name, d)})
			}
			for i, c := range f.Conflicts {
				atoms = append(atoms, atom{side: side, kind: atomConflict, feature: f.Name, idx: i,
					desc: fmt.Sprintf("%s: %q conflicts with %q", p.Name, f.Name, c)})
			}
		}
	}
	return atoms
}

// filterProfiles returns copies of a and b with the removed atoms' conditions
// stripped out.
func filterProfiles(a, b *Profile, atoms []atom, removed []bool) (*Profile, *Profile) {
	out := [2]*Profile{a.Clone(), b.Clone()}
	for i, at := range atoms {
		if !removed[i] {
			continue
		}
		p := out[at.side]
		f := p.feature(at.feature)
		switch at.kind {
		case atomRequired:
			if f != nil {
				f.Required = false
			}
		case atomSupport:
			nf := p.Features[:0]
			for _, g := range p.Features {
				if g.Name != at.feature {
					nf = append(nf, g)
				}
			}
			p.Features = nf
		case atomDep:
			if f != nil {
				f.Depends = append(f.Depends[:at.idx], f.Depends[at.idx+1:]...)
			}
		case atomConflict:
			if f != nil {
				f.Conflicts = append(f.Conflicts[:at.idx], f.Conflicts[at.idx+1:]...)
			}
		}
	}
	return out[0], out[1]
}

// feasible reports whether the profiles admit any selection, without
// optimizing. Budget exhaustion is reported distinctly.
func feasible(a, b *Profile, budget int) Status {
	if a.Validate() != nil || b.Validate() != nil {
		return StatusInfeasible
	}
	prob, reasons := buildProblem(a, b)
	if len(reasons) > 0 {
		return StatusInfeasible
	}
	_, _, st, _ := prob.solve(budget)
	return st
}

// minimalCore computes a 1-minimal conflicting subset of the declared
// conditions by deletion: every atom whose removal still leaves the problem
// infeasible is dropped. pending is true when the budget was insufficient
// for some feasibility check, in which case the core may be larger than
// necessary; the pending state is reported separately from the core itself.
func minimalCore(a, b *Profile, budget int) (core []string, pending bool) {
	atoms := buildAtoms(a, b)
	removed := make([]bool, len(atoms))
	for i := range atoms {
		removed[i] = true
		a2, b2 := filterProfiles(a, b, atoms, removed)
		switch feasible(a2, b2, budget) {
		case StatusOK:
			removed[i] = false // this atom is part of the conflict
		case StatusInfeasible:
			// still infeasible without it: keep it removed
		case StatusBudgetExceeded:
			removed[i] = false
			pending = true
		}
	}
	for i, at := range atoms {
		if !removed[i] {
			core = append(core, at.desc)
		}
	}
	return core, pending
}
