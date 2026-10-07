package iflow

import "fmt"

// ExprKind classifies IR expressions.
type ExprKind int

const (
	ConstExpr ExprKind = iota
	VarExpr
	BinExpr
)

// Expr is a small integer expression over constants and variables.
type Expr struct {
	Kind  ExprKind
	Value int64
	Var   string
	Op    string // add sub mul eq ne lt le gt ge and or
	L, R  *Expr
}

// Const builds a constant expression.
func Const(v int64) *Expr { return &Expr{Kind: ConstExpr, Value: v} }

// V builds a variable reference.
func V(name string) *Expr { return &Expr{Kind: VarExpr, Var: name} }

// Bin builds a binary expression; op is one of
// add sub mul eq ne lt le gt ge and or.
func Bin(op string, l, r *Expr) *Expr { return &Expr{Kind: BinExpr, Op: op, L: l, R: r} }

func (e *Expr) String() string {
	switch e.Kind {
	case ConstExpr:
		return fmt.Sprintf("%d", e.Value)
	case VarExpr:
		return e.Var
	default:
		return fmt.Sprintf("(%s %s %s)", e.L, e.Op, e.R)
	}
}

// OpKind classifies IR instructions.
type OpKind int

const (
	OpAssign OpKind = iota
	OpIf
	OpDeclass
	OpSink
)

// Instr is one IR instruction. Fields are used per kind:
//
//	OpAssign:  Target, Expr
//	OpIf:      Cond, Then, Else
//	OpDeclass: Target, Source, ToLevel, AddReceivers, Auth
//	OpSink:    SinkName, Receiver, Expr
type Instr struct {
	Kind         OpKind
	Target       string
	Expr         *Expr
	Cond         *Expr
	Then         []Instr
	Else         []Instr
	Source       string
	ToLevel      string
	AddReceivers []string
	Auth         string
	SinkName     string
	Receiver     string
}

// Assign builds target := expr.
func Assign(target string, expr *Expr) Instr {
	return Instr{Kind: OpAssign, Target: target, Expr: expr}
}

// If builds a conditional. The analysis deliberately does NOT drop the
// control-dependence label when the branch ends.
func If(cond *Expr, then, els []Instr) Instr {
	return Instr{Kind: OpIf, Cond: cond, Then: then, Else: els}
}

// Declass builds an explicit declassification node: target receives the value
// of source re-labelled to toLevel with additional allowed receivers. It is
// only honored when auth is present in Program.Authorizations and the new
// label is strictly no more restrictive than the old one.
func Declass(target, source, toLevel string, addReceivers []string, auth string) Instr {
	return Instr{Kind: OpDeclass, Target: target, Source: source, ToLevel: toLevel,
		AddReceivers: addReceivers, Auth: auth}
}

// Sink builds an observable output of expr to receiver under the given name.
func Sink(name, receiver string, expr *Expr) Instr {
	return Instr{Kind: OpSink, SinkName: name, Receiver: receiver, Expr: expr}
}

// Program is a script IR unit: labelled inputs, a body, and the set of
// authorization tokens that permit declassification nodes.
type Program struct {
	Inputs         map[string]Label
	Body           []Instr
	Authorizations map[string]bool
}
