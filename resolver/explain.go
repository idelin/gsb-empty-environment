package resolver

import (
	"fmt"
	"strings"
)

// UnsatError explains why a set of requirements cannot be resolved.
type UnsatError struct {
	// Core is a deletion-minimal subset of the requirements that is already
	// unsatisfiable on its own.
	Core []Requirement
	// Detail is a human-readable description of the concrete cause.
	Detail string
}

func (e *UnsatError) Error() string {
	parts := make([]string, 0, len(e.Core))
	for _, r := range e.Core {
		parts = append(parts, r.String())
	}
	return fmt.Sprintf("unsatisfiable requirements {%s}: %s",
		strings.Join(parts, ", "), e.Detail)
}

// explain diagnoses why reqs cannot be satisfied. It assumes reqs are
// unsatisfiable (checked by Solve).
func (m *Manifest) explain(reqs []Requirement) error {
	// Case 1: a requirement matches no manifest version at all.
	for _, r := range reqs {
		if !m.hasVersionInRange(r.Pkg, r.Range) {
			return &UnsatError{
				Core: []Requirement{r},
				Detail: fmt.Sprintf("manifest has no version of %q in range %s",
					r.Pkg, r.Range),
			}
		}
	}

	// Case 2: shrink to a deletion-minimal unsatisfiable core.
	core := append([]Requirement(nil), reqs...)
	i := 0
	for i < len(core) {
		trial := make([]Requirement, 0, len(core)-1)
		trial = append(trial, core[:i]...)
		trial = append(trial, core[i+1:]...)
		if !cspOK(m, nil, trial) {
			core = trial
		} else {
			i++
		}
	}

	var detail string
	switch len(core) {
	case 1:
		detail = m.explainSingle(core[0])
	case 2:
		detail = m.explainPair(core[0], core[1])
	default:
		detail = "no combination of versions satisfies these requirements simultaneously"
	}
	return &UnsatError{Core: core, Detail: detail}
}

// explainSingle diagnoses a single requirement that is unsatisfiable alone.
func (m *Manifest) explainSingle(r Requirement) string {
	// Look for a direct, concrete cause on the highest candidate version:
	// a dependency that no manifest version can satisfy.
	for _, v := range m.versionsInRange(r.Pkg, r.Range) {
		pv := PkgVersion{r.Pkg, v}
		for _, d := range m.Deps[pv] {
			if !m.hasVersionInRange(d.Pkg, d.Range) {
				return fmt.Sprintf("%s requires %s in %s, but the manifest has no version of %q in that range",
					pv, d.Pkg, d.Range, d.Pkg)
			}
		}
	}
	return fmt.Sprintf("every version of %q in %s leads to conflicting transitive dependencies",
		r.Pkg, r.Range)
}

// explainPair diagnoses a pair of requirements that clash with each other.
func (m *Manifest) explainPair(a, b Requirement) string {
	for _, va := range m.versionsInRange(a.Pkg, a.Range) {
		pva := PkgVersion{a.Pkg, va}
		for _, vb := range m.versionsInRange(b.Pkg, b.Range) {
			pvb := PkgVersion{b.Pkg, vb}
			if m.conflictsWith(pva, pvb) {
				return fmt.Sprintf("%s conflicts with %s and cannot be installed together with it",
					pva, pvb)
			}
		}
		for _, d := range m.Deps[pva] {
			if d.Pkg == b.Pkg && !d.Range.Intersects(b.Range) {
				return fmt.Sprintf("%s requires %s in %s, which does not overlap the requested %s",
					pva, b.Pkg, d.Range, b.Range)
			}
		}
	}
	for _, vb := range m.versionsInRange(b.Pkg, b.Range) {
		pvb := PkgVersion{b.Pkg, vb}
		for _, d := range m.Deps[pvb] {
			if d.Pkg == a.Pkg && !d.Range.Intersects(a.Range) {
				return fmt.Sprintf("%s requires %s in %s, which does not overlap the requested %s",
					pvb, a.Pkg, d.Range, a.Range)
			}
		}
	}
	return fmt.Sprintf("no combination of versions satisfies %q and %q simultaneously",
		a.Pkg, b.Pkg)
}
