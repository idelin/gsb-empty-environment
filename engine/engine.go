// Package engine implements a versioned, incremental spreadsheet cell
// engine. Formulas support integer constants, cell references, + - *,
// IF(cond, then, else) and error literals (#ERR!, #REF!, #CYCLE!).
//
// Dependencies are dynamic: a cell only depends on the references that were
// actually read while evaluating it, so an IF only depends on the selected
// branch. A batch of modifications is applied atomically as one new
// version; only affected cells are recomputed. Old versions remain
// queryable read-only.
package engine

import (
	"fmt"
	"sort"
	"strings"
)

type cell struct {
	id   string
	src  string
	expr Expr
	val  Value
	// deps are the cells actually read during the last evaluation,
	// including ids that do not (yet) exist.
	deps map[string]struct{}
}

type histEntry struct {
	version int
	val     Value
	deleted bool
}

// Engine holds the current (latest version) state plus per-cell history.
type Engine struct {
	cells map[string]*cell
	// dependents[x] = cells that read x during their last evaluation.
	dependents map[string]map[string]struct{}
	history    map[string][]histEntry
	version    int
}

// New creates an empty engine at version 0.
func New() *Engine {
	return &Engine{
		cells:      map[string]*cell{},
		dependents: map[string]map[string]struct{}{},
		history:    map[string][]histEntry{},
	}
}

// Version returns the current version (number of applied batches).
func (e *Engine) Version() int { return e.version }

func validID(id string) bool {
	if id == "" || strings.EqualFold(id, "IF") {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if i == 0 {
			if !isAlpha(c) {
				return false
			}
		} else if !isAlnum(c) {
			return false
		}
	}
	return true
}

