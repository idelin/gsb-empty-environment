// Package fold implements a constant-folding and algebraic
// simplification engine for SQL WHERE-style expressions.
package fold

import (
	"fmt"
	"strconv"
	"strings"
)

// Expr is a node in the expression AST.
type Expr interface{ exprNode() }

// Lit is a literal scalar: int64, float64, string or bool.
type Lit struct{ V any }

// Col is a column reference, e.g. "u.created_at".
type Col struct{ Name string }

// Bin is a binary operation. Op is one of:
// + - * / %, = != <> < <= > >=, AND, OR.
type Bin struct {
	Op   string
	L, R Expr
}

// Unary is a prefix operation: NOT or arithmetic negation "-".
type Unary struct {
	Op string
	X  Expr
}

// Call is a function invocation, e.g. NOW().
type Call struct {
	Name string
	Args []Expr
}

// Interval is a SQL interval, e.g. INTERVAL 1 DAY.
type Interval struct {
	Qty  Expr
	Unit string
}

func (*Lit) exprNode()      {}
func (*Col) exprNode()      {}
func (*Bin) exprNode()      {}
func (*Unary) exprNode()    {}
func (*Call) exprNode()     {}
func (*Interval) exprNode() {}

// prec returns the binding precedence used when rendering.
func prec(e Expr) int {
	switch n := e.(type) {
	case *Bin:
		switch n.Op {
		case "OR":
			return 1
		case "AND":
			return 2
		case "=", "!=", "<>", "<", "<=", ">", ">=":
			return 4
		case "+", "-":
			return 5
		case "*", "/", "%":
			return 6
		}
	case *Unary:
		return 3
	}
	return 9
}

func render(e Expr, parentPrec int) string {
	s := String(e)
	if prec(e) < parentPrec {
		return "(" + s + ")"
	}
	return s
}

// String renders an expression back to SQL text.
func String(e Expr) string {
	switch n := e.(type) {
	case *Lit:
		switch v := n.V.(type) {
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'g', -1, 64)
		case string:
			return "'" + strings.ReplaceAll(v, "'", "''") + "'"
		case bool:
			if v {
				return "TRUE"
			}
			return "FALSE"
		}
	case *Col:
		return n.Name
	case *Bin:
		p := prec(n)
		return render(n.L, p) + " " + n.Op + " " + render(n.R, p+1)
	case *Unary:
		if n.Op == "NOT" {
			return "NOT " + render(n.X, prec(n))
		}
		return "-" + render(n.X, prec(n))
	case *Call:
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = String(a)
		}
		return n.Name + "(" + strings.Join(args, ", ") + ")"
	case *Interval:
		return "INTERVAL " + String(n.Qty) + " " + n.Unit
	}
	panic(fmt.Sprintf("fold: unknown node %T", e))
}
