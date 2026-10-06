package testsel

import "sort"

// Changes describes what differs between two snapshots.
type Changes struct {
	// Modified functions exist in both snapshots with different hashes.
	Modified []FuncID
	// Added functions exist only in the new snapshot; their coverage is
	// unknown.
	Added []FuncID
	// Deleted functions exist only in the old snapshot.
	Deleted []FuncID
	// EdgesAdded and EdgesRemoved are static call edges that appeared or
	// disappeared.
	EdgesAdded   []Edge
	EdgesRemoved []Edge
	// DynChanged lists method names whose dynamic-dispatch target set
	// (implementations and/or their versions) changed.
	DynChanged []string
}

// Empty reports whether anything changed at all.
func (c *Changes) Empty() bool {
	return len(c.Modified) == 0 && len(c.Added) == 0 && len(c.Deleted) == 0 &&
		len(c.EdgesAdded) == 0 && len(c.EdgesRemoved) == 0 && len(c.DynChanged) == 0
}

// Diff computes the changes between an old and a new snapshot.
func Diff(old, new *Snapshot) *Changes {
	ch := &Changes{}
	for id, h := range new.Funcs {
		if oh, ok := old.Funcs[id]; !ok {
			ch.Added = append(ch.Added, id)
		} else if oh != h {
			ch.Modified = append(ch.Modified, id)
		}
	}
	for id := range old.Funcs {
		if _, ok := new.Funcs[id]; !ok {
			ch.Deleted = append(ch.Deleted, id)
		}
	}
	sortFuncs(ch.Added)
	sortFuncs(ch.Modified)
	sortFuncs(ch.Deleted)

	oldEdges := edgeSet(old.Edges)
	newEdges := edgeSet(new.Edges)
	for e := range newEdges {
		if !oldEdges[e] {
			ch.EdgesAdded = append(ch.EdgesAdded, e)
		}
	}
	for e := range oldEdges {
		if !newEdges[e] {
			ch.EdgesRemoved = append(ch.EdgesRemoved, e)
		}
	}
	sortEdges(ch.EdgesAdded)
	sortEdges(ch.EdgesRemoved)

	names := map[string]bool{}
	for n := range old.DynImpls {
		names[n] = true
	}
	for n := range new.DynImpls {
		names[n] = true
	}
	for n := range names {
		if dynImplKey(old, n) != dynImplKey(new, n) {
			ch.DynChanged = append(ch.DynChanged, n)
		}
	}
	sort.Strings(ch.DynChanged)
	return ch
}

// dynImplKey fingerprints the dispatch target set for a method name: the
// implementing functions together with their versions.
func dynImplKey(s *Snapshot, name string) string {
	impls := append([]FuncID(nil), s.DynImpls[name]...)
	sortFuncs(impls)
	key := ""
	for _, id := range impls {
		key += string(id) + "@" + s.Funcs[id] + "\x00"
	}
	return key
}

func edgeSet(edges []Edge) map[Edge]bool {
	out := make(map[Edge]bool, len(edges))
	for _, e := range edges {
		out[e] = true
	}
	return out
}

func sortFuncs(s []FuncID) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

func sortEdges(s []Edge) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Caller != s[j].Caller {
			return s[i].Caller < s[j].Caller
		}
		return s[i].Callee < s[j].Callee
	})
}
