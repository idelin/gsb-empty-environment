package engine

import "sort"

// Reference is an independent reference implementation used for
// differential testing. It keeps only the current formulas and answers
// every query by evaluating from scratch, with no caching across
// queries. Like the incremental engine, it only evaluates the selected
// IF branch.
//
// Semantics of a query Get(id): the cells reachable from id are
// evaluated; every cell sitting on a genuine cycle of actually-read
// dependencies evaluates to #CYCLE! (with the cycle as evidence), every
// other cell evaluates to the unique value determined by its expression.
type Reference struct {
	exprs map[string]Expr
}

// NewReference creates an empty reference model.
func NewReference() *Reference { return &Reference{exprs: map[string]Expr{}} }

// Apply replaces the current formula set.
func (r *Reference) Apply(sets map[string]Expr, dels []string) {
	for _, id := range dels {
		delete(r.exprs, id)
	}
	for id, ex := range sets {
		r.exprs[id] = ex
	}
}

// Has reports whether the cell currently exists.
func (r *Reference) Has(id string) bool {
	_, ok := r.exprs[id]
	return ok
}

// Cells returns the sorted ids of existing cells.
func (r *Reference) Cells() []string {
	out := make([]string, 0, len(r.exprs))
	for id := range r.exprs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Get evaluates id from scratch and returns its value.
func (r *Reference) Get(id string) Value {
	if _, ok := r.exprs[id]; !ok {
		return Err(ErrRef)
	}
	vals := map[string]Value{}
	deps := map[string]map[string]struct{}{}
	onStack := map[string]bool{}
	var stack []string
	var eval func(string) Value
	eval = func(id string) Value {
		if v, ok := vals[id]; ok {
			return v
		}
		ex, ok := r.exprs[id]
		if !ok {
			return Err(ErrRef)
		}
		if onStack[id] {
			idx := len(stack) - 1
			for stack[idx] != id {
				idx--
			}
			v := CycleErr(append([]string(nil), stack[idx:]...))
			for _, cid := range stack[idx:] {
				if _, locked := vals[cid]; !locked {
					vals[cid] = v
				}
			}
			return v
		}
		onStack[id] = true
		stack = append(stack, id)
		deps[id] = map[string]struct{}{}
		v := evalExpr(ex, func(ref string) Value {
			deps[id][ref] = struct{}{}
			return eval(ref)
		})
		stack = stack[:len(stack)-1]
		onStack[id] = false
		if lv, locked := vals[id]; locked {
			return lv
		}
		vals[id] = v
		return v
	}
	eval(id)

	// Lock every visited cell that sits on a genuine cycle of the
	// dependency graph recorded above (see Engine.Apply pass 2).
	nodes := make([]string, 0, len(deps))
	for x := range deps {
		nodes = append(nodes, x)
	}
	sort.Strings(nodes)
	succ := func(x string) []string {
		var out []string
		for d := range deps[x] {
			if _, ok := deps[d]; ok {
				out = append(out, d)
			}
		}
		sort.Strings(out)
		return out
	}
	inCycleSCC := map[string]bool{}
	for _, scc := range tarjan(nodes, succ) {
		if len(scc) == 1 {
			if _, self := deps[scc[0]][scc[0]]; !self {
				continue
			}
		}
		inSCC := map[string]bool{}
		for _, x := range scc {
			inSCC[x] = true
		}
		for _, x := range scc {
			inCycleSCC[x] = true
			if vals[x].IsErr && vals[x].Code == ErrCycle {
				continue
			}
			vals[x] = CycleErr(findCycle(x, inSCC, succ))
		}
	}

	// Re-evaluate non-cycle cells with the locked cycle values.
	memo := map[string]Value{}
	var reval func(string) Value
	reval = func(x string) Value {
		if v, ok := memo[x]; ok {
			return v
		}
		ex, ok := r.exprs[x]
		if !ok {
			return Err(ErrRef)
		}
		if inCycleSCC[x] {
			return vals[x]
		}
		v := evalExpr(ex, reval)
		memo[x] = v
		return v
	}
	for _, x := range nodes {
		if !inCycleSCC[x] {
			vals[x] = reval(x)
		}
	}
	return vals[id]
}
