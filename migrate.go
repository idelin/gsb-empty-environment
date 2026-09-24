package verenum

import (
	"fmt"
)

// Migrate resolves a lexical value observed at version from and returns its
// canonical name at version to.
//
// path is the original field path (e.g. "items[2].status") and is attached
// to ambiguity/conflict errors verbatim. When the merged alias graph gives a
// name more than one stable ID, Migrate returns *AmbiguityError instead of
// choosing a candidate.
func (r *Registry) Migrate(from, to Version, name, path string) (string, error) {
	if !r.hasVersion(to) {
		return "", fmt.Errorf("verenum: target version v%d is not registered", to)
	}
	g, err := r.mergeGraphs(r.versionSpan(from, to))
	if err != nil {
		return "", err
	}
	res := g.lookup(name)
	if !res.found {
		return "", &UnknownNameError{Name: name, From: from, To: to}
	}
	if res.ambiguous {
		return "", &AmbiguityError{Path: path, Name: name, Chains: g.shortestChains(name)}
	}
	canon, ok := r.versions[to].canon[res.id]
	if !ok {
		return "", fmt.Errorf("verenum: stable id %d (from %q) is not defined at target v%d", res.id, name, to)
	}
	return canon, nil
}

// VerifyPath performs a full-path migration consistency check across an
// ordered list of (typically minor) versions. Every lexical name appearing
// in any version must always denote the same stable ID along the path.
//
// On the first divergence it returns *PathConflictError carrying the shortest
// conflict chains and the original field path.
func (r *Registry) VerifyPath(versions []Version, path string) error {
	if len(versions) < 2 {
		return nil
	}
	g, err := r.mergeGraphs(versions)
	if err != nil {
		return err
	}
	// Deterministic name order.
	names := make([]string, 0)
	seen := map[string]bool{}
	for _, v := range versions {
		for name := range r.versions[v].byName {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	// Stable traversal order independent of map iteration.
	insertion := names
	for _, name := range insertion {
		if res := g.lookup(name); res.ambiguous {
			return &PathConflictError{Path: path, Name: name, Chains: g.shortestChains(name)}
		}
	}
	return nil
}
