package cellengine

import "sort"

// Reference is an independent, non-incremental evaluator used to cross-check
// the engine. It evaluates every cell from scratch against the given formula
// set with no carry-over state of any kind. Like the engine, it evaluates
// only the selected branch of an IF, so cycles in unselected branches are
// not errors. It shares only the parser and the Value type with the engine;
// the evaluation logic is written separately on purpose.
func Reference(cells map[string]string) map[string]Value {
	r := &refEval{
		cells:   cells,
		memo:    make(map[string]Value, len(cells)),
		onStack: make(map[string]bool),
	}
	ids := make([]string, 0, len(cells))
	for id := range cells {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make(map[string]Value, len(cells))
	for _, id := range ids {
		out[id] = r.cell(id)
	}
	return out
}

type refEval struct {
	cells   map[string]string
	memo    map[string]Value
	stack   []string
	onStack map[string]bool
}

func (r *refEval) cell(id string) Value {
	if v, ok := r.memo[id]; ok {
		return v
	}
	f, ok := r.cells[id]
	if !ok {
		return errRef(id)
	}
	if r.onStack[id] {
		start := 0
		for i, s := range r.stack {
			if s == id {
				start = i
				break
			}
		}
		return errCycle(append([]string{}, r.stack[start:]...))
	}
	r.onStack[id] = true
	r.stack = append(r.stack, id)
	var v Value
	ast, err := Parse(f)
	if err != nil {
		v = errParse(err)
	} else {
		v = r.expr(ast)
	}
	r.stack = r.stack[:len(r.stack)-1]
	delete(r.onStack, id)
	r.memo[id] = v
	return v
}

func (r *refEval) expr(ex expr) Value {
	switch n := ex.(type) {
	case numLit:
		return NumVal(n.v)
	case errExpr:
		return errLiteral()
	case refExpr:
		return r.cell(n.id)
	case binExpr:
		l := r.expr(n.l)
		if l.IsErr {
			return l
		}
		rr := r.expr(n.r)
		if rr.IsErr {
			return rr
		}
		switch n.op {
		case '+':
			return NumVal(l.Num + rr.Num)
		case '-':
			return NumVal(l.Num - rr.Num)
		case '*':
			return NumVal(l.Num * rr.Num)
		}
		return errVal("#OP!")
	case ifExpr:
		c := r.expr(n.c)
		if c.IsErr {
			return c
		}
		if c.Num != 0 {
			return r.expr(n.t)
		}
		return r.expr(n.f)
	}
	return errVal("#AST!")
}
