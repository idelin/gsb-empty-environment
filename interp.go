package main

import "fmt"

// Values are dynamically typed: int8 for integers, bool for booleans.
// Integer arithmetic wraps in signed 8-bit (two's complement), which is
// exactly Go's defined overflow behavior for int8.

func toInt(v interface{}, line int) (int8, error) {
	if x, ok := v.(int8); ok {
		return x, nil
	}
	return 0, fmt.Errorf("line %d: expected integer, got boolean", line)
}

func toBool(v interface{}, line int) (bool, error) {
	if b, ok := v.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("line %d: expected boolean, got integer", line)
}

func evalExpr(e Expr, env map[string]interface{}) (interface{}, error) {
	switch n := e.(type) {
	case *Const:
		return n.Val, nil
	case *Var:
		v, ok := env[n.Name]
		if !ok {
			return nil, fmt.Errorf("line %d: undefined variable %q", n.Line, n.Name)
		}
		return v, nil
	case *Neg:
		v, err := evalExpr(n.E, env)
		if err != nil {
			return nil, err
		}
		x, err := toInt(v, n.Line)
		if err != nil {
			return nil, err
		}
		return -x, nil
	case *Bin:
		l, r, err := evalInts(n.L, n.R, env, n.Line)
		if err != nil {
			return nil, err
		}
		if n.Op == "+" {
			return l + r, nil
		}
		return l - r, nil
	case *Cmp:
		l, r, err := evalInts(n.L, n.R, env, n.Line)
		if err != nil {
			return nil, err
		}
		switch n.Op {
		case "==":
			return l == r, nil
		case "!=":
			return l != r, nil
		case "<":
			return l < r, nil
		case "<=":
			return l <= r, nil
		case ">":
			return l > r, nil
		case ">=":
			return l >= r, nil
		}
		return nil, fmt.Errorf("line %d: unknown comparison %q", n.Line, n.Op)
	case *Not:
		v, err := evalExpr(n.E, env)
		if err != nil {
			return nil, err
		}
		b, err := toBool(v, n.Line)
		if err != nil {
			return nil, err
		}
		return !b, nil
	}
	return nil, fmt.Errorf("unknown expression %T", e)
}

func evalInts(l, r Expr, env map[string]interface{}, line int) (int8, int8, error) {
	lv, err := evalExpr(l, env)
	if err != nil {
		return 0, 0, err
	}
	rv, err := evalExpr(r, env)
	if err != nil {
		return 0, 0, err
	}
	li, err := toInt(lv, line)
	if err != nil {
		return 0, 0, err
	}
	ri, err := toInt(rv, line)
	if err != nil {
		return 0, 0, err
	}
	return li, ri, nil
}

// execBlock runs statements; returned=true means a Return was hit.
func execBlock(stmts []Stmt, env map[string]interface{}) (val interface{}, returned bool, err error) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *Assign:
			v, err := evalExpr(n.E, env)
			if err != nil {
				return nil, false, err
			}
			env[n.Name] = v
		case *If:
			cv, err := evalExpr(n.Cond, env)
			if err != nil {
				return nil, false, err
			}
			b, err := toBool(cv, n.Line)
			if err != nil {
				return nil, false, err
			}
			branch := n.Then
			if !b {
				branch = n.Else
			}
			v, ret, err := execBlock(branch, env)
			if err != nil || ret {
				return v, ret, err
			}
		case *Return:
			v, err := evalExpr(n.E, env)
			if err != nil {
				return nil, false, err
			}
			return v, true, nil
		}
	}
	return nil, false, nil
}

// Run executes the program with the given input values (in declared order)
// and returns the value of the reached return statement.
func Run(p *Program, inputs []int8) (interface{}, error) {
	if len(inputs) != len(p.Inputs) {
		return nil, fmt.Errorf("expected %d inputs, got %d", len(p.Inputs), len(inputs))
	}
	env := make(map[string]interface{})
	for i, name := range p.Inputs {
		env[name] = inputs[i]
	}
	v, ok, err := execBlock(p.Body, env)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("program ended without return")
	}
	return v, nil
}
