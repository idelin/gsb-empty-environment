package grouping

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

// refGroup keys a row by the grouping set using its own encoding.
func refKey(row Row, set []string) string {
	s := ""
	for _, c := range set {
		s += fmt.Sprintf("%#v|", row[c])
	}
	return s
}

// refAggregate is the independent per-group reference computer: plain
// loops over the group's rows, no shared code with the package.
func refAggregate(rows []Row, agg Aggregate) any {
	var vals []any
	for _, r := range rows {
		if agg.Filter != nil && !agg.Filter(r) {
			continue
		}
		if agg.Func == Count && agg.Column == "" {
			vals = append(vals, true)
			continue
		}
		if v := r[agg.Column]; v != nil {
			vals = append(vals, v)
		}
	}
	if agg.Distinct && !(agg.Func == Count && agg.Column == "") {
		seen := map[string]bool{}
		uniq := vals[:0]
		for _, v := range vals {
			k := fmt.Sprintf("%#v", v)
			if !seen[k] {
				seen[k] = true
				uniq = append(uniq, v)
			}
		}
		vals = uniq
	}
	asF := func(v any) float64 {
		switch t := v.(type) {
		case int:
			return float64(t)
		case int64:
			return float64(t)
		case float64:
			return t
		}
		return math.NaN()
	}
	switch agg.Func {
	case Count:
		return int64(len(vals))
	case Sum:
		if len(vals) == 0 {
			return nil
		}
		s := 0.0
		for _, v := range vals {
			s += asF(v)
		}
		return s
	case Avg:
		if len(vals) == 0 {
			return nil
		}
		s := 0.0
		for _, v := range vals {
			s += asF(v)
		}
		return s / float64(len(vals))
	case Min, Max:
		if len(vals) == 0 {
			return nil
		}
		best := vals[0]
		for _, v := range vals[1:] {
			less := false
			switch b := best.(type) {
			case string:
				less = v.(string) < b
			default:
				less = asF(v) < asF(best)
			}
			if (agg.Func == Min) == less {
				best = v
			}
		}
		return best
	}
	return nil
}

func refGrouping(cols []string, set []string) uint64 {
	in := map[string]bool{}
	for _, c := range set {
		in[c] = true
	}
	var v uint64
	for i, c := range cols {
		if !in[c] {
			v |= 1 << (len(cols) - 1 - i)
		}
	}
	return v
}

func valuesMatch(got, want any) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	asF := func(v any) (float64, bool) {
		switch t := v.(type) {
		case int:
			return float64(t), true
		case int64:
			return float64(t), true
		case float64:
			return t, true
		}
		return 0, false
	}
	if gf, ok := asF(got); ok {
		wf, ok2 := asF(want)
		return ok2 && math.Abs(gf-wf) < 1e-9
	}
	return reflect.DeepEqual(got, want)
}

