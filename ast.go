// Package constfold implements a constant-folding and algebraic
// simplification engine for SQL-style boolean/value expressions.
package constfold

import (
	"fmt"
	"strconv"
	"strings"
)

// LiteralKind classifies scalar literal values.
type LiteralKind int

const (
	KindNull LiteralKind = iota
	KindInt
	KindFloat
	KindString
	KindBool
)

// Expr is the common interface of all AST nodes.
type Expr interface {
	exprNode()
	String() string
}

// Literal is a compile-time scalar constant.
type Literal struct {
	Kind LiteralKind
	Int  int64
	Flt  float64
	Str  string
	Bool bool
}

func (*Literal) exprNode() {}

func (l *Literal) String() string {
	switch l.Kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return strconv.FormatInt(l.Int, 10)
	case KindFloat:
		return strconv.FormatFloat(l.Flt, 'g', -1, 64)
	case KindString:
		return "'" + strings.ReplaceAll(l.Str, "'", "''") + "'"
	case KindBool:
		if l.Bool {
			return "TRUE"
		}
		return "FALSE"
	}
	return "?"
}

// Column is a (possibly qualified) column reference, e.g. u.score.
type Column struct{ Name string }

func (*Column) exprNode()        {}
func (c *Column) String() string { return c.Name }

// Binary is a binary operation: arithmetic, comparison, AND/OR.
type Binary struct {
	Op    string
	Left  Expr
	Right Expr
}

func (*Binary) exprNode() {}
func (b *Binary) String() string {
	return "(" + b.Left.String() + " " + b.Op + " " + b.Right.String() + ")"
}

// Unary is a prefix operation: unary minus or NOT.
type Unary struct {
	Op string // "-" or "NOT"
	X  Expr
}

func (*Unary) exprNode() {}
func (u *Unary) String() string {
	if u.Op == "NOT" {
		return "(NOT " + u.X.String() + ")"
	}
	return "(-" + u.X.String() + ")"
}

// FuncCall is a function invocation, e.g. NOW(). Functions are treated
// as opaque (non-foldable) except that their arguments are folded.
type FuncCall struct {
	Name string
	Args []Expr
}

func (*FuncCall) exprNode() {}
func (f *FuncCall) String() string {
	parts := make([]string, len(f.Args))
	for i, a := range f.Args {
		parts[i] = a.String()
	}
	return f.Name + "(" + strings.Join(parts, ", ") + ")"
}

// Interval is a SQL interval literal, e.g. INTERVAL 1 DAY.
type Interval struct {
	Value Expr
	Unit  string
}

func (*Interval) exprNode() {}
func (i *Interval) String() string {
	return fmt.Sprintf("INTERVAL %s %s", i.Value.String(), i.Unit)
}

// BoolLit is a small helper to build a boolean literal.
func BoolLit(v bool) *Literal { return &Literal{Kind: KindBool, Bool: v} }

// IntLit is a small helper to build an integer literal.
func IntLit(v int64) *Literal { return &Literal{Kind: KindInt, Int: v} }
