package dictagg

import "sort"

// Group is one output group of a group-by aggregation.
type Group struct {
	Key    string  // decoded key; meaningless when HasKey is false
	HasKey bool    // false for the null group
	Sum    float64 // sum of non-null values in the group
	HasSum bool    // false when no non-null value contributed to Sum
	Count  int     // number of selected rows in the group
}

// GroupBySum groups the rows picked by sel by the decoded key of keys and
// sums the non-null entries of vals per group.
//
// Rows whose key is null never contribute to any sum; they form a single
// dedicated null group (HasKey == false). Rows with a non-null key but a
// null value contribute to Count but not to Sum.
//
// The result is sorted by decoded key (lexicographic byte order), NOT by
// dictionary code; the null group, if present, sorts first. An empty
// selection yields an empty result.
func GroupBySum(keys *DictColumn, vals *Values, sel Selection) []Group {
	if keys.Len() != vals.Len() {
		panic("dictagg: key and value columns have different lengths")
	}

	// Accumulate per dictionary code, plus one slot for the null group.
	type acc struct {
		sum    float64
		count  int
		hasSum bool
	}
	codes := make([]acc, len(keys.Dict))
	var nullAcc acc
	hasNull := false

	for _, i := range sel {
		if keys.Nulls[i] {
			// Null keys never participate in the sum; they only
			// aggregate into their own group.
			nullAcc.count++
			hasNull = true
			continue
		}
		a := &codes[keys.Codes[i]]
		a.count++
		if !vals.Nulls[i] {
			a.sum += vals.V[i]
			a.hasSum = true
		}
	}

	groups := make([]Group, 0, len(keys.Dict)+1)
	if hasNull {
		groups = append(groups, Group{HasKey: false, Count: nullAcc.count})
	}
	for code, a := range codes {
		if a.count == 0 {
			continue
		}
		groups = append(groups, Group{
			Key:    keys.Dict[code],
			HasKey: true,
			Sum:    a.sum,
			HasSum: a.hasSum,
			Count:  a.count,
		})
	}

	// Sort by decoded key, never by dictionary code. The null group
	// (HasKey == false) always sorts first.
	sort.Slice(groups, func(x, y int) bool {
		gx, gy := groups[x], groups[y]
		if gx.HasKey != gy.HasKey {
			return !gx.HasKey
		}
		return gx.Key < gy.Key
	})
	return groups
}
