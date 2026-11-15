package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Node is implemented by every statement and expression in a Program.
type Node interface {
	node()
}

// Stmt is a statement in the restricted script language.
type Stmt interface {
	Node
	stmt()
}

// Expr is an expression in the restricted script language.
type Expr interface {
	Node
	expr()
}

// Program is a sequence of statements.
type Program struct {
	Stmts []Stmt
}

// AssignStmt assigns Value to Target. Target must be a *VarExpr or a
// *FieldExpr. A field assignment is the only operation that can write a
// prototype.
type AssignStmt struct {
	Target Expr
	Value  Expr
}

// ExprStmt evaluates an expression for its side effects.
type ExprStmt struct {
	X Expr
}

// IfStmt is a two-way conditional. Else may be empty.
type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
}

// WhileStmt executes Body while Cond holds, with a hard upper bound on the
// number of body executions. MaxIterations must be between 0 and 16.
type WhileStmt struct {
	Cond          Expr
	Body          []Stmt
	MaxIterations int
}

// RepeatStmt executes Body exactly Times times. Times must be between 0 and
// 16.
type RepeatStmt struct {
	Times int
	Body  []Stmt
}

// VarExpr reads a variable. An unbound variable denotes undefined.
type VarExpr struct {
	Name string
}

// NewExpr allocates an object whose prototype is the shared prototype.
type NewExpr struct {
	Label string
}

// StringExpr is a string constant. Only the values in StringDomain are
// accepted by Validate.
type StringExpr struct {
	Value string
}

// ExternalStringExpr denotes an attacker-controlled string. It may take any
// value in StringDomain.
type ExternalStringExpr struct {
	Label string
}

// FieldExpr reads Base[Key].
type FieldExpr struct {
	Base Expr
	Key  Expr
}

// EqExpr compares two values.
type EqExpr struct {
	Left  Expr
	Right Expr
}

// NotExpr negates a condition.
type NotExpr struct {
	X Expr
}

// AndExpr and OrExpr combine conditions.
type AndExpr struct {
	Left  Expr
	Right Expr
}

type OrExpr struct {
	Left  Expr
	Right Expr
}

func (*AssignStmt) node()         {}
func (*ExprStmt) node()           {}
func (*IfStmt) node()             {}
func (*WhileStmt) node()          {}
func (*RepeatStmt) node()         {}
func (*VarExpr) node()            {}
func (*NewExpr) node()            {}
func (*StringExpr) node()         {}
func (*ExternalStringExpr) node() {}
func (*FieldExpr) node()          {}
func (*EqExpr) node()             {}
func (*NotExpr) node()            {}
func (*AndExpr) node()            {}
func (*OrExpr) node()             {}

func (*AssignStmt) stmt() {}
func (*ExprStmt) stmt()   {}
func (*IfStmt) stmt()     {}
func (*WhileStmt) stmt()  {}
func (*RepeatStmt) stmt() {}

func (*VarExpr) expr()            {}
func (*NewExpr) expr()            {}
func (*StringExpr) expr()         {}
func (*ExternalStringExpr) expr() {}
func (*FieldExpr) expr()          {}
func (*EqExpr) expr()             {}
func (*NotExpr) expr()            {}
func (*AndExpr) expr()            {}
func (*OrExpr) expr()             {}

// StringDomain is the finite set of strings a program can handle.
var stringDomain = []string{"a", "b", "__proto__", "constructor", "prototype"}

// StringDomain returns a copy of the finite string domain.
func StringDomain() []string {
	out := make([]string, len(stringDomain))
	copy(out, stringDomain)
	return out
}

// InStringDomain reports whether s is one of the allowed strings.
func InStringDomain(s string) bool {
	for _, v := range stringDomain {
		if s == v {
			return true
		}
	}
	return false
}

// Convenience constructors. They keep test programs and generated programs
// compact without hiding the AST shape.

// Var creates a variable read.
func Var(name string) *VarExpr { return &VarExpr{Name: name} }

// NewObject creates an object allocation.
func NewObject() *NewExpr { return &NewExpr{} }

