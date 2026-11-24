package viewopt

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Agg is the result of the three linear aggregates retained by every view:
// COUNT(*), COUNT(measure), and SUM(measure).
//
// Sum is NULL exactly when CountNonNull is zero.  In particular, an empty
// input (or an input whose measures are all NULL) has Sum == NULL, never 0.
type Agg struct {
	Count        int64
	CountNonNull int64
	Sum          Value
}

// EmptyAgg is the aggregate state of a group with no rows.
func EmptyAgg() Agg {
	return Agg{Count: 0, CountNonNull: 0, Sum: Null()}
}

func finalizeAgg(count, countNonNull, sum int64) Agg {
	a := Agg{Count: count, CountNonNull: countNonNull, Sum: Null()}
	if countNonNull != 0 {
		a.Sum = Int(sum)
	}
	return a
}

// Avg derives AVG(measure) from SUM and COUNT(measure).  AVG is deliberately
// not stored as an additive field: it is a ratio, not a linear aggregate, and
// must never be added across views.  The boolean is false for an empty/all-NULL
// input, where SQL AVG is NULL.
func (a Agg) Avg() (float64, bool) {
	if a.CountNonNull == 0 {
		return 0, false
	}
	if a.Sum.Null {
		return 0, false
	}
	return float64(a.Sum.V) / float64(a.CountNonNull), true
}

// Equal reports equality of the retained aggregate state.  AVG is derived and
// therefore does not need a separate comparison.
func (a Agg) Equal(b Agg) bool {
	if a.Count != b.Count || a.CountNonNull != b.CountNonNull {
		return false
	}
	if a.Sum.Null != b.Sum.Null {
		return false
	}
	return a.Sum.Null || a.Sum.V == b.Sum.V
}

func (a Agg) String() string {
	avg := "NULL"
	if v, ok := a.Avg(); ok {
		avg = fmt.Sprintf("%g", v)
	}
	return fmt.Sprintf("{count=%d count_nonnull=%d sum=%s avg=%s}",
		a.Count, a.CountNonNull, a.Sum, avg)
}

// Result is a grouped aggregate relation.  A key is present iff the plan or
// query produces a row for it.  In particular, a perfectly cancelling plan can
// leave a key present with Count == 0; that is an *empty group* and is not the
// same as an absent group.
type Result map[Key]Agg

// ResultRow is a sorted result row.
type ResultRow struct {
	Key Key
	Agg Agg
}

// Rows returns result rows in deterministic key order.
func (r Result) Rows() []ResultRow {
	out := make([]ResultRow, 0, len(r))
	for k, a := range r {
		out = append(out, ResultRow{Key: k, Agg: a})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.Compare(out[j].Key) < 0 })
	return out
}

// Equal compares two grouped relations, including group presence.
func (r Result) Equal(o Result) bool {
	if len(r) != len(o) {
		return false
	}
	for k, a := range r {
		b, ok := o[k]
		if !ok || !a.Equal(b) {
			return false
		}
	}
	return true
}

func (r Result) String() string {
	rows := r.Rows()
	out := "{"
	for i, row := range rows {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s:%s", row.Key, row.Agg)
	}
	return out + "}"
}

// View describes one materialized view.
//
// Dims is the grouping-key projection, using base-key dimension indices
// 0..2.  A view groups rows whose Filter lies in Filter, and retains
// SUM(Measure), COUNT(*) and COUNT(Measure).  ReadCost is the cost of one read
// of this view.  By default each distinct used view is charged once; see
// Options.WeightCostByCoefficient for a repeated-scan cost model.
type View struct {
	Name     string
	Dims     []int
	Filter   Interval
	ReadCost int64
}

// Query is a direct GROUP BY query over the base table.  Dims is the grouping
// projection.  Having is applied to every produced group (nil means no
// HAVING clause).  AVG can be used inside Having through Agg.Avg().
type Query struct {
	Dims   []int
	Filter Interval
	Having func(Agg) bool
}

// Accept applies the HAVING predicate.
func (q Query) Accept(a Agg) bool {
	return q.Having == nil || q.Having(a)
}

// PlanTerm adds Coeff copies of view View.  Coeff may be negative; a negative
// coefficient is a subtraction of the corresponding view rows.
type PlanTerm struct {
	View  int
	Coeff int
}

// Plan is an alternative plan: a linear combination of materialized view rows,
// followed by projection/merge to the query grouping.  AVG is not a plan term.
type Plan struct {
	Terms []PlanTerm
}

// Normalize combines terms for the same view, removes zero coefficients, and
// sorts terms by view index.
func (p Plan) Normalize() Plan {
	coeff := make(map[int]int)
	for _, t := range p.Terms {
		if t.Coeff != 0 {
			coeff[t.View] += t.Coeff
		}
	}
	out := Plan{}
	for idx, c := range coeff {
		if c != 0 {
			out.Terms = append(out.Terms, PlanTerm{View: idx, Coeff: c})
		}
	}
	sort.Slice(out.Terms, func(i, j int) bool { return out.Terms[i].View < out.Terms[j].View })
	return out
}

