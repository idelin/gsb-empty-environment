// Package attrgram implements an attribute-grammar evaluator: synthesized
// and inherited attributes over an AST, scheduled by topological sort of
// the attribute dependency graph, with cycle detection.
package attrgram

import "fmt"

// Pos is a 1-based source position attached to every AST node.
type Pos struct {
	Line, Col int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Type is the static type of an expression. TUnknown marks a failed
// inference and suppresses cascading errors.
type Type int

const (
	TUnknown Type = iota
	TInt
	TFloat
	TBool
)

func (t Type) String() string {
	switch t {
	case TInt:
		return "int"
	case TFloat:
		return "float"
	case TBool:
		return "bool"
	}
	return "<error>"
}

// Node is any AST node. Every node carries its source position so that
// diagnostics can be reported precisely.
type Node interface {
	Position() Pos
}

// Stmt is a statement node.
type Stmt interface{ Node }

// Expr is an expression node.
type Expr interface{ Node }

// Program is a flat sequence of statements (the root scope).
type Program struct {
	Pos   Pos
	Stmts []Stmt
}

func (p *Program) Position() Pos { return p.Pos }

// Decl declares a variable bound to the value of its initializer.
type Decl struct {
	Pos  Pos
	Name string
	Init Expr
}

func (d *Decl) Position() Pos { return d.Pos }

// Print evaluates its expression and records the result (sample output sink).
type Print struct {
	Pos  Pos
	Expr Expr
}

func (p *Print) Position() Pos { return p.Pos }

// IntLit is an integer literal.
type IntLit struct {
	Pos Pos
	Val int64
}

func (l *IntLit) Position() Pos { return l.Pos }

// FloatLit is a floating-point literal.
type FloatLit struct {
	Pos Pos
	Val float64
}

func (l *FloatLit) Position() Pos { return l.Pos }

// BoolLit is a boolean literal.
type BoolLit struct {
	Pos Pos
	Val bool
}

func (l *BoolLit) Position() Pos { return l.Pos }

// Var references a variable from the enclosing scope.
type Var struct {
	Pos  Pos
	Name string
}

func (v *Var) Position() Pos { return v.Pos }

// Binary is a binary operator expression (+ - * / < <= > >= == != && ||).
type Binary struct {
	Pos Pos
	Op  string
	L   Expr
	R   Expr
}

func (b *Binary) Position() Pos { return b.Pos }

func nodeName(n Node) string {
	switch n.(type) {
	case *Program:
		return "Program"
	case *Decl:
		return "Decl"
	case *Print:
		return "Print"
	case *IntLit:
		return "IntLit"
	case *FloatLit:
		return "FloatLit"
	case *BoolLit:
		return "BoolLit"
	case *Var:
		return "Var"
	case *Binary:
		return "Binary"
	}
	return fmt.Sprintf("%T", n)
}
