package resolver

import (
	"fmt"
	"sort"
)

// Solve resolves reqs against the manifest.
//
// Objective, in order:
//  1. maximize the version vector of the requested packages lexicographically
//     (in the order the requirements are given);
//  2. minimize the total number of installed (transitive) packages.
//
// On success it returns the dependency lock. On failure it returns an
// *UnsatError describing a minimal unsatisfiable core and a concrete reason.
func Solve(m *Manifest, reqs []Requirement) (*Lock, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	if err := checkRequirements(m, reqs); err != nil {
		return nil, err
	}

	// Phase 1: greedily fix each requested package to the highest version
	// that still admits a feasible completion. This yields the
	// lexicographically maximal requested-version vector.
	fixed := make(map[string]Version, len(reqs))
	for _, r := range reqs {
		placed := false
		for _, v := range m.versionsInRange(r.Pkg, r.Range) {
			fixed[r.Pkg] = v
			if cspOK(m, fixed, reqs) {
				placed = true
				break
			}
		}
		if !placed {
			delete(fixed, r.Pkg)
			return nil, m.explain(reqs)
		}
	}

	// Phase 2: with the requested versions fixed, minimize the number of
	// installed packages.
	best := minimize(m, fixed, reqs)
	if best == nil {
		// Unreachable: phase 1 proved feasibility.
		return nil, m.explain(reqs)
	}
	return &Lock{Versions: best}, nil
}

func checkRequirements(m *Manifest, reqs []Requirement) error {
	seen := make(map[string]bool, len(reqs))
	for _, r := range reqs {
		if _, ok := m.Versions[r.Pkg]; !ok {
			return fmt.Errorf("requested unknown package %q", r.Pkg)
		}
		if seen[r.Pkg] {
			return fmt.Errorf("duplicate requirement for package %q", r.Pkg)
		}
		seen[r.Pkg] = true
	}
	return nil
}

// csp is a backtracking search state over the finite manifest.
type csp struct {
	m        *Manifest
	assigned map[string]Version // installed packages
	required map[string]Range   // packages that must be installed, with allowed range
	expanded map[string]bool    // assigned packages whose deps are already propagated
}

func newCSP(m *Manifest, fixed map[string]Version, reqs []Requirement) (*csp, bool) {
	c := &csp{
		m:        m,
		assigned: make(map[string]Version, len(fixed)),
		required: make(map[string]Range, len(reqs)),
		expanded: make(map[string]bool),
	}
	for pkg, v := range fixed {
		c.assigned[pkg] = v
	}
	for _, r := range reqs {
		if _, ok := c.assigned[r.Pkg]; !ok {
			c.required[r.Pkg] = r.Range
		}
	}
	// Fixed assignments must not conflict with each other.
	pvs := make([]PkgVersion, 0, len(fixed))
	for pkg, v := range fixed {
		pvs = append(pvs, PkgVersion{pkg, v})
	}
	for i := 0; i < len(pvs); i++ {
		for j := i + 1; j < len(pvs); j++ {
			if m.conflictsWith(pvs[i], pvs[j]) {
				return nil, false
			}
		}
	}
	return c, true
}

func (c *csp) clone() *csp {
	n := &csp{
		m:        c.m,
		assigned: make(map[string]Version, len(c.assigned)),
		required: make(map[string]Range, len(c.required)),
		expanded: make(map[string]bool, len(c.expanded)),
	}
	for k, v := range c.assigned {
		n.assigned[k] = v
	}
	for k, v := range c.required {
		n.required[k] = v
	}
	for k, v := range c.expanded {
		n.expanded[k] = v
	}
	return n
}

