package policy

import (
	"fmt"
	"strings"
)

// Scope carries the runtime values an expression may reference.
type Scope struct {
	Row      map[string]any // column name (lower-cased) -> value
	Caller   string         // transaction caller address
	BlockNum int64          // current block number
}

// Eval evaluates an expression against a scope.
func Eval(e Expr, sc *Scope) (any, error) {
	switch n := e.(type) {
	case *Literal:
		return n.Value, nil
	case *ColumnRef:
		if sc == nil || sc.Row == nil {
			return nil, fmt.Errorf("column %q has no row scope", n.Name)
		}
		v, ok := sc.Row[strings.ToLower(n.Name)]
		if !ok {
			return nil, fmt.Errorf("unknown column %q", n.Name)
		}
		return v, nil
	case *CallerAddress:
		return sc.Caller, nil
	case *BlockNumber:
		return sc.BlockNum, nil
	case *ParenExpr:
		return Eval(n.X, sc)
	case *UnaryExpr:
		v, err := Eval(n.X, sc)
		if err != nil {
			return nil, err
		}
		if n.Op == OpNot {
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("NOT requires a boolean, got %T", v)
			}
			return !b, nil
		}
		switch num := v.(type) {
		case int64:
			return -num, nil
		case float64:
			return -num, nil
		}
		return nil, fmt.Errorf("cannot negate %T", v)
	case *BinaryExpr:
		return evalBinary(n, sc)
	}
	return nil, fmt.Errorf("cannot evaluate %T", e)
}

func evalBinary(b *BinaryExpr, sc *Scope) (any, error) {
	// AND / OR short-circuit on booleans.
	if b.Op == OpAnd || b.Op == OpOr {
		l, err := boolOf(b.Left, sc)
		if err != nil {
			return nil, err
		}
		if b.Op == OpAnd && !l {
			return false, nil
		}
		if b.Op == OpOr && l {
			return true, nil
		}
		return boolOf(b.Right, sc)
	}

	l, err := Eval(b.Left, sc)
	if err != nil {
		return nil, err
	}
	r, err := Eval(b.Right, sc)
	if err != nil {
		return nil, err
	}

	switch b.Op {
	case OpEq, OpNeq:
		eq, err := valuesEqual(l, r)
		if err != nil {
			return nil, err
		}
		if b.Op == OpNeq {
			return !eq, nil
		}
		return eq, nil
	case OpLt, OpLte, OpGt, OpGte:
		c, err := compareValues(l, r)
		if err != nil {
			return nil, err
		}
		switch b.Op {
		case OpLt:
			return c < 0, nil
		case OpLte:
			return c <= 0, nil
		case OpGt:
			return c > 0, nil
		}
		return c >= 0, nil
	}

	// Arithmetic.
	lf, lok := asFloat(l)
	rf, rok := asFloat(r)
	if !lok || !rok {
		return nil, fmt.Errorf("arithmetic on non-numeric values %T, %T", l, r)
	}
	var res float64
	switch b.Op {
	case OpAdd:
		res = lf + rf
	case OpSub:
		res = lf - rf
	case OpMul:
		res = lf * rf
	case OpDiv:
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		res = lf / rf
	}
	if _, li := l.(int64); li {
		if _, ri := r.(int64); ri && b.Op != OpDiv {
			return int64(res), nil
		}
	}
	return res, nil
}

func boolOf(e Expr, sc *Scope) (bool, error) {
	v, err := Eval(e, sc)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expected boolean, got %T", v)
	}
	return b, nil
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

func valuesEqual(a, b any) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	if fa, ok := asFloat(a); ok {
		fb, ok := asFloat(b)
		if !ok {
			return false, fmt.Errorf("cannot compare %T and %T", a, b)
		}
		return fa == fb, nil
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		if !ok {
			return false, fmt.Errorf("cannot compare %T and %T", a, b)
		}
		return av == bv, nil
	case bool:
		bv, ok := b.(bool)
		if !ok {
			return false, fmt.Errorf("cannot compare %T and %T", a, b)
		}
		return av == bv, nil
	}
	return false, fmt.Errorf("cannot compare %T and %T", a, b)
}

func compareValues(a, b any) (int, error) {
	if fa, ok := asFloat(a); ok {
		fb, ok := asFloat(b)
		if !ok {
			return 0, fmt.Errorf("cannot order %T and %T", a, b)
		}
		switch {
		case fa < fb:
			return -1, nil
		case fa > fb:
			return 1, nil
		}
		return 0, nil
	}
	if sa, ok := a.(string); ok {
		sb, ok := b.(string)
		if !ok {
			return 0, fmt.Errorf("cannot order %T and %T", a, b)
		}
		return strings.Compare(sa, sb), nil
	}
	return 0, fmt.Errorf("cannot order %T and %T", a, b)
}
