package packetvm

import "fmt"

// Size selects how many bytes a fixed-length load reads from the packet.
type Size uint8

const (
	Byte Size = 1
	Half Size = 2
	Word Size = 4
)

// Valid reports whether s is one of the supported fixed load widths.
func (s Size) Valid() bool {
	return s == Byte || s == Half || s == Word
}

// Source tracks the tenant-authored text that produced a node so that
// verifier rejections can be attributed back to a specific expression.
type Source struct {
	ID   int    // stable identifier assigned by Parse
	Text string // verbatim source fragment
	Line int    // 1-based source line
	Col  int    // 1-based source column
}

func (s Source) String() string {
	return fmt.Sprintf("expr#%d %q (line %d col %d)", s.ID, s.Text, s.Line, s.Col)
}

// Numeric is an expression that yields an unsigned 32-bit integer.
type Numeric interface {
	isNumeric()
	Src() Source
}

// Bool is an expression that yields a packet verdict.
type Bool interface {
	isBool()
	Src() Source
}

// Imm is a constant immediate value.
type Imm struct {
	Value uint32
	Pos   Source
}

// Len is the runtime packet length in bytes.
type Len struct{ Pos Source }

// Load is a fixed-width, fixed-offset read from the packet.
type Load struct {
	Offset uint32
	Size   Size
	Pos    Source
}

// Cmp compares two numeric values with an unsigned comparison operator.
type Cmp struct {
	Op       CmpOp
	LHS, RHS Numeric
	Pos      Source
}

// And, Or and Not are the boolean connectives. And/Or short-circuit.
type And struct {
	Terms []Bool
	Pos   Source
}

type Or struct {
	Terms []Bool
	Pos   Source
}

type Not struct {
	Inner Bool
	Pos   Source
}

func (i *Imm) isNumeric()  {}
func (l *Len) isNumeric()  {}
func (l *Load) isNumeric() {}
func (c *Cmp) isBool()     {}
func (a *And) isBool()     {}
func (o *Or) isBool()      {}
func (n *Not) isBool()     {}

func (i *Imm) Src() Source  { return i.Pos }
func (l *Len) Src() Source  { return l.Pos }
func (l *Load) Src() Source { return l.Pos }
func (c *Cmp) Src() Source  { return c.Pos }
func (a *And) Src() Source  { return a.Pos }
func (o *Or) Src() Source   { return o.Pos }
func (n *Not) Src() Source  { return n.Pos }

// CmpOp is an unsigned comparison.
type CmpOp uint8

const (
	Eq CmpOp = iota
	Ne
	Lt
	Le
	Gt
	Ge
)

func (op CmpOp) String() string {
	switch op {
	case Eq:
		return "=="
	case Ne:
		return "!="
	case Lt:
		return "<"
	case Le:
		return "<="
	case Gt:
		return ">"
	case Ge:
		return ">="
	default:
		return fmt.Sprintf("cmp(%d)", uint8(op))
	}
}

// negated returns the operator that yields the complement truth value.
func (op CmpOp) negated() CmpOp {
	switch op {
	case Eq:
		return Ne
	case Ne:
		return Eq
	case Lt:
		return Ge
	case Le:
		return Gt
	case Gt:
		return Le
	case Ge:
		return Lt
	default:
		panic("packetvm: invalid comparison operator")
	}
}

// Compare evaluates an unsigned comparison.
func Compare(op CmpOp, a, b uint32) bool {
	switch op {
	case Eq:
		return a == b
	case Ne:
		return a != b
	case Lt:
		return a < b
	case Le:
		return a <= b
	case Gt:
		return a > b
	case Ge:
		return a >= b
	default:
		panic("packetvm: invalid comparison operator")
	}
}

// Rule is one tenant-uploaded filter: a named boolean predicate.
type Rule struct {
	Name string
	Body Bool
}
