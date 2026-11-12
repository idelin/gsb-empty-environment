package netsec

import "fmt"

// state maps variable names to boolean values.
type state map[string]bool

func (s state) clone() state {
	c := make(state, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}

// stateKey produces a canonical encoding: length-prefixed sorted entries.
func stateKey(s state) string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sortStrings(names)
	buf := make([]byte, 0, len(names)*8)
	putInt := func(n int) {
		buf = append(buf, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	putInt(len(names))
	for _, name := range names {
		putInt(len(name))
		buf = append(buf, name...)
		if s[name] {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	return string(buf)
}

func evalExpr(e Expr, s state) (bool, error) {
	switch x := e.(type) {
	case Var:
		v, ok := s[string(x)]
		if !ok {
			return false, &EvalError{Reason: "undefined variable " + string(x)}
		}
		return v, nil
	case Lit:
		return bool(x), nil
	case Not:
		v, err := evalExpr(x.X, s)
		return !v, err
	case And:
		a, err := evalExpr(x.X, s)
		if err != nil {
			return false, err
		}
		b, err := evalExpr(x.Y, s)
		if err != nil {
			return false, err
		}
		return a && b, nil
	case Or:
		a, err := evalExpr(x.X, s)
		if err != nil {
			return false, err
		}
		b, err := evalExpr(x.Y, s)
		if err != nil {
			return false, err
		}
		return a || b, nil
	default:
		return false, &EvalError{Reason: "unknown expression type: " + typeName(e)}
	}
}

func typeName(e Expr) string {
	if e == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", e)
}

// sortStrings is a tiny dependency-free insertion sort: program state has few
// variables and canonical order only needs stability.
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
