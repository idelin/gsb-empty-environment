package dictagg

import "sort"

// row is a fully decoded input row.
type row struct {
	key    string
	hasKey bool
	val    float64
	hasVal bool
}

// decode fully materializes the rows picked by sel: every selected row is
// decoded through the dictionary into plain key/value pairs. This is the
// reference ("naive") execution model used to cross-check the vectorized
// executor.
func decode(keys *DictColumn, vals *Values, sel Selection) []row {
	rows := make([]row, 0, len(sel))
	for _, i := range sel {
		r := row{}
		r.key, r.hasKey = keys.KeyAt(int(i))
		if !vals.Nulls[i] {
			r.val, r.hasVal = vals.V[i], true
		}
		rows = append(rows, r)
	}
	return rows
}

// NaiveGroupBySum is the fully decoded reference implementation of
// GroupBySum. It decodes every selected row, groups with a plain map keyed
// by the decoded string, and sorts by decoded key with the null group first.
func NaiveGroupBySum(keys *DictColumn, vals *Values, sel Selection) []Group {
	type acc struct {
		sum    float64
		count  int
		hasSum bool
	}
	byKey := make(map[string]*acc)
	var nullAcc acc
	hasNull := false

	for _, r := range decode(keys, vals, sel) {
		if !r.hasKey {
			nullAcc.count++
			hasNull = true
			continue
		}
		a := byKey[r.key]
		if a == nil {
			a = &acc{}
			byKey[r.key] = a
		}
		a.count++
		if r.hasVal {
			a.sum += r.val
			a.hasSum = true
		}
	}

	groups := make([]Group, 0, len(byKey)+1)
	if hasNull {
		groups = append(groups, Group{HasKey: false, Count: nullAcc.count})
	}
	for k, a := range byKey {
		groups = append(groups, Group{
			Key:    k,
			HasKey: true,
			Sum:    a.sum,
			HasSum: a.hasSum,
			Count:  a.count,
		})
	}
	sort.Slice(groups, func(x, y int) bool {
		gx, gy := groups[x], groups[y]
		if gx.HasKey != gy.HasKey {
			return !gx.HasKey
		}
		return gx.Key < gy.Key
	})
	return groups
}
