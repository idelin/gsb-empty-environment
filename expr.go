package unitinfer

import "fmt"

// Kind classifies expression nodes.
type Kind int

const (
	KindNum  Kind = iota // literal with a known unit
	KindVar              // program variable (unit may be unknown)
	KindBin              // binary + - * /
	KindPow              // integer power
	KindCall             // function application
)

// Op is a binary operator.
type Op int

const (
	OpAdd Op = iota
	OpSub
	OpMul
	OpDiv
)

func (o Op) String() string {
	switch o {
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	}
	return "?"
}

// Expr is an expression node. Nodes are created via Builder, which assigns
// unique ids used in constraint provenance.
type Expr struct {
	Kind Kind
	Op   Op
	Lit  Term     // KindNum: unit of the literal
	Name string   // KindVar
	L, R *Expr    // KindBin
	Base *Expr    // KindPow
	Exp  int      // KindPow: integer exponent
	Sig  *FuncSig // KindCall
	Args []*Expr  // KindCall
	id   int
}

// ID returns the unique node id.
func (e *Expr) ID() int { return e.id }

// String renders the expression (used in constraint descriptions).
func (e *Expr) String() string {
	switch e.Kind {
	case KindNum:
		return fmt.Sprintf("lit#%d", e.id)
	case KindVar:
		return e.Name
	case KindBin:
		return fmt.Sprintf("(%s %s %s)", e.L.String(), e.Op, e.R.String())
	case KindPow:
		return fmt.Sprintf("(%s)^%d", e.Base.String(), e.Exp)
	case KindCall:
		s := e.Sig.Name + "("
		for i, a := range e.Args {
			if i > 0 {
				s += ", "
			}
			s += a.String()
		}
		return s + ")"
	}
	return "?"
}

// FuncSig is a function signature whose parameter and result units are
// linear terms that may mention signature-local unit variables
// (ids 0..NVars-1). Each call site instantiates them with fresh variables,
// giving let-polymorphism over units.
type FuncSig struct {
	Name   string
	NVars  int    // number of signature unit variables
	Params []Term // parameter units, may reference sig vars
	Result Term   // result unit, may reference sig vars
}

// Builder allocates expression nodes with unique ids.
type Builder struct{ next int }

// NewBuilder returns a fresh Builder.
func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) alloc(e *Expr) *Expr {
	e.id = b.next
	b.next++
	return e
}

// Num builds a literal whose unit is the constant term unit.
func (b *Builder) Num(unit Term) *Expr { return b.alloc(&Expr{Kind: KindNum, Lit: unit}) }

// Var builds a reference to a program variable.
func (b *Builder) Var(name string) *Expr { return b.alloc(&Expr{Kind: KindVar, Name: name}) }

// Bin builds a binary expression.
func (b *Builder) Bin(op Op, l, r *Expr) *Expr {
	return b.alloc(&Expr{Kind: KindBin, Op: op, L: l, R: r})
}

// Pow builds base^k for a constant integer exponent k.
func (b *Builder) Pow(base *Expr, k int) *Expr {
	return b.alloc(&Expr{Kind: KindPow, Base: base, Exp: k})
}

// Call builds an application of a unit-polymorphic function.
func (b *Builder) Call(sig *FuncSig, args ...*Expr) *Expr {
	return b.alloc(&Expr{Kind: KindCall, Sig: sig, Args: args})
}
