package fold

// Simplify rewrites an expression into an equivalent, smaller form by
//   - folding pure-constant subtrees into scalar literals
//     (1 + 2 * 3 -> 7, age > 10 + 5 -> age > 15)
//   - applying algebraic identities
//     (x * 1 -> x, x + 0 -> x, x * 0 -> 0)
//   - eliminating trivial boolean conditions
//     (x AND TRUE -> x, x OR FALSE -> x, 1 = 1 -> TRUE)
//
// Non-deterministic calls such as NOW() are treated as opaque and kept.
// Note: rules assume a two-valued (non-NULL) semantics, matching the
// exercise scope; NULL propagation is out of scope.
func Simplify(e Expr) Expr {
	switch n := e.(type) {
	case *Bin:
		return simplifyBin(n.Op, Simplify(n.L), Simplify(n.R))
	case *Unary:
		return simplifyUnary(n.Op, Simplify(n.X))
	case *Call:
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = Simplify(a)
		}
		return &Call{Name: n.Name, Args: args}
	case *Interval:
		return &Interval{Qty: Simplify(n.Qty), Unit: n.Unit}
	default:
		return e
	}
}

func litBool(b bool) *Lit { return &Lit{V: b} }
func litInt(n int64) *Lit { return &Lit{V: n} }

func isBoolLit(e Expr, want bool) bool {
	l, ok := e.(*Lit)
	if !ok {
		return false
	}
	b, ok := l.V.(bool)
	return ok && b == want
}

// isNumLit reports whether e is a numeric literal equal to want.
func isNumLit(e Expr, want int64) bool {
	l, ok := e.(*Lit)
	if !ok {
		return false
	}
	switch v := l.V.(type) {
	case int64:
		return v == want
	case float64:
		return v == float64(want)
	}
	return false
}

func simplifyBin(op string, l, r Expr) Expr {
	switch op {
	case "AND":
		if isBoolLit(l, false) || isBoolLit(r, false) {
			return litBool(false)
		}
		if isBoolLit(l, true) {
			return r
		}
		if isBoolLit(r, true) {
			return l
		}
	case "OR":
		if isBoolLit(l, true) || isBoolLit(r, true) {
			return litBool(true)
		}
		if isBoolLit(l, false) {
			return r
		}
		if isBoolLit(r, false) {
			return l
		}
	case "+":
		if isNumLit(l, 0) {
			return r
		}
		if isNumLit(r, 0) {
			return l
		}
	case "-":
		if isNumLit(r, 0) {
			return l
		}
	case "*":
		if isNumLit(l, 0) || isNumLit(r, 0) {
			return litInt(0)
		}
		if isNumLit(l, 1) {
			return r
		}
		if isNumLit(r, 1) {
			return l
		}
	case "/":
		if isNumLit(r, 1) {
			return l
		}
	}
	if ll, ok := l.(*Lit); ok {
		if rr, ok := r.(*Lit); ok {
			if v, ok := foldConst(op, ll.V, rr.V); ok {
				return &Lit{V: v}
			}
		}
	}
	return &Bin{Op: op, L: l, R: r}
}

func simplifyUnary(op string, x Expr) Expr {
	switch op {
	case "NOT":
		if isBoolLit(x, true) {
			return litBool(false)
		}
		if isBoolLit(x, false) {
			return litBool(true)
		}
		if u, ok := x.(*Unary); ok && u.Op == "NOT" {
			return u.X // NOT NOT x -> x
		}
	case "-":
		if l, ok := x.(*Lit); ok {
			switch v := l.V.(type) {
			case int64:
				return litInt(-v)
			case float64:
				return &Lit{V: -v}
			}
		}
		if u, ok := x.(*Unary); ok && u.Op == "-" {
			return u.X // -(-x) -> x
		}
	}
	return &Unary{Op: op, X: x}
}

// foldConst evaluates op on two literal operands.
// ok is false when the operation is not foldable (type mismatch,
// division by zero, unknown operator).
func foldConst(op string, a, b any) (any, bool) {
	switch op {
	case "+", "-", "*", "/", "%":
		return foldArith(op, a, b)
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		return foldCompare(op, a, b)
	case "AND", "OR":
		x, xok := a.(bool)
		y, yok := b.(bool)
		if !xok || !yok {
			return nil, false
		}
		if op == "AND" {
			return x && y, true
		}
		return x || y, true
	}
	return nil, false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func foldArith(op string, a, b any) (any, bool) {
	// Integer arithmetic stays integer when both operands are ints.
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			switch op {
			case "+":
				return x + y, true
			case "-":
				return x - y, true
			case "*":
				return x * y, true
			case "/":
				if y == 0 {
					return nil, false
				}
				return x / y, true
			case "%":
				if y == 0 {
					return nil, false
				}
				return x % y, true
			}
		}
	}
	x, xok := asFloat(a)
	y, yok := asFloat(b)
	if !xok || !yok {
		return nil, false
	}
	switch op {
	case "+":
		return x + y, true
	case "-":
		return x - y, true
	case "*":
		return x * y, true
	case "/":
		if y == 0 {
			return nil, false
		}
		return x / y, true
	}
	return nil, false
}

func foldCompare(op string, a, b any) (any, bool) {
	// Numeric comparison (int/float mix allowed).
	if x, ok := asFloat(a); ok {
		if y, ok := asFloat(b); ok {
			return cmpResult(op, x < y, x == y), true
		}
		return nil, false
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		if !ok {
			return nil, false
		}
		return cmpResult(op, x < y, x == y), true
	case bool:
		y, ok := b.(bool)
		if !ok {
			return nil, false
		}
		switch op {
		case "=":
			return x == y, true
		case "!=", "<>":
			return x != y, true
		}
	}
	return nil, false
}

func cmpResult(op string, less, equal bool) bool {
	switch op {
	case "=":
		return equal
	case "!=", "<>":
		return !equal
	case "<":
		return less
	case "<=":
		return less || equal
	case ">":
		return !less && !equal
	case ">=":
		return !less || equal
	}
	return false
}
