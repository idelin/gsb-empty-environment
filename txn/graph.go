package txn

import (
	"fmt"
	"sort"
)

// orderRel expresses "X Before Y" regardless of whether the source unit
// declared Before or the target unit declared After. Reason records the
// original declaration for diagnostics.
type orderRel struct {
	x, y   string
	reason string
}

// depIndex holds precomputed reverse and symmetric adjacency so that
// transaction expansion never scans the whole graph.
type depIndex struct {
	// revPartOf[parent] are units declaring PartOf=parent.
	revPartOf map[string][]string
	// revBindsTo[parent] are units declaring BindsTo=parent.
	revBindsTo map[string][]string
	// conflicts[u] is the symmetric conflict closure of u.
	conflicts map[string][]string
	// orders holds every unique "x Before y" relation in sorted order.
	orders []orderRel
}

func buildIndex(g *Graph) *depIndex {
	idx := &depIndex{
		revPartOf:  map[string][]string{},
		revBindsTo: map[string][]string{},
		conflicts:  map[string][]string{},
	}
	confSet := map[string]map[string]struct{}{}
	orderSet := map[orderRel]struct{}{}

	add := func(m map[string]map[string]struct{}, from, to string) {
		if m[from] == nil {
			m[from] = map[string]struct{}{}
		}
		m[from][to] = struct{}{}
	}

	for name, u := range g.Units {
		for _, child := range u.PartOf {
			idx.revPartOf[child] = append(idx.revPartOf[child], name)
		}
		for _, parent := range u.BindsTo {
			idx.revBindsTo[parent] = append(idx.revBindsTo[parent], name)
		}
		for _, other := range u.Conflicts {
			add(confSet, name, other)
			add(confSet, other, name)
		}
		for _, later := range u.Before {
			orderSet[orderRel{name, later, fmt.Sprintf("%s Before %s", name, later)}] = struct{}{}
		}
		for _, earlier := range u.After {
			orderSet[orderRel{earlier, name, fmt.Sprintf("%s After %s", name, earlier)}] = struct{}{}
		}
	}

	for k, v := range idx.revPartOf {
		sort.Strings(v)
		idx.revPartOf[k] = v
	}
	for k, v := range idx.revBindsTo {
		sort.Strings(v)
		idx.revBindsTo[k] = v
	}
	for u, set := range confSet {
		out := make([]string, 0, len(set))
		for name := range set {
			out = append(out, name)
		}
		sort.Strings(out)
		idx.conflicts[u] = out
	}
	for pair := range orderSet {
		idx.orders = append(idx.orders, pair)
	}
	sort.Slice(idx.orders, func(i, j int) bool {
		if idx.orders[i].x != idx.orders[j].x {
			return idx.orders[i].x < idx.orders[j].x
		}
		return idx.orders[i].y < idx.orders[j].y
	})
	return idx
}

// validateReferences rejects hard dependencies on missing units. Wants targets
// may be missing: systemd silently ignores an unsatisfiable Wants edge.
func validateReferences(g *Graph) error {
	names := make([]string, 0, len(g.Units))
	for name := range g.Units {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		u := g.Units[name]
		check := func(kind, target string, optional bool) error {
			if g.Units[target] != nil || optional {
				return nil
			}
			return fmt.Errorf("%w: unit %q %s=%q", ErrUnknownUnit, name, kind, target)
		}
		for _, t := range u.Requires {
			if err := check("Requires", t, false); err != nil {
				return err
			}
		}
		for _, t := range u.Wants {
			if err := check("Wants", t, true); err != nil {
				return err
			}
		}
		for _, t := range u.BindsTo {
			if err := check("BindsTo", t, false); err != nil {
				return err
			}
		}
		for _, t := range u.Requisite {
			if err := check("Requisite", t, false); err != nil {
				return err
			}
		}
		for _, t := range u.PartOf {
			if err := check("PartOf", t, false); err != nil {
				return err
			}
		}
		for _, t := range u.Conflicts {
			if err := check("Conflicts", t, false); err != nil {
				return err
			}
		}
		for _, t := range u.Before {
			if err := check("Before", t, false); err != nil {
				return err
			}
		}
		for _, t := range u.After {
			if err := check("After", t, false); err != nil {
				return err
			}
		}
	}
	return nil
}