func TestAggregatesMatchIndependentReference(t *testing.T) {
	// Deterministic dataset with NULLs, duplicates and mixed types.
	var input []Row
	for i := 0; i < 40; i++ {
		r := Row{
			"a": []any{"p", "q", nil}[i%3],
			"b": []any{"u", "v"}[i%2],
			"c": int64(i % 4),
			"d": []any{int64(1), int64(2), nil}[i%3],
			"x": []any{int64(i % 7), nil, int64(i % 7), float64(i%5) + 0.5}[i%4],
			"y": int64((i * 3) % 11),
		}
		input = append(input, r)
	}

	aggs := []Aggregate{
		{Name: "c_all", Func: Count},
		{Name: "c_x", Func: Count, Column: "x"},
		{Name: "c_dx", Func: Count, Column: "x", Distinct: true},
		{Name: "c_dd", Func: Count, Column: "d", Distinct: true},
		{Name: "s_y", Func: Sum, Column: "y"},
		{Name: "s_dy", Func: Sum, Column: "y", Distinct: true},
		{Name: "s_x", Func: Sum, Column: "x"},
		{Name: "a_y", Func: Avg, Column: "y"},
		{Name: "mn_x", Func: Min, Column: "x"},
		{Name: "mx_y", Func: Max, Column: "y"},
		{Name: "f_cnt", Func: Count, Filter: func(r Row) bool {
			y, _ := r["y"].(int64)
			return y > 4
		}},
		{Name: "f_sum", Func: Sum, Column: "y", Filter: func(r Row) bool {
			return r["a"] != nil
		}},
		{Name: "f_avg", Func: Avg, Column: "x", Filter: func(r Row) bool {
			return r["b"] == "u"
		}},
		{Name: "f_cdx", Func: Count, Column: "x", Distinct: true, Filter: func(r Row) bool {
			y, _ := r["y"].(int64)
			return y%2 == 0
		}},
		{Name: "f_max", Func: Max, Column: "y", Filter: func(r Row) bool {
			return r["d"] != nil
		}},
	}
	gcalls := []GroupingCall{
		{Name: "g_abc", Columns: []string{"a", "b", "c"}},
		{Name: "g_d", Columns: []string{"d"}},
	}
	q := Query{
		GroupBy: []Element{
			Rollup(Col("a"), Tuple("b", "c")),
			GroupingSets([]string{"d"}, []string{}),
		},
		Aggregates: aggs,
		Grouping:   gcalls,
	}

	res := Execute(input, q)
	wantSets := Expand(q.GroupBy, false)
	if len(res.Sets) != len(wantSets) {
		t.Fatalf("set count = %d, want %d", len(res.Sets), len(wantSets))
	}

	for si, set := range wantSets {
		sr := res.Sets[si]
		// Reference grouping: first-appearance key order.
		var keys []string
		byKey := map[string][]Row{}
		for _, r := range input {
			k := refKey(r, set)
			if _, ok := byKey[k]; !ok {
				keys = append(keys, k)
			}
			byKey[k] = append(byKey[k], r)
		}
		if len(set) == 0 && len(keys) == 0 {
			keys = append(keys, "")
		}
		if len(sr.Rows) != len(keys) {
			t.Fatalf("set %v: rows = %d, want %d", set, len(sr.Rows), len(keys))
		}
		for gi, k := range keys {
			grows := byKey[k]
			got := sr.Rows[gi]
			// Key columns: real values from the group, synthesized NULL
			// for columns outside the set.
			inSet := map[string]bool{}
			for _, c := range set {
				inSet[c] = true
				if !valuesMatch(got[c], grows[0][c]) {
					t.Errorf("set %v group %d: col %s = %v, want %v",
						set, gi, c, got[c], grows[0][c])
				}
			}
			for _, c := range res.Columns {
				if !inSet[c] && got[c] != nil {
					t.Errorf("set %v group %d: col %s = %v, want synthesized NULL",
						set, gi, c, got[c])
				}
			}
			for _, agg := range aggs {
				want := refAggregate(grows, agg)
				if !valuesMatch(got[agg.Name], want) {
					t.Errorf("set %v group %d: %s = %v, want %v",
						set, gi, agg.Name, got[agg.Name], want)
				}
			}
			for _, gc := range gcalls {
				if got[gc.Name] != refGrouping(gc.Columns, set) {
					t.Errorf("set %v group %d: %s = %v, want %v",
						set, gi, gc.Name, got[gc.Name], refGrouping(gc.Columns, set))
				}
			}
		}
	}
}

func TestCountDistinctIgnoresNull(t *testing.T) {
	input := []Row{
		{"x": nil}, {"x": nil}, {"x": int64(1)}, {"x": int64(1)}, {"x": int64(2)},
	}
	res := Execute(input, Query{
		Aggregates: []Aggregate{
			{Name: "cd", Func: Count, Column: "x", Distinct: true},
			{Name: "c", Func: Count, Column: "x"},
			{Name: "sd", Func: Sum, Column: "x", Distinct: true},
		},
	})
	r := res.Sets[0].Rows[0]
	if r["cd"] != int64(2) {
		t.Fatalf("COUNT(DISTINCT x) = %v, want 2 (NULLs ignored)", r["cd"])
	}
	if r["c"] != int64(3) {
		t.Fatalf("COUNT(x) = %v, want 3", r["c"])
	}
	if r["sd"] != int64(3) {
		t.Fatalf("SUM(DISTINCT x) = %v, want 3", r["sd"])
	}
}
