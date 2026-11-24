package viewopt

import "fmt"

// projectBaseKey projects a base key onto the grouping projection dims.  The
// result uses positions 0..len(dims)-1; unused components are NULL.
func projectBaseKey(k Key, dims []int) Key {
	var out Key
	for i := range out {
		out[i] = Null()
	}
	for j, d := range dims {
		if j >= 3 || d < 0 || d >= 3 {
			continue
		}
		out[j] = k[d]
	}
	return out
}

// projectViewKey maps a view-group key to a query-group key.  If the view does
// not group by a query dimension, that target component becomes NULL.  The
// optimizer rejects such plans, but this deterministic behaviour is useful for
// diagnosing a user-supplied plan.
func projectViewKey(vk Key, viewDims, queryDims []int) Key {
	var out Key
	for i := range out {
		out[i] = Null()
	}
	pos := [3]int{-1, -1, -1}
	for p, d := range viewDims {
		if p < 3 && d >= 0 && d < 3 {
			pos[d] = p
		}
	}
	for j, d := range queryDims {
		if j >= 3 || d < 0 || d >= 3 {
			continue
		}
		if p := pos[d]; p >= 0 {
			out[j] = vk[p]
		}
	}
	return out
}

type aggAcc struct {
	count        int64
	countNonNull int64
	sum          int64
}

func (a *aggAcc) add(m Value) {
	a.count++
	if !m.Null {
		a.countNonNull++
		a.sum += m.V
	}
}

func (a aggAcc) result() Agg { return finalizeAgg(a.count, a.countNonNull, a.sum) }

// MaterializeView computes a view from a base table.  Groups are keyed by the
// view's projection; dims not used by the projection are NULL.  Views contain
// only groups that actually have at least one source row.
func MaterializeView(v View, t Table) (map[Key]Agg, error) {
	if err := validateDims(v.Dims); err != nil {
		return nil, fmt.Errorf("view %q: %w", v.Name, err)
	}
	if v.ReadCost < 0 {
		return nil, fmt.Errorf("view %q: negative read cost", v.Name)
	}
	acc := make(map[Key]*aggAcc)
	for _, row := range t {
		if !v.Filter.Contains(row.Filter) {
			continue
		}
		k := projectBaseKey(row.Key, v.Dims)
		a := acc[k]
		if a == nil {
			a = &aggAcc{}
			acc[k] = a
		}
		a.add(row.Measure)
	}
	out := make(map[Key]Agg, len(acc))
	for k, a := range acc {
		out[k] = a.result()
	}
	return out, nil
}

// EvaluateQuery is an independent direct interpreter for the query.  It
// materializes only groups that have at least one matching source row and then
// applies HAVING.
func EvaluateQuery(q Query, t Table) (Result, error) {
	if err := validateDims(q.Dims); err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	acc := make(map[Key]*aggAcc)
	for _, row := range t {
		if !q.Filter.Contains(row.Filter) {
			continue
		}
		k := projectBaseKey(row.Key, q.Dims)
		a := acc[k]
		if a == nil {
			a = &aggAcc{}
			acc[k] = a
		}
		a.add(row.Measure)
	}
	out := make(Result, len(acc))
	for k, a := range acc {
		res := a.result()
		if q.Accept(res) {
			out[k] = res
		}
	}
	return out, nil
}

// EvaluatePlan executes a linear view plan and then applies the query's
// HAVING clause.  Every key occurring in any selected view's rows is retained
// in the merged relation, even if its coefficient-weighted COUNT cancels to
// zero.  That is how an "extra empty group" can be observed.  SUM remains NULL
// when the weighted non-null count is zero; it is never replaced by 0.
func EvaluatePlan(q Query, views []View, p Plan, t Table) (Result, error) {
	if err := validateDims(q.Dims); err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	p = p.Normalize()
	cache := make(map[int]map[Key]Agg)
	type merged struct {
		count        int64
		countNonNull int64
		sum          int64
	}
	acc := make(map[Key]*merged)

	for _, term := range p.Terms {
		if term.View < 0 || term.View >= len(views) {
			return nil, fmt.Errorf("plan references view %d, but there are %d views", term.View, len(views))
		}
		v := views[term.View]
		if err := validateDims(v.Dims); err != nil {
			return nil, fmt.Errorf("view %d: %w", term.View, err)
		}
		data, ok := cache[term.View]
		if !ok {
			var err error
			data, err = MaterializeView(v, t)
			if err != nil {
				return nil, err
			}
			cache[term.View] = data
		}
		c := int64(term.Coeff)
		for vk, a := range data {
			k := projectViewKey(vk, v.Dims, q.Dims)
			m := acc[k]
			if m == nil {
				m = &merged{}
				acc[k] = m
			}
			m.count += c * a.Count
			m.countNonNull += c * a.CountNonNull
			if !a.Sum.Null {
				m.sum += c * a.Sum.V
			}
		}
	}

	out := make(Result, len(acc))
	for k, m := range acc {
		res := finalizeAgg(m.count, m.countNonNull, m.sum)
		if q.Accept(res) {
			out[k] = res
		}
	}
	return out, nil
}