// propagate expands dependencies of assigned packages until fixpoint.
// It returns false as soon as a contradiction is detected.
func (c *csp) propagate() bool {
	for {
		pkg, ver, found := c.nextUnexpanded()
		if !found {
			break
		}
		c.expanded[pkg] = true
		for _, d := range c.m.Deps[PkgVersion{pkg, ver}] {
			if av, ok := c.assigned[d.Pkg]; ok {
				if !d.Range.Contains(av) {
					return false
				}
				continue
			}
			r, ok := c.required[d.Pkg]
			if !ok {
				r = AnyRange()
			}
			r, ok = r.Intersect(d.Range)
			if !ok {
				return false
			}
			c.required[d.Pkg] = r
		}
	}
	// Cheap domain check: every required package must still have a candidate.
	for pkg, r := range c.required {
		if !c.m.hasVersionInRange(pkg, r) {
			return false
		}
	}
	return true
}

func (c *csp) nextUnexpanded() (string, Version, bool) {
	names := make([]string, 0, len(c.assigned))
	for pkg := range c.assigned {
		if !c.expanded[pkg] {
			names = append(names, pkg)
		}
	}
	if len(names) == 0 {
		return "", Version{}, false
	}
	sort.Strings(names)
	return names[0], c.assigned[names[0]], true
}

// pickUnassigned chooses the unassigned required package with the fewest
// candidate versions (most-constrained first), for deterministic search.
func (c *csp) pickUnassigned() (string, []Version, bool) {
	names := make([]string, 0, len(c.required))
	for pkg := range c.required {
		names = append(names, pkg)
	}
	sort.Strings(names)
	bestPkg := ""
	var bestVers []Version
	for _, pkg := range names {
		vers := c.m.versionsInRange(pkg, c.required[pkg])
		if bestVers == nil || len(vers) < len(bestVers) {
			bestPkg, bestVers = pkg, vers
		}
	}
	if bestVers == nil {
		return "", nil, false
	}
	return bestPkg, bestVers, true
}

// assignable reports whether pkg@v may be installed alongside the current
// assignment (conflict check).
func (c *csp) assignable(pkg string, v Version) bool {
	pv := PkgVersion{pkg, v}
	for op, ov := range c.assigned {
		if c.m.conflictsWith(pv, PkgVersion{op, ov}) {
			return false
		}
	}
	return true
}

// branch returns child states, one per candidate version of the picked
// package, highest version first. Inconsistent children are dropped.
func (c *csp) branch() []*csp {
	pkg, vers, ok := c.pickUnassigned()
	if !ok {
		return nil
	}
	var out []*csp
	for _, v := range vers {
		child := c.clone()
		delete(child.required, pkg)
		child.assigned[pkg] = v
		if child.assignable(pkg, v) && child.propagate() {
			out = append(out, child)
		}
	}
	return out
}

// searchAny finds any consistent completion (depth-first).
func (c *csp) searchAny() bool {
	if !c.propagate() {
		return false
	}
	if _, _, done := c.pickUnassigned(); done == false {
		return true
	}
	for _, child := range c.branch() {
		if child.searchAny() {
			return true
		}
	}
	return false
}

// searchMin finds a consistent completion with the fewest installed packages
// (branch and bound). Deterministic: packages and versions are tried in a
// fixed order, so equal-cost ties resolve identically every run.
func (c *csp) searchMin(best *map[string]Version) {
	if !c.propagate() {
		return
	}
	lowerBound := len(c.assigned) + len(c.required)
	if *best != nil && lowerBound >= len(*best) {
		return
	}
	if _, _, done := c.pickUnassigned(); done == false {
		sol := make(map[string]Version, len(c.assigned))
		for k, v := range c.assigned {
			sol[k] = v
		}
		*best = sol
		return
	}
	for _, child := range c.branch() {
		child.searchMin(best)
	}
}

func cspOK(m *Manifest, fixed map[string]Version, reqs []Requirement) bool {
	c, ok := newCSP(m, fixed, reqs)
	return ok && c.searchAny()
}

func minimize(m *Manifest, fixed map[string]Version, reqs []Requirement) map[string]Version {
	c, ok := newCSP(m, fixed, reqs)
	if !ok {
		return nil
	}
	var best map[string]Version
	c.searchMin(&best)
	return best
}
