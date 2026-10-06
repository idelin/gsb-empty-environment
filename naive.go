package dictagg

import "sort"

// NaiveGroupSum is the reference executor: it fully decodes both columns into
// plain row-wise values, then applies the selection vector and grouping with
// no awareness of dictionary encoding. It exists to cross-check GroupSum.
func NaiveGroupSum(keys *DictColumn, vals *IntColumn, sel []int) []GroupRow {
	type decodedKey struct {
		key string
		ok  bool
	}
	type decodedVal struct {
		v  int64
		ok bool
	}
	dk := make([]decodedKey, keys.Len())
	for i := range dk {
		k, ok := keys.ValueAt(i)
		dk[i] = decodedKey{k, ok}
	}
	dv := make([]decodedVal, vals.Len())
	for i := range dv {
		v, ok := vals.ValueAt(i)
		dv[i] = decodedVal{v, ok}
	}

	byKey := make(map[string]*GroupRow)
	var nullRow *GroupRow
	for _, i := range sel {
		k := dk[i]
		var g *GroupRow
		if k.ok {
			g = byKey[k.key]
			if g == nil {
				g = &GroupRow{Key: k.key}
				byKey[k.key] = g
			}
		} else {
			if nullRow == nil {
				nullRow = &GroupRow{NullKey: true}
			}
			g = nullRow
		}
		g.Rows++
		if dv[i].ok {
			g.Sum += dv[i].v
			g.Count++
		}
	}

	out := make([]GroupRow, 0, len(byKey)+1)
	for _, g := range byKey {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if nullRow != nil {
		out = append(out, *nullRow)
	}
	return out
}
