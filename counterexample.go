package viewopt

import (
	"fmt"
	"sort"
)

// minimalCounterexample constructs the smallest table that distinguishes the
// plan from the direct query.  Coverage/structural failures are row-local, so
// a one-row witness normally exists.  A short two-row search is used when a
// HAVING predicate filters out every one-row witness.  If HAVING masks even
// that, the pre-HAVING witness is returned with PreHaving=true.
func minimalCounterexample(q Query, views []View, p Plan, opt Options, vios []violation) *Counterexample {
	if len(vios) == 0 || opt.Domain.IsEmpty() || opt.MaxMultiplicity == 0 {
		return nil
	}

	keys := counterKeys(q, vios)
	filters := counterFilters(q, opt.Domain, vios)
	// The package's default encoding assumption is a non-negative value
	// range (see Options.Domain); NULL sorts before every non-null value.
	// A few larger probes let a HAVING SUM/AVG threshold be exercised without
	// making the common count/presence witness non-minimal.
	measures := []Value{Null(), Int(0), Int(1), Int(2), Int(10), Int(100)}

	var rows []Row
	for _, k := range keys {
		for _, f := range filters {
			for _, m := range measures {
				rows = append(rows, Row{Key: k, Filter: f, Measure: m})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Compare(rows[j]) < 0 })
	rows = dedupeRows(rows)

	reason := vios[0].reason
	var best Table
	consider := func(t Table) {
		if !feasibleMultiplicity(t, opt.MaxMultiplicity) {
			return
		}
		if !tablesDiffer(q, views, p, t) {
			return
		}
		if best == nil || CompareTables(t, best) < 0 {
			best = t.Sorted()
		}
	}

	maxRows := opt.MaxTotalRows
	if maxRows > 64 {
		maxRows = 64
	}
	for n := 1; n <= maxRows; n++ {
		// Repeating one tuple is enough to cross a COUNT/SUM/AVG HAVING
		// threshold in the common case.
		if n <= opt.MaxMultiplicity {
			for _, r := range rows {
				t := make(Table, n)
				for i := range t {
					t[i] = r
				}
				consider(t)
			}
		}
		// For two rows also try two distinct tuples, since a HAVING
		// predicate may compare two groups or need two filter values.
		if n == 2 {
			pairRows := rows
			if len(pairRows) > 48 {
				pairRows = pairRows[:48]
			}
			for i := 0; i < len(pairRows); i++ {
				for j := i + 1; j < len(pairRows); j++ {
					consider(Table{pairRows[i], pairRows[j]})
				}
			}
		}
		if best != nil {
			return &Counterexample{Table: best, Reason: reason}
		}
	}

	// Fall back to a symbolic (pre-HAVING) witness.  It is still a valid
	// counterexample to aggregate-relation equivalence and therefore to
	// equivalence for every HAVING clause when the plan is accepted only under
	// the package's conservative guarantee.
	row, ok := fallbackWitness(q, opt.Domain, vios)
	if !ok {
		return nil
	}
	return &Counterexample{Table: Table{row}, Reason: reason, PreHaving: true}
}

func counterKeys(q Query, vios []violation) []Key {
	seen := make(map[Key]bool)
	var out []Key
	add := func(k Key) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	add(K())
	for d := 0; d < 3; d++ {
		var k Key
		for i := range k {
			k[i] = Null()
		}
		k[d] = Int(0)
		add(k)
		k[d] = Int(1)
		add(k)
	}
	add(K(Int(0), Int(0), Int(0)))
	for _, v := range vios {
		if v.kind == violationStructural && v.dim >= 0 && v.dim < 3 {
			var k Key
			for i := range k {
				k[i] = Null()
			}
			k[v.dim] = Int(0)
			add(k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Compare(out[j]) < 0 })
	return out
}

func counterFilters(q Query, domain Interval, vios []violation) []int64 {
	seen := make(map[int64]bool)
	var out []int64
	add := func(f int64) {
		if !domain.Contains(f) || seen[f] {
			return
		}
		seen[f] = true
		out = append(out, f)
	}
	for _, v := range vios {
		if v.hasCell {
			add(v.cell.minValue())
		}
	}
	if len(out) == 0 {
		if in := q.Filter.Intersect(domain); !in.IsEmpty() {
			if in.HasLo {
				add(in.Lo)
			} else if in.HasHi {
				add(in.Hi)
			} else {
				add(0)
			}
		}
	}
	if len(out) == 0 {
		if domain.HasLo {
			add(domain.Lo)
		} else if domain.HasHi {
			add(domain.Hi)
		} else {
			add(0)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func fallbackWitness(q Query, domain Interval, vios []violation) (Row, bool) {
	for _, v := range vios {
		var k Key
		for i := range k {
			k[i] = Null()
		}
		if v.kind == violationStructural && v.dim >= 0 && v.dim < 3 {
			k[v.dim] = Int(0)
		}
		var f int64
		ok := false
		if v.hasCell {
			f = v.cell.minValue()
			ok = domain.Contains(f)
		}
		if !ok {
			in := q.Filter.Intersect(domain)
			if in.IsEmpty() {
				continue
			}
			if in.HasLo {
				f = in.Lo
			} else if in.HasHi {
				f = in.Hi
			} else {
				f = 0
			}
			ok = true
		}
		if ok {
			return Row{Key: k, Filter: f, Measure: Null()}, true
		}
	}
	return Row{}, false
}

func tablesDiffer(q Query, views []View, p Plan, t Table) bool {
	a, err := EvaluateQuery(q, t)
	if err != nil {
		return false
	}
	b, err := EvaluatePlan(q, views, p, t)
	if err != nil {
		return false
	}
	return !a.Equal(b)
}

func feasibleMultiplicity(t Table, max int) bool {
	if max < 0 {
		return false
	}
	counts := make(map[Row]int)
	for _, r := range t {
		counts[r]++
		if counts[r] > max {
			return false
		}
	}
	return true
}

func dedupeRows(rows []Row) []Row {
	out := rows[:0]
	for i, r := range rows {
		if i == 0 || out[len(out)-1] != r {
			out = append(out, r)
		}
	}
	return out
}

func (v violation) String() string {
	switch v.kind {
	case violationCoverage:
		return fmt.Sprintf("coverage at %s: got %d want %d", intervalCellString(v.cell), v.got, v.want)
	case violationStructural:
		return fmt.Sprintf("structural: view %d missing dim %d", v.view, v.dim)
	case violationPhantom:
		return fmt.Sprintf("phantom empty group at %s", intervalCellString(v.cell))
	default:
		return "unknown violation"
	}
}