// NewNamedObject creates a labeled object allocation. Labels are diagnostic
// only.
func NewNamedObject(label string) *NewExpr { return &NewExpr{Label: label} }

// Lit creates a string constant.
func Lit(s string) *StringExpr { return &StringExpr{Value: s} }

// External creates an attacker-controlled string.
func External() *ExternalStringExpr { return &ExternalStringExpr{} }

// ExternalNamed creates a labeled attacker-controlled string.
func ExternalNamed(label string) *ExternalStringExpr {
	return &ExternalStringExpr{Label: label}
}

// Get creates a field read.
func Get(base, key Expr) *FieldExpr { return &FieldExpr{Base: base, Key: key} }

// Put creates an assignment. Target may be a variable or field expression.
func Put(target, value Expr) *AssignStmt {
	return &AssignStmt{Target: target, Value: value}
}

// Let creates a variable assignment.
func Let(name string, value Expr) *AssignStmt {
	return Put(Var(name), value)
}

// SetField creates a field assignment.
func SetField(base, key, value Expr) *AssignStmt {
	return Put(Get(base, key), value)
}

// If creates a conditional statement.
func If(cond Expr, then, els []Stmt) *IfStmt {
	return &IfStmt{Cond: cond, Then: then, Else: els}
}

// While creates a bounded while loop.
func While(cond Expr, maxIterations int, body ...Stmt) *WhileStmt {
	return &WhileStmt{Cond: cond, Body: body, MaxIterations: maxIterations}
}

// Repeat creates a fixed-iteration loop.
func Repeat(times int, body ...Stmt) *RepeatStmt {
	return &RepeatStmt{Times: times, Body: body}
}

// Seq creates a program.
func Seq(stmts ...Stmt) *Program { return &Program{Stmts: stmts} }

// ExprString renders an expression in a compact, deterministic form.
func ExprString(e Expr) string {
	if e == nil {
		return "<nil>"
	}
	switch x := e.(type) {
	case *VarExpr:
		return x.Name
	case *NewExpr:
		if x.Label != "" {
			return "new(" + x.Label + ")"
		}
		return "new"
	case *StringExpr:
		return fmt.Sprintf("%q", x.Value)
	case *ExternalStringExpr:
		if x.Label != "" {
			return "external(" + x.Label + ")"
		}
		return "external"
	case *FieldExpr:
		return ExprString(x.Base) + "[" + ExprString(x.Key) + "]"
	case *EqExpr:
		return "(" + ExprString(x.Left) + " == " + ExprString(x.Right) + ")"
	case *NotExpr:
		return "!(" + ExprString(x.X) + ")"
	case *AndExpr:
		return "(" + ExprString(x.Left) + " && " + ExprString(x.Right) + ")"
	case *OrExpr:
		return "(" + ExprString(x.Left) + " || " + ExprString(x.Right) + ")"
	default:
		return "<expr>"
	}
}

// StmtString renders a statement in a compact, deterministic form.
func StmtString(s Stmt) string {
	if s == nil {
		return "<nil>"
	}
	switch x := s.(type) {
	case *AssignStmt:
		return ExprString(x.Target) + " = " + ExprString(x.Value)
	case *ExprStmt:
		return ExprString(x.X)
	case *IfStmt:
		return "if " + ExprString(x.Cond) + " { ... }"
	case *WhileStmt:
		return fmt.Sprintf("while %s (max %d) { ... }", ExprString(x.Cond), x.MaxIterations)
	case *RepeatStmt:
		return fmt.Sprintf("repeat %d { ... }", x.Times)
	default:
		return "<stmt>"
	}
}

// ProgramString renders a program one statement per line.
func ProgramString(p *Program) string {
	if p == nil {
		return "<nil program>"
	}
	lines := make([]string, 0, len(p.Stmts))
	for _, s := range p.Stmts {
		lines = append(lines, StmtString(s))
	}
	return strings.Join(lines, "\n")
}

// SortedKeys returns the keys of m in deterministic order.
func sortedStringSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
