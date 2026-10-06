package dictagg

// A selection vector is a []int of row indices. It may be empty and may
// contain the same row more than once; downstream operators treat each
// occurrence as a distinct input row.

// FilterEq returns a selection vector of rows whose decoded key equals key.
// NULL rows never match.
func FilterEq(c *DictColumn, key string) []int {
	var sel []int
	for i := 0; i < c.Len(); i++ {
		if v, ok := c.ValueAt(i); ok && v == key {
			sel = append(sel, i)
		}
	}
	return sel
}

// FilterIn returns a selection vector of rows whose decoded key is in keys.
// NULL rows never match.
func FilterIn(c *DictColumn, keys ...string) []int {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	var sel []int
	for i := 0; i < c.Len(); i++ {
		if v, ok := c.ValueAt(i); ok {
			if _, hit := set[v]; hit {
				sel = append(sel, i)
			}
		}
	}
	return sel
}

// FilterNotNull returns a selection vector of all non-NULL rows.
func FilterNotNull(c *DictColumn) []int {
	var sel []int
	for i := 0; i < c.Len(); i++ {
		if !c.Nulls.IsNull(i) {
			sel = append(sel, i)
		}
	}
	return sel
}

// FilterBy returns a selection vector of rows for which pred accepts the
// decoded key. pred is never called for NULL rows; they never match.
func FilterBy(c *DictColumn, pred func(key string) bool) []int {
	var sel []int
	for i := 0; i < c.Len(); i++ {
		if v, ok := c.ValueAt(i); ok && pred(v) {
			sel = append(sel, i)
		}
	}
	return sel
}
