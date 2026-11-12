package policy

import (
	"fmt"
	"strings"

	"example.org/netsec/q0509/ast"
)

type evaluator struct {
	ctx     Context
	columns map[string]struct{}
}

func newEvaluator(ctx Context, columns []string) *evaluator {
	ev := &evaluator{ctx: ctx, columns: make(map[string]struct{}, len(columns))}
	for _, c := range columns {
		ev.columns[c] = struct{}{}
	}
	return ev
}

// isTrue implements SQL three-valued logic: only an explicit boolean true
// passes; NULL and false both fail the predicate.
func (ev *evaluator) isTrue(expr ast.Expr, row Row) (bool, error) {
	value, err := ev.eval(expr, row)
	if err != nil {
		return false, err
	}
	b, ok := value.(bool)
	return ok && b, nil
}

func (ev *evaluator) eval(expr ast.Expr, row Row) (any, error) {
	switch n := expr.(type) {
	case nil:
		return nil, nil
	case *ast.Literal:
		return n.Value, nil
	case *ast.CallerAddr:
		return ev.ctx.Caller, nil
	case *ast.BlockNum:
		return ev.ctx.BlockNum, nil
	case *ast.Identifier:
		if _, ok := ev.columns[n.Name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownColumn, n.Name)
		}
		return row[n.Name], nil
	case *ast.ParenExpr:
		return ev.eval(n.Expr, row)
	case *ast.UnaryExpr:
		return ev.evalUnary(n, row)
	case *ast.BinaryExpr:
		return ev.evalBinary(n, row)
	case *ast.IsNull:
		value, err := ev.eval(n.Expr, row)
		if err != nil {
			return nil, err
		}
		isNull := value == nil
		if n.Not {
			return !isNull, nil
		}
		return isNull, nil
	case *ast.InExpr:
		return ev.evalIn(n, row)
	case *ast.FuncExpr:
		return ev.evalFunc(n, row)
	case *ast.SubqueryExpr:
		// Static checks already reject these; defend in depth.
		return nil, ErrSubquery
	default:
		return nil, fmt.Errorf("%w: unsupported expression %T", ErrBadStatement, expr)
	}
}

func (ev *evaluator) evalUnary(n *ast.UnaryExpr, row Row) (any, error) {
	value, err := ev.eval(n.Expr, row)
	if err != nil {
		return nil, err
	}
	switch n.Operator {
	case "NOT":
		if value == nil {
			return nil, nil
		}
		b, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: NOT requires a boolean operand", ErrBadStatement)
		}
		return !b, nil
	case "-":
		return arithNeg(value)
	default:
		return nil, fmt.Errorf("%w: unsupported unary operator %q", ErrBadStatement, n.Operator)
	}
}

func (ev *evaluator) evalBinary(n *ast.BinaryExpr, row Row) (any, error) {
	switch n.Operator {
	case "AND", "OR":
		return ev.evalLogical(n, row)
	}

	left, err := ev.eval(n.Left, row)
	if err != nil {
		return nil, err
	}
	right, err := ev.eval(n.Right, row)
	if err != nil {
		return nil, err
	}

	switch n.Operator {
	case "=", "<>", "<", "<=", ">", ">=":
		return compareValues(n.Operator, left, right)
	case "+", "-", "*", "/", "%":
		return arithOp(n.Operator, left, right)
	default:
		return nil, fmt.Errorf("%w: unsupported operator %q", ErrBadStatement, n.Operator)
	}
}

func (ev *evaluator) evalLogical(n *ast.BinaryExpr, row Row) (any, error) {
	left, err := ev.eval(n.Left, row)
	if err != nil {
		return nil, err
	}
	right, err := ev.eval(n.Right, row)
	if err != nil {
		return nil, err
	}
	lb, lok := left.(bool)
	rb, rok := right.(bool)

	switch n.Operator {
	case "AND":
		switch {
		case lok && !lb:
			return false, nil
		case rok && !rb:
			return false, nil
		case lok && lb && rok && rb:
			return true, nil
		default:
			return nil, nil
		}
	case "OR":
		switch {
		case lok && lb:
			return true, nil
		case rok && rb:
			return true, nil
		case lok && !lb && rok && !rb:
			return false, nil
		default:
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("%w: %q is not a logical operator", ErrBadStatement, n.Operator)
	}
}

func (ev *evaluator) evalIn(n *ast.InExpr, row Row) (any, error) {
	needle, err := ev.eval(n.Expr, row)
	if err != nil {
		return nil, err
	}
	hasNull := needle == nil
	for _, candidate := range n.Values {
		value, err := ev.eval(candidate, row)
		if err != nil {
			return nil, err
		}
		if value == nil {
			hasNull = true
			continue
		}
		equal, err := compareValues("=", needle, value)
		if err != nil {
			return nil, err
		}
		if b, ok := equal.(bool); ok && b {
			if n.Not {
				return false, nil
			}
			return true, nil
		}
	}
	if hasNull {
		return nil, nil
	}
	return n.Not, nil
}

func (ev *evaluator) evalFunc(n *ast.FuncExpr, row Row) (any, error) {
	args := make([]any, len(n.Args))
	for i, a := range n.Args {
		value, err := ev.eval(a, row)
		if err != nil {
			return nil, err
		}
		args[i] = value
	}
	switch strings.ToUpper(n.Name) {
	case "LOWER":
		if len(args) != 1 {
			return nil, fmt.Errorf("%w: LOWER expects 1 argument", ErrBadStatement)
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("%w: LOWER expects a string", ErrBadStatement)
		}
		return strings.ToLower(s), nil
	case "UPPER":
		if len(args) != 1 {
			return nil, fmt.Errorf("%w: UPPER expects 1 argument", ErrBadStatement)
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("%w: UPPER expects a string", ErrBadStatement)
		}
		return strings.ToUpper(s), nil
	case "LENGTH":
		if len(args) != 1 {
			return nil, fmt.Errorf("%w: LENGTH expects 1 argument", ErrBadStatement)
		}
		switch v := args[0].(type) {
		case string:
			return int64(len(v)), nil
		case []byte:
			return int64(len(v)), nil
		default:
			return nil, fmt.Errorf("%w: LENGTH expects a string", ErrBadStatement)
		}
	case "COALESCE":
		for _, v := range args {
			if v != nil {
				return v, nil
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: function %q is not allowed", ErrBadStatement, n.Name)
	}
}
