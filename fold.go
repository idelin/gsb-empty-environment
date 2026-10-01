package constfold

// Fold recursively simplifies an expression: it pre-evaluates constant
// subtrees, applies algebraic identities, and eliminates redundant
// boolean conditions. The input AST is not mutated; a new tree is
// returned. Non-deterministic or opaque constructs (columns, function
// calls such as NOW()) are preserved, but their children are folded.
func Fold(e Expr) Expr {
	switch n := e.(type) {
	case *Literal, *Column:
		return e
	case *Interval:
		return &Interval{Value: Fold(n.Value), Unit: n.Unit}
	case *FuncCall:
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = Fold(a)
		}
		return &FuncCall{Name: n.Name, Args: args}
	case *Unary:
		return foldUnary(n.Op, Fold(n.X))
	case *Binary:
		return foldBinary(n.Op, Fold(n.Left), Fold(n.Right))
	}
	return e
}

// FoldString parses, folds and re-renders an expression string.
func FoldString(src string) (string, error) {
	e, err := Parse(src)
	if err != nil {
		return "", err
	}
	return Fold(e).String(), nil
}

func foldUnary(op string, x Expr) Expr {
	if lit, ok := x.(*Literal); ok {
		switch op {
		case "NOT":
			if lit.Kind == KindBool {
				return BoolLit(!lit.Bool)
			}
			if lit.Kind == KindNull {
				return lit // NOT NULL = NULL
			}
		case "-":
			switch lit.Kind {
			case KindInt:
				return IntLit(-lit.Int)
			case KindFloat:
				return &Literal{Kind: KindFloat, Flt: -lit.Flt}
			}
		}
	}
	// NOT (NOT x) => x
	if op == "NOT" {
		if u, ok := x.(*Unary); ok && u.Op == "NOT" {
			return u.X
		}
	}
	return &Unary{Op: op, X: x}
}

func foldBinary(op string, l, r Expr) Expr {
	switch op {
	case "AND", "OR":
		return foldBoolConnective(op, l, r)
	case "+", "-", "*", "/", "%":
		return foldArithmetic(op, l, r)
	default: // comparisons
		return foldComparison(op, l, r)
	}
}

func foldBoolConnective(op string, l, r Expr) Expr {
	lb, lIsBool := boolLit(l)
	rb, rIsBool := boolLit(r)
	if op == "AND" {
		// FALSE AND x = FALSE (also absorbs NULL, per SQL 3VL)
		if lIsBool && !lb {
			return BoolLit(false)
		}
		if rIsBool && !rb {
			return BoolLit(false)
		}
		// TRUE AND x = x
		if lIsBool && lb {
			return r
		}
		if rIsBool && rb {
			return l
		}
	} else { // OR
		// TRUE OR x = TRUE
		if lIsBool && lb {
			return BoolLit(true)
		}
		if rIsBool && rb {
			return BoolLit(true)
		}
		// FALSE OR x = x
		if lIsBool && !lb {
			return r
		}
		if rIsBool && !rb {
			return l
		}
	}
	return &Binary{Op: op, Left: l, Right: r}
}

func foldArithmetic(op string, l, r Expr) Expr {
	// Both operands constant: pre-evaluate.
	if isNumberLit(l) && isNumberLit(r) {
		if v, ok := evalArithmetic(op, l.(*Literal), r.(*Literal)); ok {
			return v
		}
		// Fall through (e.g. division by zero: keep for runtime).
	}
	// Algebraic identities.
	switch op {
	case "+":
		if isZero(l) {
			return r
		}
		if isZero(r) {
			return l
		}
	case "-":
		if isZero(r) {
			return l
		}
	case "*":
		if isZero(l) || isZero(r) {
			return IntLit(0)
		}
		if isOne(l) {
			return r
		}
		if isOne(r) {
			return l
		}
	case "/":
		if isOne(r) {
			return l
		}
		if isZero(l) && !isZero(r) {
			return IntLit(0)
		}
	case "%":
		if isOne(r) {
			return IntLit(0)
		}
		if isZero(l) && !isZero(r) {
			return IntLit(0)
		}
	}
	return &Binary{Op: op, Left: l, Right: r}
}