// Apply atomically applies one batch of modifications as a single new
// version: every set/delete in the batch becomes visible together. It
// recomputes exactly the affected cells and returns their sorted ids.
// A parse or validation error aborts the whole batch with no version bump.
func (e *Engine) Apply(sets map[string]string, dels []string) ([]string, error) {
	parsed := make(map[string]Expr, len(sets))
	for id, src := range sets {
		if !validID(id) {
			return nil, fmt.Errorf("invalid cell id %q", id)
		}
		ex, err := Parse(src)
		if err != nil {
			return nil, fmt.Errorf("cell %s: %v", id, err)
		}
		parsed[id] = ex
	}
	delSet := map[string]bool{}
	for _, id := range dels {
		if !validID(id) {
			return nil, fmt.Errorf("invalid cell id %q", id)
		}
		if _, dup := parsed[id]; dup {
			return nil, fmt.Errorf("cell %s is both set and deleted", id)
		}
		delSet[id] = true
	}
	if len(parsed) == 0 && len(delSet) == 0 {
		return nil, nil
	}

	changed := map[string]bool{}
	for id := range parsed {
		changed[id] = true
	}
	for id := range delSet {
		changed[id] = true
	}

	// dirty = changed cells plus their transitive dependents in the
	// current (pre-batch) graph. Clean cells keep their cached values:
	// they read no dirty cell, so their values cannot change.
	dirty := map[string]bool{}
	queue := make([]string, 0, len(changed))
	for id := range changed {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if dirty[id] {
			continue
		}
		dirty[id] = true
		for dep := range e.dependents[id] {
			if !dirty[dep] {
				queue = append(queue, dep)
			}
		}
	}

	// Apply deletes: drop the cell and its outgoing edges. Edges from
	// other cells pointing at the deleted id stay, so re-adding the id
	// later dirties exactly those cells.
	for id := range delSet {
		if c, ok := e.cells[id]; ok {
			for d := range c.deps {
				delete(e.dependents[d], id)
			}
			delete(e.cells, id)
		}
	}

	// Apply sets.
	for id, ex := range parsed {
		c, ok := e.cells[id]
		if !ok {
			c = &cell{id: id}
			e.cells[id] = c
		}
		c.expr = ex
		c.src = sets[id]
	}

	// Drop stale outgoing edges of dirty cells; they are re-recorded
	// during re-evaluation.
	for id := range dirty {
		if c, ok := e.cells[id]; ok {
			for d := range c.deps {
				delete(e.dependents[d], id)
			}
			c.deps = map[string]struct{}{}
		}
	}

	ctx := &batchCtx{
		e:          e,
		dirty:      dirty,
		state:      map[string]int{},
		recomputed: map[string]bool{},
	}
	ids := make([]string, 0, len(dirty))
	for id := range dirty {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var existing []string
	for _, id := range ids {
		if _, ok := e.cells[id]; ok {
			existing = append(existing, id)
			ctx.eval(id)
		}
	}

	// Pass 2: lock every dirty cell that sits on a genuine cycle of the
	// final dependency graph. The DFS above only locks the loops it
	// happens to close on its own stack; a cell whose cycle runs through
	// already-evaluated cells would otherwise escape with a non-cycle
	// error. Cells on genuine cycles always hold errors at this point
	// (a locked cell is #CYCLE!, and any cell reading an executed error
	// is an error), so forcing #CYCLE! here never changes which
	// references were read.
	succ := func(id string) []string {
		var out []string
		for d := range e.cells[id].deps {
			if dirty[d] {
				if _, ok := e.cells[d]; ok {
					out = append(out, d)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	inCycleSCC := map[string]bool{}
	for _, scc := range tarjan(existing, succ) {
		if len(scc) == 1 {
			if _, self := e.cells[scc[0]].deps[scc[0]]; !self {
				continue
			}
		}
		inSCC := map[string]bool{}
		for _, id := range scc {
			inSCC[id] = true
		}
		for _, id := range scc {
			inCycleSCC[id] = true
			c := e.cells[id]
			if c.val.IsErr && c.val.Code == ErrCycle {
				continue // already locked with genuine evidence
			}
			c.val = CycleErr(findCycle(id, inSCC, succ))
		}
	}

	// Pass 3: forcing changed some error codes, so re-evaluate the
	// remaining dirty cells. Their dependencies form a DAG, and their
	// reads are unchanged (only error codes moved), so this is a plain
	// bottom-up re-evaluation.
	memo := map[string]Value{}
	var reval func(string) Value
	reval = func(id string) Value {
		if v, ok := memo[id]; ok {
			return v
		}
		c, ok := e.cells[id]
		if !ok {
			return Err(ErrRef)
		}
		if !dirty[id] || inCycleSCC[id] {
			return c.val
		}
		v := evalExpr(c.expr, reval)
		memo[id] = v
		return v
	}
	for _, id := range existing {
		if !inCycleSCC[id] {
			e.cells[id].val = reval(id)
		}
	}

	// Rebuild reverse edges from the freshly recorded dependencies.
	for id := range ctx.recomputed {
		c := e.cells[id]
		for d := range c.deps {
			m := e.dependents[d]
			if m == nil {
				m = map[string]struct{}{}
				e.dependents[d] = m
			}
			m[id] = struct{}{}
		}
	}

	e.version++
	for id := range ctx.recomputed {
		e.history[id] = append(e.history[id], histEntry{version: e.version, val: e.cells[id].val})
	}
	for id := range delSet {
		e.history[id] = append(e.history[id], histEntry{version: e.version, deleted: true})
	}

	out := make([]string, 0, len(ctx.recomputed))
	for id := range ctx.recomputed {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// batchCtx carries per-batch evaluation state.
type batchCtx struct {
	e     *Engine
	dirty map[string]bool
	// state: 1 = on the evaluation stack, 2 = done (only for dirty cells).
	state      map[string]int
	stack      []string
	recomputed map[string]bool
}

// eval returns the up-to-date value of id. Clean cells answer from the
// cache; dirty cells are re-evaluated on demand, recording their real
// dependencies. Cycles are detected via the evaluation stack and every
// cell on the cycle receives the same evidence.
func (ctx *batchCtx) eval(id string) Value {
	c, ok := ctx.e.cells[id]
	if !ok {
		return Err(ErrRef)
	}
	if !ctx.dirty[id] {
		return c.val
	}
	switch ctx.state[id] {
	case 2:
		return c.val
	case 1:
		idx := len(ctx.stack) - 1
		for ctx.stack[idx] != id {
			idx--
		}
		cyc := append([]string(nil), ctx.stack[idx:]...)
		v := CycleErr(cyc)
		for _, cid := range cyc {
			cc := ctx.e.cells[cid]
			cc.val = v
			ctx.state[cid] = 2
			ctx.recomputed[cid] = true
		}
		return v
	}
	ctx.state[id] = 1
	ctx.stack = append(ctx.stack, id)
	c.deps = map[string]struct{}{}
	v := evalExpr(c.expr, func(ref string) Value {
		c.deps[ref] = struct{}{}
		return ctx.eval(ref)
	})
	ctx.stack = ctx.stack[:len(ctx.stack)-1]
	if ctx.state[id] != 2 {
		// Not locked by a nested cycle detection: record the value.
		ctx.state[id] = 2
		c.val = v
	}
	ctx.recomputed[id] = true
	return c.val
}

// Get returns the value of cell id at the given version. found is false if
// the cell did not exist at that version. Versions outside [0, Version()]
// are clamped.
func (e *Engine) Get(id string, ver int) (val Value, found bool) {
	if ver < 0 {
		ver = 0
	}
	if ver > e.version {
		ver = e.version
	}
	h := e.history[id]
	lo, hi := 0, len(h)
	for lo < hi {
		mid := (lo + hi) / 2
		if h[mid].version <= ver {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Value{}, false
	}
	ent := h[lo-1]
	if ent.deleted {
		return Value{}, false
	}
	return ent.val, true
}

// Cells returns the sorted ids of cells existing in the current version.
func (e *Engine) Cells() []string {
	out := make([]string, 0, len(e.cells))
	for id := range e.cells {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Formula returns the current formula source of a cell.
func (e *Engine) Formula(id string) (string, bool) {
	c, ok := e.cells[id]
	if !ok {
		return "", false
	}
	return c.src, true
}

// tarjan returns the strongly connected components of the graph induced
// by nodes and the succ edge function.
func tarjan(nodes []string, succ func(string) []string) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var sccs [][]string
	counter := 0
	var visit func(v string)
	visit = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ(v) {
			if _, seen := index[w]; !seen {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if index[w] < low[v] {
					low[v] = index[w]
				}
			}
		}
		if low[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}
	for _, n := range nodes {
		if _, seen := index[n]; !seen {
			visit(n)
		}
	}
	return sccs
}

// findCycle returns a genuine dependency cycle through start, following
// succ edges that stay inside inSCC. start must belong to a non-trivial
// strongly connected component, so such a cycle always exists.
func findCycle(start string, inSCC map[string]bool, succ func(string) []string) []string {
	var path []string
	onPath := map[string]bool{}
	visited := map[string]bool{}
	var dfs func(id string) bool
	dfs = func(id string) bool {
		path = append(path, id)
		onPath[id] = true
		for _, next := range succ(id) {
			if !inSCC[next] {
				continue
			}
			if next == start {
				return true
			}
			if !onPath[next] && !visited[next] {
				if dfs(next) {
					return true
				}
			}
		}
		visited[id] = true
		path = path[:len(path)-1]
		onPath[id] = false
		return false
	}
	if !dfs(start) {
		return []string{start}
	}
	return append([]string(nil), path...)
}
