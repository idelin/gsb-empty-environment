package netsec

import (
	"fmt"
)

// Op identifies an operation in the analysis IR.
type Op uint8

const (
	OpSecretInput Op = iota
	OpPublicInput
	OpConst
	OpAnd
	OpOr
	OpXor
	OpNot
	OpShl
	OpShr
	OpAdd
	OpSub
	OpMul
	OpSelect
	OpPublicMask
	OpLookup
	OpBranch
	OpCall
)

func (op Op) String() string {
	switch op {
	case OpSecretInput:
		return "secret-input"
	case OpPublicInput:
		return "public-input"
	case OpConst:
		return "const"
	case OpAnd:
		return "and"
	case OpOr:
		return "or"
	case OpXor:
		return "xor"
	case OpNot:
		return "not"
	case OpShl:
		return "shl"
	case OpShr:
		return "shr"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpSelect:
		return "select"
	case OpPublicMask:
		return "public-mask"
	case OpLookup:
		return "lookup"
	case OpBranch:
		return "branch"
	case OpCall:
		return "call"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// CallSpec describes a summarized library call at one call site.
//
// AllowedPublic is a caller declaration over argument bits: those bits are
// treated as public for the callee's ordinary timing dependencies.  It does
// not launder secret address bits.  Set AllowPublicAddress only when the
// caller's contract intentionally permits secret-derived addresses.
type CallSpec struct {
	Library            string
	Site               string
	AllowedPublic      []Mask
	AllowPublicAddress bool
	// TimingBits and AddressBits optionally add call-site-specific
	// dependencies to the library summary (one mask per parameter).  They
	// are unioned with ParamSummary.TimingBits/AddressBits.
	TimingBits  []Mask
	AddressBits []Mask
}

// Node is one instruction in the composed-function IR.  Args refer to earlier
// node indices.  Width is required for every node that produces a value.
type Node struct {
	Name   string
	Op     Op
	Width  int
	Args   []int
	Value  uint64 // OpConst
	Shift  uint   // OpShl, OpShr
	Public Mask   // OpPublicMask mask; OpLookup caller-declared public index bits
	// OpLookup: whether Public may excuse address bits.  The secure default is
	// false because a public mask does not erase secret provenance.
	AllowPublicAddress bool
	Call               *CallSpec

	index int
}

// Program is a straight-line composed function.  Nodes execute in order.
type Program struct {
	Name  string
	Nodes []Node
}

// Build validates and copies the program, assigning stable node indices.
func (p *Program) Build() (*Program, error) {
	if p == nil {
		return nil, fmt.Errorf("nil program")
	}
	out := &Program{Name: p.Name, Nodes: make([]Node, len(p.Nodes))}
	if out.Name == "" {
		out.Name = "composed"
	}
	names := make(map[string]int, len(p.Nodes))
	for i := range p.Nodes {
		node := p.Nodes[i]
		node.index = i
		if node.Name == "" {
			node.Name = fmt.Sprintf("n%d", i)
		}
		if previous, exists := names[node.Name]; exists {
			return nil, fmt.Errorf("duplicate node name %q (nodes %d and %d)", node.Name, previous, i)
		}
		names[node.Name] = i
		if node.Call != nil {
			call := *node.Call
			if call.Site == "" {
				call.Site = node.Name
			}
			node.Call = &call
		}
		out.Nodes[i] = node
	}
	return out, nil
}

// Node returns a node by name.
func (p *Program) Node(name string) (Node, bool) {
	for i := range p.Nodes {
		if p.Nodes[i].Name == name {
			return p.Nodes[i], true
		}
	}
	return Node{}, false
}

// Builder appends nodes to a Program while generating stable names.
type Builder struct {
	program Program
	next    int
}

// NewBuilder starts a program builder.
func NewBuilder(name string) *Builder {
	return &Builder{program: Program{Name: name}}
}

func (b *Builder) add(name string, op Op, width int, args []int, mutate func(*Node)) int {
	if name == "" {
		name = fmt.Sprintf("n%d", b.next)
	}
	node := Node{Name: name, Op: op, Width: width, Args: args}
	if mutate != nil {
		mutate(&node)
	}
	b.program.Nodes = append(b.program.Nodes, node)
	b.next++
	return len(b.program.Nodes) - 1
}

// Secret adds a secret input and returns its node index.
func (b *Builder) Secret(name string, width int) int {
	return b.add(name, OpSecretInput, width, nil, nil)
}

// Public adds a public input and returns its node index.
func (b *Builder) Public(name string, width int) int {
	return b.add(name, OpPublicInput, width, nil, nil)
}

// Const adds a constant and returns its node index.
func (b *Builder) Const(name string, width int, value uint64) int {
	return b.add(name, OpConst, width, nil, func(n *Node) { n.Value = value })
}

// And adds a bitwise AND.
func (b *Builder) And(name string, x, y int) int {
	return b.add(name, OpAnd, b.width(x), []int{x, y}, nil)
}

// Or adds a bitwise OR.
func (b *Builder) Or(name string, x, y int) int {
	return b.add(name, OpOr, b.width(x), []int{x, y}, nil)
}

// Xor adds a bitwise XOR.
func (b *Builder) Xor(name string, x, y int) int {
	return b.add(name, OpXor, b.width(x), []int{x, y}, nil)
}

// Not adds a bitwise NOT.
func (b *Builder) Not(name string, x int) int {
	return b.add(name, OpNot, b.width(x), []int{x}, nil)
}

// Shl adds a left shift.
func (b *Builder) Shl(name string, x int, shift uint) int {
	return b.add(name, OpShl, b.width(x), []int{x}, func(n *Node) { n.Shift = shift })
}

// Shr adds a right shift.
func (b *Builder) Shr(name string, x int, shift uint) int {
	return b.add(name, OpShr, b.width(x), []int{x}, func(n *Node) { n.Shift = shift })
}

// Add adds a wrapping addition.
func (b *Builder) Add(name string, x, y int) int {
	return b.add(name, OpAdd, b.width(x), []int{x, y}, nil)
}

// Sub adds a wrapping subtraction.
func (b *Builder) Sub(name string, x, y int) int {
	return b.add(name, OpSub, b.width(x), []int{x, y}, nil)
}

// Mul adds a wrapping multiplication.
func (b *Builder) Mul(name string, x, y int) int {
	return b.add(name, OpMul, b.width(x), []int{x, y}, nil)
}

// Select adds a data selection (cmov).  Arguments are condition, true value,
// false value.
func (b *Builder) Select(name string, cond, whenTrue, whenFalse int) int {
	return b.add(name, OpSelect, b.width(whenTrue), []int{cond, whenTrue, whenFalse}, nil)
}

// PublicMask adds a public-mask/declassification operation.  The value is
// ANDed with mask, and surviving dependencies are declassified for ordinary
// timing checks.  They remain secret for address checks.
func (b *Builder) PublicMask(name string, x int, mask Mask) int {
	return b.add(name, OpPublicMask, b.width(x), []int{x}, func(n *Node) { n.Public = mask })
}

// Lookup adds a table lookup whose result depends on the index.  The index
// must be public unless Public/AllowPublicAddress explicitly changes the
// contract.
func (b *Builder) Lookup(name string, index int, width int) int {
	return b.add(name, OpLookup, width, []int{index}, nil)
}

// LookupTable is Lookup with an abstract table value as the first argument.
func (b *Builder) LookupTable(name string, table, index int, width int) int {
	return b.add(name, OpLookup, width, []int{table, index}, nil)
}

// LookupContract sets the public-index declaration on a lookup node.
func (b *Builder) LookupContract(name string, public Mask, allowPublicAddress bool) {
	for i := range b.program.Nodes {
		if b.program.Nodes[i].Name == name {
			b.program.Nodes[i].Public = public
			b.program.Nodes[i].AllowPublicAddress = allowPublicAddress
			return
		}
	}
}

// Branch records a control-flow decision.
func (b *Builder) Branch(name string, cond int) int {
	return b.add(name, OpBranch, 0, []int{cond}, nil)
}

// Call adds a summarized library call.
func (b *Builder) Call(name string, spec CallSpec, args ...int) int {
	width := 0
	for _, arg := range args {
		if w := b.width(arg); w > width {
			width = w
		}
	}
	return b.add(name, OpCall, width, args, func(n *Node) {
		call := spec
		n.Call = &call
	})
}

// Build returns the program.  Validation happens during analysis.
func (b *Builder) Build() *Program {
	program := b.program
	program.Nodes = append([]Node(nil), program.Nodes...)
	return &program
}

func (b *Builder) width(index int) int {
	if index < 0 || index >= len(b.program.Nodes) {
		return 0
	}
	return b.program.Nodes[index].Width
}

// String renders a node compactly.
func (n Node) String() string {
	var extra string
	switch n.Op {
	case OpConst:
		extra = fmt.Sprintf(" value=%#x", n.Value)
	case OpShl, OpShr:
		extra = fmt.Sprintf(" shift=%d", n.Shift)
	case OpPublicMask, OpLookup:
		extra = fmt.Sprintf(" public=%s", maskString(n.Public))
	case OpCall:
		if n.Call != nil {
			extra = fmt.Sprintf(" library=%s allowed=%v", n.Call.Library, n.Call.AllowedPublic)
		}
	}
	args := ""
	if len(n.Args) > 0 {
		args = fmt.Sprintf(" args=%v", n.Args)
	}
	return fmt.Sprintf("%s %s(width=%d%s%s)", n.Op, n.Name, n.Width, args, extra)
}