func foldComparison(op string, l, r Expr) Expr {
	ll, lok := l.(*Literal)
	rl, rok := r.(*Literal)
	if !lok || !rok {
		// x = x (same column) is always TRUE for non-nullable refs;
		// conservatively only fold identical columns on equality.
		if op == "=" {
			if lc, ok1 := l.(*Column); ok1 {
				if rc, ok2 := r.(*Column); ok2 && lc.Name == rc.Name {
					return BoolLit(true)
				}
			}
		}
		return &Binary{Op: op, Left: l, Right: r}
	}
	// NULL compared to anything yields NULL.
	if ll.Kind == KindNull || rl.Kind == KindNull {
		return &Literal{Kind: KindNull}
	}
	if isNumberLit(ll) && isNumberLit(rl) {
		return BoolLit(compareNumbers(op, numVal(ll), numVal(rl)))
	}
	if ll.Kind == KindString && rl.Kind == KindString {
		return BoolLit(compareOrdered(op, ll.Str, rl.Str))
	}
	if ll.Kind == KindBool && rl.Kind == KindBool {
		switch op {
		case "=":
			return BoolLit(ll.Bool == rl.Bool)
		case "!=":
			return BoolLit(ll.Bool != rl.Bool)
		}
	}
	return &Binary{Op: op, Left: l, Right: r}
}

// ---- literal helpers ----

func boolLit(e Expr) (bool, bool) {
	if l, ok := e.(*Literal); ok && l.Kind == KindBool {
		return l.Bool, true
	}
	return false, false
}

func isNumberLit(e Expr) bool {
	if l, ok := e.(*Literal); ok {
		return l.Kind == KindInt || l.Kind == KindFloat
	}
	return false
}

func numVal(l *Literal) float64 {
	if l.Kind == KindInt {
		return float64(l.Int)
	}
	return l.Flt
}

func isZero(e Expr) bool {
	if l, ok := e.(*Literal); ok {
		return (l.Kind == KindInt && l.Int == 0) || (l.Kind == KindFloat && l.Flt == 0)
	}
	return false
}

func isOne(e Expr) bool {
	if l, ok := e.(*Literal); ok {
		return (l.Kind == KindInt && l.Int == 1) || (l.Kind == KindFloat && l.Flt == 1)
	}
	return false
}

// evalArithmetic computes op on two numeric literals. The second return
// value is false when evaluation is unsafe (division/modulo by zero).
func evalArithmetic(op string, l, r *Literal) (*Literal, bool) {
	// Integer arithmetic stays integer when both sides are ints.
	if l.Kind == KindInt && r.Kind == KindInt && op != "/" {
		a, b := l.Int, r.Int
		switch op {
		case "+":
			return IntLit(a + b), true
		case "-":
			return IntLit(a - b), true
		case "*":
			return IntLit(a * b), true
		case "%":
			if b == 0 {
				return nil, false
			}
			return IntLit(a % b), true
		}
	}
	a, b := numVal(l), numVal(r)
	switch op {
	case "+":
		return &Literal{Kind: KindFloat, Flt: a + b}, true
	case "-":
		return &Literal{Kind: KindFloat, Flt: a - b}, true
	case "*":
		return &Literal{Kind: KindFloat, Flt: a * b}, true
	case "/":
		if b == 0 {
			return nil, false
		}
		return &Literal{Kind: KindFloat, Flt: a / b}, true
	case "%":
		if b == 0 {
			return nil, false
		}
		return &Literal{Kind: KindFloat, Flt: float64(int64(a) % int64(b))}, true
	}
	return nil, false
}

func compareNumbers(op string, a, b float64) bool {
	switch op {
	case "=":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	}
	return false
}

func compareOrdered(op string, a, b string) bool {
	switch op {
	case "=":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	}
	return false
}
