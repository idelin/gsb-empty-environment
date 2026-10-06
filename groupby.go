package dictagg

import "sort"

// GroupRow is one output group of a grouped aggregation.
type GroupRow struct {
	Key     string // decoded group key; meaningless when NullKey is true
	NullKey bool   // true for the group of NULL keys
	Sum     int64  // sum of non-NULL values
	Count   int    // number of non-NULL values
	Rows    int    // selected rows in the group (incl. NULL values, incl. duplicates)
}

// GroupSum groups the rows listed in sel by the decoded key of keys and sums
// the non-NULL values of vals per group.
//
// Semantics:
//   - sel may be empty (result is empty) and may contain duplicate row
//     indices; each occurrence contributes independently.
//   - Rows with a NULL key form a single NULL group, emitted last.
//   - NULL values never contribute to Sum or Count, but their rows still
//     belong to their key's group and are reflected in Rows.
//   - Groups are sorted by decoded key (byte-wise), NOT by dictionary code.
func GroupSum(keys *DictColumn, vals *IntColumn, sel []int) []GroupRow {
	type agg struct {
		sum   int64
		count int
		rows  int
	}
	byKey := make(map[string]*agg)
	var nullAgg *agg
	get := func(m map[string]*agg, k string) *agg {
		a, ok := m[k]
		if !ok {
			a = &agg{}
			m[k] = a
		}
		return a
	}

	for _, i := range sel {
		key, keyOK := keys.ValueAt(i)
		var a *agg
		if keyOK {
			a = get(byKey, key)
		} else {
			if nullAgg == nil {
				nullAgg = &agg{}
			}
			a = nullAgg
		}
		a.rows++
		if v, ok := vals.ValueAt(i); ok {
			a.sum += v
			a.count++
		}
	}

	out := make([]GroupRow, 0, len(byKey)+1)
	for k, a := range byKey {
		out = append(out, GroupRow{Key: k, Sum: a.sum, Count: a.count, Rows: a.rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if nullAgg != nil {
		out = append(out, GroupRow{NullKey: true, Sum: nullAgg.sum, Count: nullAgg.count, Rows: nullAgg.rows})
	}
	return out
}
