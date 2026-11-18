package netsec

import "fmt"

// Standard audit criteria.
//
// CriteriaSafeDeploy is the stronger gate: anything allowed to deploy is
// necessarily allowed to run, but an audit under CriteriaSafeRun alone never
// satisfies a safe-to-deploy gate. The reverse implication does not hold.
const (
	CriteriaSafeRun    = "safe-to-run"
	CriteriaSafeDeploy = "safe-to-deploy"
)

// FullAudit certifies a single version from scratch. It is the only kind of
// record that can start a trusted audit chain.
type FullAudit struct {
	Version  string
	Criteria string
}

// DeltaAudit certifies the transition From -> To. It is only meaningful when
// From is itself reachable from a full audit along the same criteria.
type DeltaAudit struct {
	From     string
	To       string
	Criteria string
}

// Violation marks every version inside Range as forbidden regardless of any
// audit record. Violations take precedence over audit reachability.
type Violation struct {
	Range VersionRange
}

// CriterionImplicate reports whether an audit performed under criteria a is
// sufficient to satisfy a gate under criteria b. The default resolver only
// knows that safe-to-deploy implies safe-to-run (plus identity). Pass a
// custom function to model additional domain-specific criteria hierarchies.
type CriterionImplicate func(a, b string) bool

// DefaultCriterionImplicate implements the built-in criteria hierarchy:
// every criteria implies itself, and safe-to-deploy implies safe-to-run.
func DefaultCriterionImplicate(a, b string) bool {
	if a == b {
		return true
	}
	if a == CriteriaSafeDeploy && b == CriteriaSafeRun {
		return true
	}
	return false
}

// Resolver evaluates whether crate versions may pass the dependency gate.
// It is safe for concurrent use after NewResolver returns.
type Resolver struct {
	implicates CriterionImplicate
	violations []VersionRange

	// reachable[criteria] holds the canonical versions reachable from a
	// full audit using only delta edges of that same criteria.
	reachable map[string]map[string]bool
}

// NewResolver validates every record and precomputes, per criteria, the set
// of versions reachable from the full audits. Cycles are handled by visited
// marking; dangling deltas whose From is never reachable simply contribute
// nothing (a broken chain).
func NewResolver(full []FullAudit, delta []DeltaAudit, violations []Violation, implicates CriterionImplicate) (*Resolver, error) {
	if implicates == nil {
		implicates = DefaultCriterionImplicate
	}

	type edge struct {
		from, to string
	}
	roots := make(map[string][]string)
	adjacency := make(map[string][]edge)

	for i, audit := range full {
		v, err := ParseVersion(audit.Version)
		if err != nil {
			return nil, fmt.Errorf("netsec: full audit #%d: %w", i, err)
		}
		if audit.Criteria == "" {
			return nil, fmt.Errorf("netsec: full audit #%d: empty criteria", i)
		}
		roots[audit.Criteria] = append(roots[audit.Criteria], v.String())
	}
	for i, audit := range delta {
		from, err := ParseVersion(audit.From)
		if err != nil {
			return nil, fmt.Errorf("netsec: delta audit #%d (from): %w", i, err)
		}
		to, err := ParseVersion(audit.To)
		if err != nil {
			return nil, fmt.Errorf("netsec: delta audit #%d (to): %w", i, err)
		}
		if audit.Criteria == "" {
			return nil, fmt.Errorf("netsec: delta audit #%d: empty criteria", i)
		}
		adjacency[audit.Criteria] = append(adjacency[audit.Criteria], edge{
			from: from.String(),
			to:   to.String(),
		})
	}

	reachable := make(map[string]map[string]bool)
	for criteria, starts := range roots {
		edges := adjacency[criteria]
		next := make(map[string][]string)
		for _, e := range edges {
			next[e.from] = append(next[e.from], e.to)
		}
		seen := make(map[string]bool)
		queue := append([]string(nil), starts...)
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			queue = append(queue, next[cur]...)
		}
		reachable[criteria] = seen
	}

	ranges := make([]VersionRange, len(violations))
	for i, violation := range violations {
		ranges[i] = violation.Range
	}

	return &Resolver{
		implicates: implicates,
		violations: ranges,
		reachable:  reachable,
	}, nil
}

// Allowed reports whether version passes the gate for the requested criteria.
//
// The version must be reachable from a full audit via delta edges of one
// single criteria whose audits imply the requested criteria, and it must not
// fall inside any violation range. Violations are checked first and always
// win: a violated version is denied even when a valid audit chain reaches it.
func (r *Resolver) Allowed(version, criteria string) (bool, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return false, err
	}
	if criteria == "" {
		return false, fmt.Errorf("netsec: empty gate criteria")
	}
	canonical := v.String()

	for _, violationRange := range r.violations {
		if violationRange.Contains(v) {
			return false, nil
		}
	}

	for auditCriteria, versions := range r.reachable {
		if !r.implicates(auditCriteria, criteria) {
			continue
		}
		if versions[canonical] {
			return true, nil
		}
	}
	return false, nil
}

// Violated reports whether version is forbidden by a violation range,
// independent of any audit chain.
func (r *Resolver) Violated(version string) (bool, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return false, err
	}
	for _, violationRange := range r.violations {
		if violationRange.Contains(v) {
			return true, nil
		}
	}
	return false, nil
}
