package grouping

import "strings"

// Row is one input or output record. A missing key and a nil value both
// denote SQL NULL.
type Row map[string]any

// AggFunc identifies an aggregate function.
type AggFunc int

const (
	Count AggFunc = iota
	Sum
	Avg
	Min
	Max
)

// Aggregate is one aggregate call of the SELECT list. Column is empty
// for COUNT(*). Distinct removes duplicate non-NULL inputs before
// aggregation. Filter, when non-nil, restricts the rows the aggregate
// sees (FILTER (WHERE cond)); rows failing the predicate are skipped
// for this aggregate only.
type Aggregate struct {
	Name     string
	Func     AggFunc
	Column   string
	Distinct bool
	Filter   func(Row) bool
}

// GroupingCall is one GROUPING(c1, ..., ck) bitmap function of the
// SELECT list. At most 64 columns may be listed.
type GroupingCall struct {
	Name    string
	Columns []string
}

// Query describes one GROUP BY evaluation.
type Query struct {
	// GroupBy is the comma-separated grouping element list; an empty
	// list means GROUP BY () (a single grand-total group).
	GroupBy []Element
	// DistinctSets selects GROUP BY DISTINCT; the zero value is
	// GROUP BY ALL, which preserves duplicate grouping sets.
	DistinctSets bool
	Aggregates   []Aggregate
	Grouping     []GroupingCall
}

// SetResult holds the output rows produced for one expanded grouping
// set, in first-appearance order of the group keys.
type SetResult struct {
	Set  []string
	Rows []Row
}

// Result is the outcome of Execute. Columns lists every grouping
// column of the clause in canonical order; output rows carry all of
// them, with nil for columns absent from the row's grouping set
// (synthesized NULLs), followed by the aggregate and GROUPING outputs
// under their declared names.
type Result struct {
	Columns []string
	Sets    []SetResult
}

// AllRows flattens the per-set rows in expansion order.
func (r Result) AllRows() []Row {
	var out []Row
	for _, s := range r.Sets {
		out = append(out, s.Rows...)
	}
	return out
}

// Execute runs the query over the input rows.
func Execute(input []Row, q Query) Result {
	sets := Expand(q.GroupBy, q.DistinctSets)
	cols := canonicalCols(sets)
	res := Result{Columns: cols}
	for _, set := range sets {
		res.Sets = append(res.Sets, executeSet(input, set, cols, q))
	}
	return res
}

func executeSet(input []Row, set, cols []string, q Query) SetResult {
	sr := SetResult{Set: set}
	var order []string
	groups := map[string][]Row{}
	keyVals := map[string][]any{}
	for _, row := range input {
		vals := make([]any, len(set))
		parts := make([]string, len(set))
		for i, c := range set {
			vals[i] = row[c]
			parts[i] = valueKey(row[c])
		}
		key := strings.Join(parts, "\x1f")
		if _, ok := groups[key]; !ok {
			order = append(order, key)
			keyVals[key] = vals
		}
		groups[key] = append(groups[key], row)
	}
	// The empty grouping set () always yields exactly one group, even
	// when the input table is empty.
	if len(set) == 0 && len(order) == 0 {
		order = append(order, "")
		groups[""] = nil
		keyVals[""] = []any{}
	}
	for _, key := range order {
		grows := groups[key]
		out := Row{}
		for _, c := range cols {
			out[c] = nil
		}
		for i, c := range set {
			out[c] = keyVals[key][i]
		}
		for _, agg := range q.Aggregates {
			out[agg.Name] = computeAggregate(grows, agg)
		}
		for _, gc := range q.Grouping {
			out[gc.Name] = GroupingBitmap(gc.Columns, set)
		}
		sr.Rows = append(sr.Rows, out)
	}
	return sr
}

// GroupingBitmap evaluates GROUPING(c1, ..., ck) for one grouping set:
// bit i (most significant first) is 1 iff column ci is absent from the
// set, 0 iff it participates in grouping.
func GroupingBitmap(cols []string, set []string) uint64 {
	member := make(map[string]bool, len(set))
	for _, c := range set {
		member[c] = true
	}
	var v uint64
	for i, c := range cols {
		if !member[c] {
			v |= 1 << (len(cols) - 1 - i)
		}
	}
	return v
}

func computeAggregate(rows []Row, agg Aggregate) any {
	var vals []any
	for _, r := range rows {
		if agg.Filter != nil && !agg.Filter(r) {
			continue
		}
		if agg.Func == Count && agg.Column == "" {
			vals = append(vals, true)
			continue
		}
		v := r[agg.Column]
		if v == nil {
			continue
		}
		vals = append(vals, v)
	}
	if agg.Distinct && !(agg.Func == Count && agg.Column == "") {
		vals = dedupValues(vals)
	}
	switch agg.Func {
	case Count:
		return int64(len(vals))
	case Sum:
		return sumValues(vals)
	case Avg:
		if len(vals) == 0 {
			return nil
		}
		total, n := 0.0, 0
		for _, v := range vals {
			if f, _, ok := toNumber(v); ok {
				total += f
				n++
			}
		}
		if n == 0 {
			return nil
		}
		return total / float64(n)
	case Min, Max:
		var best any
		for _, v := range vals {
			if best == nil {
				best = v
				continue
			}
			c := compareValues(v, best)
			if (agg.Func == Min && c < 0) || (agg.Func == Max && c > 0) {
				best = v
			}
		}
		return best
	}
	return nil
}

// sumValues sums numeric inputs, yielding int64 when every input is an
// integer and float64 as soon as any float appears; NULL-only or empty
// input yields nil.
func sumValues(vals []any) any {
	var isum int64
	var fsum float64
	allInt := true
	n := 0
	for _, v := range vals {
		f, isInt, ok := toNumber(v)
		if !ok {
			continue
		}
		n++
		switch {
		case isInt && allInt:
			isum += int64(f)
		case allInt:
			allInt = false
			fsum = float64(isum) + f
		default:
			fsum += f
		}
	}
	if n == 0 {
		return nil
	}
	if allInt {
		return isum
	}
	return fsum
}

func dedupValues(vals []any) []any {
	seen := make(map[string]bool, len(vals))
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		k := valueKey(v)
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}

// canonicalCols is the union of all grouping sets in first-appearance
// order.
func canonicalCols(sets [][]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, set := range sets {
		for _, c := range set {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}
