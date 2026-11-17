package netsec

import "fmt"

// MaxInputs is the largest number of input bytes accepted by a Graph.
const MaxInputs = 12

// NodeID identifies a node in one SSA graph.
//
// Error.Source is deliberately also a NodeID: a rewrite may assign unrelated
// local IDs to its nodes, so both versions should put the ID of the original
// source operation in every error they can report for that operation.
type NodeID uint32

// Op is an SSA operation.
type Op uint8

const (
	OpInput Op = iota + 1
	OpConst
	OpAdd
	OpSub
	OpMul
	OpTrunc
	OpExtend
	OpCmp
	OpOverflow
	OpBranch
	OpReturn
	OpError
)

func (op Op) String() string {
	switch op {
	case OpInput:
		return "input"
	case OpConst:
		return "const"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpTrunc:
		return "trunc"
	case OpExtend:
		return "extend"
	case OpCmp:
		return "cmp"
	case OpOverflow:
		return "overflow"
	case OpBranch:
		return "branch"
	case OpReturn:
		return "return"
	case OpError:
		return "error"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// IsArithmetic reports whether op is a wrapping integer arithmetic node.
func (op Op) IsArithmetic() bool {
	return op == OpAdd || op == OpSub || op == OpMul
}

// IsTerminal reports whether op ends a control-flow path.
func (op Op) IsTerminal() bool {
	return op == OpReturn || op == OpError
}

// CmpPred is an integer comparison predicate.
type CmpPred uint8

const (
	CmpEq CmpPred = iota + 1
	CmpNe
	CmpLt
	CmpLe
	CmpGt
	CmpGe
)

func (p CmpPred) String() string {
	switch p {
	case CmpEq:
		return "eq"
	case CmpNe:
		return "ne"
	case CmpLt:
		return "lt"
	case CmpLe:
		return "le"
	case CmpGt:
		return "gt"
	case CmpGe:
		return "ge"
	default:
		return fmt.Sprintf("cmp(%d)", uint8(p))
	}
}

// OverflowCheck describes the range used by an OpOverflow node.
//
// Width is the width of the original operation whose overflow is being
// checked. For a signed check the mathematical result must fit in
// [-2^(Width-1), 2^(Width-1)-1]; for an unsigned check it must fit in
// [0, 2^Width-1]. The arithmetic node itself still wraps to Node.Width.
type OverflowCheck struct {
	Width  uint8
	Signed bool
}

// Node is one SSA instruction.
//
// The operands used by each operation are:
//
//	OpInput              InputIndex
//	OpConst              Value, Width
//	OpAdd/OpSub/OpMul    X, Y, Width, optional Check
//	OpTrunc              X, Width
//	OpExtend             X, Width, Signed
//	OpCmp                X, Y, Pred, Signed
//	OpOverflow           X (which must be arithmetic and have Check != nil)
//	OpBranch             X (condition), Then, Else
//	OpReturn             X
//	OpError              Source
//
// A branch takes Then when X is non-zero and Else otherwise.
type Node struct {
	ID         NodeID
	Op         Op
	Width      uint8
	Signed     bool
	InputIndex int
	Value      uint64
	X          NodeID
	Y          NodeID
	Pred       CmpPred
	Then       NodeID
	Else       NodeID
	Source     NodeID
	Check      *OverflowCheck
}

// Graph is an acyclic SSA/control-flow graph. Input bytes are unsigned and
// therefore have width 8. Arithmetic nodes perform two's-complement wrapping;
// overflow checks are explicit OpOverflow predicates.
type Graph struct {
	Name       string
	InputCount int
	Entry      NodeID
	Nodes      []Node
}

// OutcomeKind distinguishes a normal return from an error.
type OutcomeKind uint8

const (
	OutcomeReturn OutcomeKind = iota + 1
	OutcomeError
)

func (k OutcomeKind) String() string {
	switch k {
	case OutcomeReturn:
		return "return"
	case OutcomeError:
		return "error"
	default:
		return fmt.Sprintf("outcome(%d)", uint8(k))
	}
}

// Outcome is the observable result of one concrete execution. Return values
// are sign-extended to int64, so a 16-bit -1 and a 32-bit -1 compare equal.
// Errors are compared by their source node only.
type Outcome struct {
	Kind   OutcomeKind
	Value  int64
	Width  uint8
	Source NodeID
}

// IsError reports whether the outcome is an error.
func (o Outcome) IsError() bool {
	return o.Kind == OutcomeError
}

// Equal reports whether two outcomes are observationally equivalent.
func (o Outcome) Equal(other Outcome) bool {
	if o.Kind != other.Kind {
		return false
	}
	if o.Kind == OutcomeError {
		return o.Source == other.Source
	}
	return o.Kind == OutcomeReturn && o.Value == other.Value
}

func (o Outcome) String() string {
	if o.Kind == OutcomeError {
		return fmt.Sprintf("error(source=%d)", o.Source)
	}
	return fmt.Sprintf("return(%d)", o.Value)
}

// CheckOverflow reports whether the mathematical result of arithmetic node n
// with the supplied operands is outside n.Check's range. It is exported so
// callers can build and test their own overflow predicates.
func CheckOverflow(n Node, x, y uint64) bool {
	if n.Check == nil {
		return false
	}
	return checkOverflow(n.Op, n.Width, x, y, *n.Check)
}
