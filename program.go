package netsec

import "fmt"

// MaxNodes is the largest control-flow graph accepted by this package.
const MaxNodes = 48

// Register identifies one of the 256 byte-sized registers. Register values
// are always reduced modulo 256.
type Register uint8

// Value is the eight-bit value held by a register.
type Value uint8

// Op enumerates the finite instruction set understood by the analysis.
type Op uint8

const (
	OpNop Op = iota
	OpConst
	OpCopy
	OpAdd
	OpSub
	OpMul
	OpEqBranch
	OpSelect
	OpPublicWrite
	OpDeclassify
	OpReturn
	OpAbort
)

// Short aliases are provided for hosts that prefer instruction names over
// opcode names.
const (
	Nop         = OpNop
	Const       = OpConst
	Copy        = OpCopy
	Add         = OpAdd
	Sub         = OpSub
	Mul         = OpMul
	EqBranch    = OpEqBranch
	BranchEq    = OpEqBranch
	Select      = OpSelect
	PublicWrite = OpPublicWrite
	Declassify  = OpDeclassify
	Return      = OpReturn
	Abort       = OpAbort
)

// EdgeKind distinguishes ordinary control-flow edges from abnormal/exception
// edges. Both kinds propagate the same control labels; the distinction is
// retained so reports and hosts can identify exception paths.
type EdgeKind uint8

const (
	NormalEdge EdgeKind = iota
	AbnormalEdge
)

// Edge is one CFG successor.
type Edge struct {
	To       int      `json:"to"`
	Kind     EdgeKind `json:"kind,omitempty"`
	Abnormal bool     `json:"abnormal,omitempty"`
}

// Instruction is one node payload.
//
// Semantics by opcode:
//
//	OpConst       dst = imm
//	OpCopy        dst = src1
//	OpAdd/Sub/Mul dst = src1 op src2 (mod 256)
//	OpEqBranch    branch on src1 == src2; succ[0] is true, succ[1] is false
//	OpSelect      dst = src1 != 0 ? src2 : src3
//	OpPublicWrite append src1 to the output named by Output
//	OpDeclassify  remove declared labels from dst
//	OpReturn      terminate normally
//	OpAbort       terminate abnormally
//
// Labels on OpDeclassify are the labels the instruction asks the host to
// declassify. They have no meaning on other instructions.
type Instruction struct {
	Op     Op       `json:"op"`
	Dst    Register `json:"dst,omitempty"`
	Src1   Register `json:"src1,omitempty"`
	Src2   Register `json:"src2,omitempty"`
	Src3   Register `json:"src3,omitempty"`
	Imm    uint8    `json:"imm,omitempty"`
	Labels LabelSet `json:"labels,omitempty"`
	Output int      `json:"output,omitempty"`
}

// EdgeList returns the instruction's explicit successors. Edges takes
// precedence over Succ, which in turn takes precedence over Successors.
// Abnormal is accepted as a parallel flag list when Edges is unused.
type Node struct {
	Instr        Instruction `json:"instr"`
	Edges        []Edge      `json:"edges,omitempty"`
	Succ         []int       `json:"succ,omitempty"`
	Successors   []int       `json:"successors,omitempty"`
	Abnormal     []bool      `json:"abnormal,omitempty"`
	Return       bool        `json:"return,omitempty"`
	AbnormalExit bool        `json:"abnormal_exit,omitempty"`
}

// Input declares an initial register value and the secret labels attached to
// that value. A non-empty Labels set denotes one secret boolean input for the
// concrete reference executor.
type Input struct {
	Register Register `json:"register"`
	Value    uint8    `json:"value,omitempty"`
	Labels   LabelSet `json:"labels,omitempty"`
}

// Program is a finite control-flow graph. Nodes are indexed by their position
// in Nodes. Entry defaulting to zero makes a normal literal convenient.
type Program struct {
	Entry  int     `json:"entry"`
	Nodes  []Node  `json:"nodes"`
	Inputs []Input `json:"inputs,omitempty"`
}

func (n Node) outgoing() (targets []int, abnormal []bool) {
	if len(n.Edges) > 0 {
		targets = make([]int, 0, len(n.Edges))
		abnormal = make([]bool, 0, len(n.Edges))
		for _, edge := range n.Edges {
			targets = append(targets, edge.To)
			abnormal = append(abnormal, edge.Abnormal || edge.Kind == AbnormalEdge)
		}
		return targets, abnormal
	}
	switch {
	case len(n.Succ) > 0:
		targets = append([]int(nil), n.Succ...)
	case len(n.Successors) > 0:
		targets = append([]int(nil), n.Successors...)
	}
	abnormal = make([]bool, len(targets))
	for i := range abnormal {
		if len(n.Abnormal) == len(targets) {
			abnormal[i] = n.Abnormal[i]
		}
	}
	return targets, abnormal
}

func (n Node) terminal() bool {
	return n.Return || n.AbnormalExit || n.Instr.Op == OpReturn || n.Instr.Op == OpAbort
}

// Outgoing returns the effective successors of a node.
func (n Node) Outgoing() []int {
	targets, _ := n.outgoing()
	return targets
}

// OutgoingEdges returns the effective successors together with their kinds.
func (n Node) OutgoingEdges() []Edge {
	targets, abnormal := n.outgoing()
	edges := make([]Edge, len(targets))
	for i := range targets {
		edges[i] = Edge{To: targets[i]}
		if abnormal[i] {
			edges[i].Kind = AbnormalEdge
			edges[i].Abnormal = true
		}
	}
	return edges
}

// NewProgram constructs a program whose entry is node zero.
func NewProgram(nodes ...Node) *Program {
	return &Program{Entry: 0, Nodes: nodes}
}