// Cost computes the read cost of the plan using the default model: every
// distinct view with a nonzero coefficient is read once, regardless of the
// coefficient's magnitude.
func (p Plan) Cost(views []View) (int64, error) {
	return p.cost(views, false)
}

// CostWeighted computes sum |coeff| * view.ReadCost.  Use it when a
// coefficient is intended to model repeated scans of the same materialization.
func (p Plan) CostWeighted(views []View) (int64, error) {
	return p.cost(views, true)
}

func (p Plan) cost(views []View, weightByCoeff bool) (int64, error) {
	p = p.Normalize()
	var total int64
	for _, t := range p.Terms {
		if t.View < 0 || t.View >= len(views) {
			return 0, fmt.Errorf("plan references view %d, but there are %d views", t.View, len(views))
		}
		if views[t.View].ReadCost < 0 {
			return 0, fmt.Errorf("view %d has negative read cost %d", t.View, views[t.View].ReadCost)
		}
		units := int64(1)
		if weightByCoeff {
			ac := t.Coeff
			if ac < 0 {
				ac = -ac
			}
			units = int64(ac)
		}
		add := units * views[t.View].ReadCost
		if total > math.MaxInt64-add {
			return 0, errors.New("plan cost overflows int64")
		}
		total += add
	}
	return total, nil
}

// Options controls optimization and counterexample search.
type Options struct {
	// MaxAbsCoeff bounds every plan coefficient to [-MaxAbsCoeff,
	// MaxAbsCoeff].  Values <= 0 mean the default (2).
	MaxAbsCoeff int

	// Domain is the set of filter coordinates that can occur in a base
	// table.  The zero value means the whole int64 line.  DefaultOptions
	// uses [0, 2^40].
	Domain Interval

	// MaxMultiplicity bounds the multiplicity of each distinct tuple in a
	// counterexample table.  Values <= 0 mean the default (2).
	MaxMultiplicity int

	// MaxTotalRows bounds multi-row counterexample search.  Values <= 0
	// mean the default (6).
	MaxTotalRows int

	// WeightCostByCoefficient changes the read-cost model from "one read
	// per distinct used view" (the default) to
	// sum |coefficient| * ReadCost.
	WeightCostByCoefficient bool
}

// DefaultOptions returns the options used by the package examples: small
// coefficients, a 2^40 encoded value domain, multiplicity at most 2, and at
// most six rows in an exhaustive counterexample search.
func DefaultOptions() Options {
	return Options{
		MaxAbsCoeff:     2,
		Domain:          Between(0, int64(1)<<40),
		MaxMultiplicity: 2,
		MaxTotalRows:    6,
	}
}

func (o Options) normalized() Options {
	if o.MaxAbsCoeff <= 0 {
		o.MaxAbsCoeff = 2
	}
	if o.MaxMultiplicity <= 0 {
		o.MaxMultiplicity = 2
	}
	if o.MaxTotalRows <= 0 {
		o.MaxTotalRows = 6
	}
	return o
}

// Problem is an optimization request.
type Problem struct {
	Views   []View
	Query   Query
	Options Options
}

// Counterexample is a base table on which a plan and the direct query differ.
// Table is sorted; when PreHaving is true the difference is in the grouped
// aggregate relation before HAVING (some HAVING clause may mask it).
type Counterexample struct {
	Table     Table
	Reason    string
	PreHaving bool
}

// Solution is the result of Optimize.
//
// If Equivalent is true, Plan is a minimum-read-cost equivalent plan and
// Counterexample is nil.  Otherwise Plan and Candidate are both the best
// coverage-correct plan found (or the empty plan), and Counterexample explains
// why at least that candidate fails.
type Solution struct {
	Equivalent     bool
	Plan           Plan
	Cost           int64
	Candidate      Plan
	CandidateCost  int64
	Counterexample *Counterexample
	Reason         string
}

func validateDims(dims []int) error {
	if len(dims) > 3 {
		return fmt.Errorf("grouping dimension count %d exceeds 3", len(dims))
	}
	seen := [3]bool{}
	for _, d := range dims {
		if d < 0 || d >= 3 {
			return fmt.Errorf("grouping dimension %d outside 0..2", d)
		}
		if seen[d] {
			return fmt.Errorf("duplicate grouping dimension %d", d)
		}
		seen[d] = true
	}
	return nil
}

func dimsCover(viewDims, queryDims []int) bool {
	have := [3]bool{}
	for _, d := range viewDims {
		if d >= 0 && d < 3 {
			have[d] = true
		}
	}
	for _, d := range queryDims {
		if d < 0 || d >= 3 || !have[d] {
			return false
		}
	}
	return true
}

func firstMissingDim(viewDims, queryDims []int) (int, bool) {
	have := [3]bool{}
	for _, d := range viewDims {
		if d >= 0 && d < 3 {
			have[d] = true
		}
	}
	for _, d := range queryDims {
		if d < 0 || d >= 3 || !have[d] {
			return d, true
		}
	}
	return 0, false
}
