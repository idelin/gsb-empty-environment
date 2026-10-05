package main

import "sort"

// ChangeSet is the result of diffing the old and new call graphs.
type ChangeSet struct {
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"` // edge-set changed or explicitly marked
}

// Roots returns every changed symbol, sorted and deduplicated. These are
// the starting points of impact propagation.
func (cs *ChangeSet) Roots() []string {
	all := append(append(append([]string{}, cs.Added...), cs.Removed...), cs.Modified...)
	return dedup(all)
}

// Diff computes the change set between the old and new graphs. A symbol
// present in both graphs is modified when its set of outgoing call sites
// changed (edge added, removed, or re-resolved). explicitModified lists
// symbols whose body changed without an observable edge change.
func Diff(oldG, newG *Graph, explicitModified []string) *ChangeSet {
	cs := &ChangeSet{
		Added:    []string{},
		Removed:  []string{},
		Modified: []string{},
	}
	for name := range oldG.Symbols {
		if _, ok := newG.Symbols[name]; !ok {
			cs.Removed = append(cs.Removed, name)
		}
	}
	for name := range newG.Symbols {
		if _, ok := oldG.Symbols[name]; !ok {
			cs.Added = append(cs.Added, name)
		}
	}
	for name := range newG.Symbols {
		if _, ok := oldG.Symbols[name]; !ok {
			continue
		}
		if edgeSetKey(oldG.Edges[name]) != edgeSetKey(newG.Edges[name]) {
			cs.Modified = append(cs.Modified, name)
		}
	}
	for _, name := range explicitModified {
		if _, ok := newG.Symbols[name]; ok {
			cs.Modified = append(cs.Modified, name)
		} else if _, ok := oldG.Symbols[name]; ok {
			cs.Removed = append(cs.Removed, name)
		}
	}
	cs.Added = dedup(cs.Added)
	cs.Removed = dedup(cs.Removed)
	cs.Modified = dedup(cs.Modified)
	return cs
}

func edgeSetKey(edges []Edge) string {
	keys := make([]string, 0, len(edges))
	for _, e := range edges {
		keys = append(keys, e.canonicalKey())
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += "\x00"
		}
		out += k
	}
	return out
}