// AddNode appends a node and returns its index.
func (p *Program) AddNode(instr Instruction, succ ...int) int {
	p.Nodes = append(p.Nodes, Node{Instr: instr, Succ: succ})
	return len(p.Nodes) - 1
}

// AddNodeWithEdges appends a node with explicit edge kinds.
func (p *Program) AddNodeWithEdges(instr Instruction, edges ...Edge) int {
	p.Nodes = append(p.Nodes, Node{Instr: instr, Edges: edges})
	return len(p.Nodes) - 1
}

// AddInput declares an initial value/label binding.
func (p *Program) AddInput(reg Register, value uint8, labels ...Label) {
	p.Inputs = append(p.Inputs, Input{
		Register: reg,
		Value:    value,
		Labels:   Labels(labels...),
	})
}

// NewConst returns a constant instruction.
func NewConst(dst Register, value uint8) Instruction {
	return Instruction{Op: OpConst, Dst: dst, Imm: value}
}

// NewCopy returns a register-copy instruction.
func NewCopy(dst, src Register) Instruction {
	return Instruction{Op: OpCopy, Dst: dst, Src1: src}
}

// NewAdd returns a modulo-256 addition instruction.
func NewAdd(dst, left, right Register) Instruction {
	return Instruction{Op: OpAdd, Dst: dst, Src1: left, Src2: right}
}

// NewSub returns a modulo-256 subtraction instruction.
func NewSub(dst, left, right Register) Instruction {
	return Instruction{Op: OpSub, Dst: dst, Src1: left, Src2: right}
}

// NewMul returns a modulo-256 multiplication instruction.
func NewMul(dst, left, right Register) Instruction {
	return Instruction{Op: OpMul, Dst: dst, Src1: left, Src2: right}
}

// NewEqBranch returns an equality branch. The first successor is the true
// edge and the second successor is the false edge.
func NewEqBranch(left, right Register) Instruction {
	return Instruction{Op: OpEqBranch, Src1: left, Src2: right}
}

// NewSelect returns a three-input select instruction.
func NewSelect(dst, condition, ifTrue, ifFalse Register) Instruction {
	return Instruction{Op: OpSelect, Dst: dst, Src1: condition, Src2: ifTrue, Src3: ifFalse}
}

// NewPublicWrite returns an instruction that writes src to output zero.
func NewPublicWrite(src Register) Instruction {
	return Instruction{Op: OpPublicWrite, Src1: src}
}

// NewPublicWriteTo returns an instruction that writes src to a named output.
func NewPublicWriteTo(output int, src Register) Instruction {
	return Instruction{Op: OpPublicWrite, Src1: src, Output: output}
}

// NewDeclassify returns an instruction that requests removal of labels from
// reg.
func NewDeclassify(reg Register, labels ...Label) Instruction {
	return Instruction{Op: OpDeclassify, Dst: reg, Labels: Labels(labels...)}
}

// NewReturn returns a normal-return instruction.
func NewReturn() Instruction {
	return Instruction{Op: OpReturn}
}

// NewAbort returns an abnormal-exit instruction.
func NewAbort() Instruction {
	return Instruction{Op: OpAbort}
}

func asProgram(value any) (*Program, error) {
	switch program := value.(type) {
	case *Program:
		if program == nil {
			return nil, fmt.Errorf("%w: nil program", ErrInvalidProgram)
		}
		return program, nil
	case Program:
		return &program, nil
	default:
		return nil, fmt.Errorf("%w: unsupported program type %T", ErrInvalidProgram, value)
	}
}

// Validate checks the finite-CFG bounds and instruction shape.
func (p *Program) Validate() error {
	return validateProgram(p)
}

func validateProgram(p *Program) error {
	if p == nil {
		return fmt.Errorf("%w: nil program", ErrInvalidProgram)
	}
	if len(p.Nodes) == 0 {
		return fmt.Errorf("%w: empty program", ErrInvalidProgram)
	}
	if len(p.Nodes) > MaxNodes {
		return fmt.Errorf("%w: %d nodes exceeds limit %d", ErrTooManyNodes, len(p.Nodes), MaxNodes)
	}
	if p.Entry < 0 || p.Entry >= len(p.Nodes) {
		return fmt.Errorf("%w: entry %d is outside [0,%d)", ErrInvalidProgram, p.Entry, len(p.Nodes))
	}
	seenInputs := make(map[Register]struct{}, len(p.Inputs))
	for i, input := range p.Inputs {
		if _, exists := seenInputs[input.Register]; exists {
			return fmt.Errorf("%w: duplicate input register %d at input %d", ErrInvalidProgram, input.Register, i)
		}
		seenInputs[input.Register] = struct{}{}
	}
	for index, node := range p.Nodes {
		if node.Instr.Op > OpAbort {
			return fmt.Errorf("%w: node %d has unknown op %d", ErrInvalidProgram, index, node.Instr.Op)
		}
		if node.Instr.Output < 0 {
			return fmt.Errorf("%w: node %d has negative output id", ErrInvalidProgram, index)
		}
		targets, _ := node.outgoing()
		if len(node.Abnormal) != 0 && len(node.Abnormal) != len(targets) {
			return fmt.Errorf("%w: node %d abnormal flag count %d does not match %d successors",
				ErrInvalidProgram, index, len(node.Abnormal), len(targets))
		}
		for _, target := range targets {
			if target < 0 || target >= len(p.Nodes) {
				return fmt.Errorf("%w: node %d targets node %d outside [0,%d)",
					ErrInvalidProgram, index, target, len(p.Nodes))
			}
		}
		if node.Instr.Op == OpEqBranch && len(targets) != 2 {
			return fmt.Errorf("%w: branch node %d must have exactly two successors, got %d",
				ErrInvalidProgram, index, len(targets))
		}
	}
	return nil
}
