package starjoin

// Predicate evaluates a filter condition against row i of a table.
type Predicate func(t *Table, i int) bool

// EqInt matches rows where column col equals v.
func EqInt(col string, v int64) Predicate {
	return func(t *Table, i int) bool {
		c := t.Col(col)
		return c != nil && c.Kind == IntCol && c.Ints[i] == v
	}
}

// BetweenInt matches rows where lo <= col <= hi.
func BetweenInt(col string, lo, hi int64) Predicate {
	return func(t *Table, i int) bool {
		c := t.Col(col)
		return c != nil && c.Kind == IntCol && c.Ints[i] >= lo && c.Ints[i] <= hi
	}
}

// EqStr matches rows where column col equals v.
func EqStr(col string, v string) Predicate {
	return func(t *Table, i int) bool {
		c := t.Col(col)
		return c != nil && c.Kind == StrCol && c.Strs[i] == v
	}
}

// InStr matches rows where column col is one of vals.
func InStr(col string, vals ...string) Predicate {
	set := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		set[v] = struct{}{}
	}
	return func(t *Table, i int) bool {
		c := t.Col(col)
		if c == nil || c.Kind != StrCol {
			return false
		}
		_, ok := set[c.Strs[i]]
		return ok
	}
}

// And conjunctively combines predicates.
func And(ps ...Predicate) Predicate {
	return func(t *Table, i int) bool {
		for _, p := range ps {
			if !p(t, i) {
				return false
			}
		}
		return true
	}
}

// Or disjunctively combines predicates.
func Or(ps ...Predicate) Predicate {
	return func(t *Table, i int) bool {
		for _, p := range ps {
			if p(t, i) {
				return true
			}
		}
		return false
	}
}
