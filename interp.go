package main

import "fmt"

// All arithmetic is performed on int8 values; overflow wraps around
// (two's complement), matching Go's defined int8 semantics.

func boolByte(b bool) int8 {
	if b {
		return 1
	}
	return 0
}

func evalExpr(e Expr, env map[string]int8) (int8, error) {
	switch e := e.(type) {
	case Num:
		return e.Val, nil
	case Var:
		v, ok := env[e.Name]
		if !ok {
			return 0, fmt.Errorf("undefined variable %q", e.Name)
		}
		return v, nil
	case Neg:
		v, err := evalExpr(e.E, env)
		if err != nil {
			return 0, err
		}
		return -v, nil // wraps: -(-128) == -128
	case Bin:
		l, err := evalExpr(e.L, env)
		if err != nil {
			return 0, err
		}
		r, err := evalExpr(e.R, env)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case "+":
			return l + r, nil
		case "-":
			return l - r, nil
		case "<":
			return boolByte(l < r), nil
		case "<=":
			return boolByte(l <= r), nil
		case ">":
			return boolByte(l > r), nil
		case ">=":
			return boolByte(l >= r), nil
		case "==":
			return boolByte(l == r), nil
		case "!=":
			return boolByte(l != r), nil
		}
		return 0, fmt.Errorf("unknown operator %q", e.Op)
	}
	return 0, fmt.Errorf("bad expression")
}

// exec runs a statement list; ok is false if no return was reached.
func exec(stmts []Stmt, env map[string]int8) (val int8, ok bool, err error) {
	for _, s := range stmts {
		switch s := s.(type) {
		case Input:
			// no-op at run time; inputs are pre-bound in env
		case Assign:
			v, err := evalExpr(s.E, env)
			if err != nil {
				return 0, false, err
			}
			env[s.Name] = v
		case If:
			c, err := evalExpr(s.Cond, env)
			if err != nil {
				return 0, false, err
			}
			body := s.Else
			if c != 0 {
				body = s.Then
			}
			v, ok, err := exec(body, env)
			if err != nil || ok {
				return v, ok, err
			}
		case Return:
			v, err := evalExpr(s.E, env)
			if err != nil {
				return 0, false, err
			}
			return v, true, nil
		}
	}
	return 0, false, nil
}

// inputNames returns the declared input variables in order.
func inputNames(prog []Stmt) []string {
	var names []string
	for _, s := range prog {
		if in, ok := s.(Input); ok {
			names = append(names, in.Names...)
		}
	}
	return names
}

// run executes the program with the given input assignment.
// Falling off the end without a return yields 0.
func run(prog []Stmt, inputs map[string]int8) (int8, error) {
	env := make(map[string]int8, len(inputs))
	for _, name := range inputNames(prog) {
		v, ok := inputs[name]
		if !ok {
			return 0, fmt.Errorf("missing value for input %q", name)
		}
		env[name] = v
	}
	v, _, err := exec(prog, env)
	return v, err
}
