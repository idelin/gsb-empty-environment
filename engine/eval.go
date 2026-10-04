package engine

// evalExpr evaluates an expression. read resolves a cell reference to a
// value; it is called exactly once per reference actually read, so callers
// can record real (executed-branch) dependencies from it.
//
// Semantics:
//   - arithmetic evaluates both operands; the first error (left, then right)
//     propagates;
//   - IF evaluates its condition, then only the selected branch;
//   - error literals evaluate to their error.
func evalExpr(e Expr, read func(string) Value) Value {
	switch n := e.(type) {
	case *Const:
		return Int(n.N)
	case *ErrLit:
		return Err(n.Code)
	case *Ref:
		return read(n.ID)
	case *Bin:
		l := evalExpr(n.L, read)
		r := evalExpr(n.R, read)
		if l.IsErr {
			return l
		}
		if r.IsErr {
			return r
		}
		switch n.Op {
		case '+':
			return Int(l.Num + r.Num)
		case '-':
			return Int(l.Num - r.Num)
		case '*':
			return Int(l.Num * r.Num)
		}
	case *If:
		c := evalExpr(n.C, read)
		if c.IsErr {
			return c
		}
		if c.Num != 0 {
			return evalExpr(n.T, read)
		}
		return evalExpr(n.F, read)
	}
	panic("unreachable")
}
