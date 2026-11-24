package viewopt

import (
	"errors"
	"fmt"
	"sort"
)

// ErrEnumerationLimit is returned when an exhaustive run exceeds its table
// budget.  Increase EnumerateOptions.Limit or use a smaller domain.
var ErrEnumerationLimit = errors.New("exhaustive enumeration limit exceeded")

// Domain is a finite set of rows for exhaustive small-domain checking.  The
// base tables enumerated are all multisets of the Cartesian product of Keys,
// Filters and Measures.
type Domain struct {
	Keys     []Key
	Filters  []int64
	Measures []Value
}

// Tuples returns the distinct rows of the domain in deterministic order.
func (d Domain) Tuples() []Row {
	seen := make(map[Row]bool)
	var out []Row
	for _, k := range d.Keys {
		for _, f := range d.Filters {
			for _, m := range d.Measures {
				r := Row{Key: k, Filter: f, Measure: m}
				if !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Compare(out[j]) < 0 })
	return out
}

// EnumerateOptions controls exhaustive checking.
type EnumerateOptions struct {
	// MaxMultiplicity is the maximum multiplicity of one distinct tuple.
	MaxMultiplicity int
	// MaxTotalRows is the maximum number of rows in an enumerated table.
	MaxTotalRows int
	// Limit caps the number of tables examined.  <= 0 means 1,000,000.
	Limit int
}

func (o EnumerateOptions) normalized() EnumerateOptions {
	if o.MaxMultiplicity <= 0 {
		o.MaxMultiplicity = 2
	}
	if o.MaxTotalRows <= 0 {
		o.MaxTotalRows = 6
	}
	if o.Limit <= 0 {
		o.Limit = 1_000_000
	}
	return o
}

// ExhaustiveVerify enumerates every small base table (up to MaxTotalRows and
// MaxMultiplicity) and compares the direct interpreter with the plan
// interpreter, including HAVING and group presence.
//
// verified is true when no differing table was found.  Because rows are
// enumerated by increasing row count and then lexicographically, a returned
// witness is minimal within the enumerated bounds.
func ExhaustiveVerify(q Query, views []View, p Plan, domain Domain, opt EnumerateOptions) (
	verified bool, witness *Counterexample, err error,
) {
	opt = opt.normalized()
	if err := validateDims(q.Dims); err != nil {
		return false, nil, fmt.Errorf("query: %w", err)
	}
	for i, v := range views {
		if err := validateDims(v.Dims); err != nil {
			return false, nil, fmt.Errorf("view %d: %w", i, err)
		}
		if v.ReadCost < 0 {
			return false, nil, fmt.Errorf("view %d: negative read cost", i)
		}
	}
	for _, term := range p.Normalize().Terms {
		if term.View < 0 || term.View >= len(views) {
			return false, nil, fmt.Errorf("plan references view %d, but there are %d views", term.View, len(views))
		}
	}
	tuples := domain.Tuples()
	if len(tuples) == 0 {
		return true, nil, nil
	}

	examined := 0
	for total := 0; total <= opt.MaxTotalRows; total++ {
		counts := make([]int, len(tuples))
		var best Table
		var enumErr error
		var rec func(pos, remaining int)
		rec = func(pos, remaining int) {
			if enumErr != nil {
				return
			}
			if pos == len(tuples) {
				if remaining != 0 {
					return
				}
				examined++
				if examined > opt.Limit {
					enumErr = ErrEnumerationLimit
					return
				}
				t := make(Table, 0, total)
				for i, c := range counts {
					for j := 0; j < c; j++ {
						t = append(t, tuples[i])
					}
				}
				if tablesDiffer(q, views, p, t) {
					if best == nil || CompareTables(t, best) < 0 {
						best = append(Table(nil), t...)
					}
				}
				return
			}
			max := opt.MaxMultiplicity
			if remaining < max {
				max = remaining
			}
			for c := 0; c <= max; c++ {
				counts[pos] = c
				rec(pos+1, remaining-c)
				if enumErr != nil {
					return
				}
			}
			counts[pos] = 0
		}
		rec(0, total)
		if enumErr != nil {
			return false, nil, enumErr
		}
		if best != nil {
			return false, &Counterexample{Table: best.Sorted(), Reason: "exhaustive small-domain comparison"}, nil
		}
	}
	return true, nil, nil
}
