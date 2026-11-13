package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Op is a binary arithmetic operator.
type Op string

const (
	OpAdd Op = "+"
	OpSub Op = "-"
	OpMul Op = "*"
	OpDiv Op = "/"
)

// AggKind names a supported aggregate.
type AggKind string

const (
	AggSum   AggKind = "SUM"
	AggCount AggKind = "COUNT"
	AggMin   AggKind = "MIN"
	AggMax   AggKind = "MAX"
)

// Retractable reports whether the aggregate admits monoid retraction
// (inverse subtraction). MIN/MAX cannot be retracted once a winning row is
// removed without retaining every contributing row.
func (k AggKind) Retractable() bool {
	return k == AggSum || k == AggCount
}

// Expr is a sealed projection-expression node. External packages can build
// expressions only through the concrete structs in this file, so the host can
// analyze them exhaustively.
type Expr interface {
	kindMarker()
}

// ColExpr references a source column by name.
type ColExpr struct {
	Column string
}

// LitExpr is a constant integer literal.
type LitExpr struct {
	Value int64
}

// BinExpr is a binary arithmetic expression.
type BinExpr struct {
	Op    Op
	Left  Expr
	Right Expr
}

// FuncExpr is a named deterministic scalar function.
type FuncExpr struct {
	Name string
	Args []Expr
}

// AggExpr aggregates an argument over a group.
type AggExpr struct {
	Kind AggKind
	Arg  Expr
}

func (ColExpr) kindMarker()  {}
func (LitExpr) kindMarker()  {}
func (BinExpr) kindMarker()  {}
func (FuncExpr) kindMarker() {}
func (AggExpr) kindMarker()  {}

// ErrInvalidExpr is returned when an expression is malformed or references an
// unknown column/function/operator.
var ErrInvalidExpr = errors.New("netsec: invalid expression")

// errExpr is the wrapped evaluation form of ErrInvalidExpr.
var errExpr = ErrInvalidExpr

func evalExpr(e Expr, row Row) (int64, error) {
	switch n := e.(type) {
	case ColExpr:
		v, ok := row[n.Column]
		if !ok {
			return 0, fmt.Errorf("%w: missing column %q", errExpr, n.Column)
		}
		return asInt64(v)
	case LitExpr:
		return n.Value, nil
	case BinExpr:
		l, err := evalExpr(n.Left, row)
		if err != nil {
			return 0, err
		}
		r, err := evalExpr(n.Right, row)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case OpAdd:
			return l + r, nil
		case OpSub:
			return l - r, nil
		case OpMul:
			return l * r, nil
		case OpDiv:
			if r == 0 {
				return 0, fmt.Errorf("%w: division by zero", errExpr)
			}
			return l / r, nil
		default:
			return 0, fmt.Errorf("%w: unknown operator %q", errExpr, n.Op)
		}
	case FuncExpr:
		fn, ok := scalarFuncs[n.Name]
		if !ok {
			return 0, fmt.Errorf("%w: unknown function %q", errExpr, n.Name)
		}
		args := make([]int64, len(n.Args))
		for i, a := range n.Args {
			v, err := evalExpr(a, row)
			if err != nil {
				return 0, err
			}
			args[i] = v
		}
		return fn.fn(args)
	case AggExpr:
		return 0, fmt.Errorf("%w: aggregate used in row-level position", errExpr)
	default:
		return 0, fmt.Errorf("%w: unknown expression node", errExpr)
	}
}

func asInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case string:
		i, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: non-integer column value", errExpr)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("%w: non-integer column value %T", errExpr, v)
	}
}

// canonicalExpr renders an expression in a deterministic, structural form so
// that identical expressions always produce identical digests.
func canonicalExpr(e Expr) string {
	switch n := e.(type) {
	case ColExpr:
		return "col:" + n.Column
	case LitExpr:
		return "lit:" + strconv.FormatInt(n.Value, 10)
	case BinExpr:
		return "bin(" + string(n.Op) + "," + canonicalExpr(n.Left) + "," + canonicalExpr(n.Right) + ")"
	case FuncExpr:
		parts := make([]string, len(n.Args))
		for i, a := range n.Args {
			parts[i] = canonicalExpr(a)
		}
		return "fn(" + n.Name + "," + strings.Join(parts, ";") + ")"
	case AggExpr:
		return "agg(" + string(n.Kind) + "," + canonicalExpr(n.Arg) + ")"
	default:
		return "unknown"
	}
}

// validateExpr walks the expression tree and rejects unknown nodes, empty
// names and references to columns outside the source schema.
func validateExpr(e Expr, schema map[string]bool) error {
	switch n := e.(type) {
	case ColExpr:
		if n.Column == "" || !schema[n.Column] {
			return fmt.Errorf("%w: unknown column %q", errExpr, n.Column)
		}
	case LitExpr:
	case BinExpr:
		if n.Op != OpAdd && n.Op != OpSub && n.Op != OpMul && n.Op != OpDiv {
			return fmt.Errorf("%w: unknown operator %q", errExpr, n.Op)
		}
		if err := validateExpr(n.Left, schema); err != nil {
			return err
		}
		return validateExpr(n.Right, schema)
	case FuncExpr:
		if _, ok := scalarFuncs[n.Name]; !ok {
			return fmt.Errorf("%w: unknown function %q", errExpr, n.Name)
		}
		fn := scalarFuncs[n.Name]
		if fn.arity > 0 && len(n.Args) != fn.arity {
			return fmt.Errorf("%w: function %q expects %d args", errExpr, n.Name, fn.arity)
		}
		for _, a := range n.Args {
			if err := validateExpr(a, schema); err != nil {
				return err
			}
		}
	case AggExpr:
		if n.Kind != AggSum && n.Kind != AggCount && n.Kind != AggMin && n.Kind != AggMax {
			return fmt.Errorf("%w: unknown aggregate %q", errExpr, n.Kind)
		}
		return validateExpr(n.Arg, schema)
	case nil:
		return fmt.Errorf("%w: nil expression", errExpr)
	default:
		return fmt.Errorf("%w: unknown expression node %T", errExpr, e)
	}
	return nil
}

// containsAgg reports whether the expression tree contains an aggregate node.
func containsAgg(e Expr) bool {
	switch n := e.(type) {
	case AggExpr:
		return true
	case BinExpr:
		return containsAgg(n.Left) || containsAgg(n.Right)
	case FuncExpr:
		for _, a := range n.Args {
			if containsAgg(a) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
