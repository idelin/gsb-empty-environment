package viewopt

import (
	"fmt"
	"sort"
	"strings"
)

type violationKind int

const (
	violationCoverage violationKind = iota
	violationStructural
	violationPhantom
)

type violation struct {
	kind    violationKind
	cell    cell
	hasCell bool
	view    int
	dim     int
	want    int
	got     int
	reason  string
}

// Verification is the result of VerifyPlan.
type Verification struct {
	Equivalent     bool
	Counterexample *Counterexample
	Reason         string
}

// VerifyPlan checks whether a plan is equivalent to the direct query for every
// base multiset (over the configured filter domain), and if not, constructs a
// minimal counterexample table.
//
// A plan is accepted only when:
//   - every used view groups by a superset of the query's grouping dimensions;
//   - on every filter cell, the sum of view coefficients equals 1 inside the
//     query and 0 outside it (so COUNT/SUM/COUNT-non-null are exact);
//   - a view touching a region outside the query does not create a visible
//     extra empty group.  Such a group has COUNT=0, COUNT(non-null)=0 and
//     SUM=NULL; it is harmless only when HAVING rejects that state.
//
// These conditions are independent of the actual key and measure values, so
// they prove equivalence for every underlying multiset, not merely for a
// finite sample.
func VerifyPlan(q Query, views []View, p Plan, opt Options) (Verification, error) {
	opt = opt.normalized()
	if err := validateDims(q.Dims); err != nil {
		return Verification{}, fmt.Errorf("query: %w", err)
	}
	if opt.Domain.IsEmpty() {
		return Verification{Equivalent: true}, nil
	}
	vios, err := analyzePlan(q, views, p, opt)
	if err != nil {
		return Verification{}, err
	}
	if len(vios) == 0 {
		return Verification{Equivalent: true}, nil
	}
	reason := vios[0].reason
	ce := minimalCounterexample(q, views, p, opt, vios)
	return Verification{
		Equivalent:     false,
		Counterexample: ce,
		Reason:         reason,
	}, nil
}

func analyzePlan(q Query, views []View, p Plan, opt Options) ([]violation, error) {
	p = p.Normalize()
	if len(p.Terms) > 0 {
		// Validate all plans terms, including unused views referenced directly.
		for _, t := range p.Terms {
			if t.View < 0 || t.View >= len(views) {
				return nil, fmt.Errorf("plan references view %d, but there are %d views", t.View, len(views))
			}
			if err := validateDims(views[t.View].Dims); err != nil {
				return nil, fmt.Errorf("view %d: %w", t.View, err)
			}
			if views[t.View].ReadCost < 0 {
				return nil, fmt.Errorf("view %d has negative read cost", t.View)
			}
		}
	}

	var vios []violation
	domain := opt.Domain
	filters := make([]Interval, 0, len(p.Terms))
	termByPos := make([]PlanTerm, 0, len(p.Terms))
	for _, t := range p.Terms {
		filters = append(filters, views[t.View].Filter)
		termByPos = append(termByPos, t)
	}
	cells := partitionCells(domain, append([]Interval{q.Filter}, filters...))
	if len(cells) == 0 {
		return vios, nil
	}

	// A coarse view cannot generally be combined at the query grain: its row
	// aggregates several finer keys.  Reject it whenever it can materialize
	// any row in the domain, even outside the query, because those coarse
	// rows are not necessarily the all-zero phantom state.
	for _, t := range termByPos {
		v := views[t.View]
		if dimsCover(v.Dims, q.Dims) {
			continue
		}
		if v.Filter.Intersect(domain).IsEmpty() {
			continue
		}
		var chosen cell
		found := false
		for _, c := range cells {
			if !v.Filter.Contains(c.sample()) {
				continue
			}
			if !found || !q.Filter.Contains(c.sample()) {
				chosen = c
				found = true
			}
			if !q.Filter.Contains(c.sample()) {
				break
			}
		}
		d, _ := firstMissingDim(v.Dims, q.Dims)
		vios = append(vios, violation{
			kind:    violationStructural,
			cell:    chosen,
			hasCell: found,
			view:    t.View,
			dim:     d,
			reason:  fmt.Sprintf("view %d (%s) does not group by query dimension %d", t.View, v.Name, d),
		})
	}

	emptyVisible := q.Accept(EmptyAgg())
	for _, c := range cells {
		f := c.sample()
		want := 0
		if q.Filter.Contains(f) {
			want = 1
		}
		got := 0
		touching := false
		for i, t := range termByPos {
			if filters[i].Contains(f) {
				got += t.Coeff
				touching = true
			}
		}
		if got != want {
			vios = append(vios, violation{
				kind:    violationCoverage,
				cell:    c,
				hasCell: true,
				want:    want,
				got:     got,
				reason:  fmt.Sprintf("filter cell %s has coefficient sum %d, want %d", intervalCellString(c), got, want),
			})
			continue
		}
		if want == 0 && touching && emptyVisible {
			vios = append(vios, violation{
				kind:    violationPhantom,
				cell:    c,
				hasCell: true,
				want:    0,
				got:     0,
				reason:  fmt.Sprintf("views touch non-query filter cell %s and produce an extra empty group", intervalCellString(c)),
			})
		}
	}
	return vios, nil
}

func intervalCellString(c cell) string {
	lo, hi := "-inf", "+inf"
	if c.hasLo {
		lo = fmt.Sprintf("%d", c.lo)
	}
	if c.hasHi {
		hi = fmt.Sprintf("%d", c.hi)
	}
	return "[" + lo + "," + hi + "]"
}

// coverageInput is the symbolic interval system used by the optimizer.
type coverageInput struct {
	domain  Interval
	cells   []cell
	want    []int8
	masks   [][]bool
	outside []bool
	viewIdx []int
}

func buildCoverageInput(q Query, views []View, viewIdx []int, opt Options) coverageInput {
	filters := make([]Interval, len(viewIdx))
	for i, idx := range viewIdx {
		filters[i] = views[idx].Filter
	}
	all := append([]Interval{q.Filter}, filters...)
	cells := partitionCells(opt.Domain, all)
	in := coverageInput{
		domain:  opt.Domain,
		cells:   cells,
		want:    make([]int8, len(cells)),
		masks:   make([][]bool, len(viewIdx)),
		outside: make([]bool, len(viewIdx)),
		viewIdx: append([]int(nil), viewIdx...),
	}
	for i, c := range cells {
		f := c.sample()
		if q.Filter.Contains(f) {
			in.want[i] = 1
		}
	}
	for j := range viewIdx {
		in.masks[j] = make([]bool, len(cells))
		for i, c := range cells {
			if filters[j].Contains(c.sample()) {
				in.masks[j][i] = true
				if in.want[i] == 0 {
					in.outside[j] = true
				}
			}
		}
	}
	return in
}

func (in coverageInput) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "domain=%s cells=%d want=%v views=%v", in.domain, len(in.cells), in.want, in.viewIdx)
	return b.String()
}

func sortedViewIndices(views []View) ([]int, error) {
	out := make([]int, 0, len(views))
	for i, v := range views {
		if err := validateDims(v.Dims); err != nil {
			return nil, fmt.Errorf("view %d: %w", i, err)
		}
		if v.ReadCost < 0 {
			return nil, fmt.Errorf("view %d has negative read cost", i)
		}
		out = append(out, i)
	}
	sort.Ints(out)
	return out, nil
}
