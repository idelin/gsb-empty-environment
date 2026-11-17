package netsec

import (
	"errors"
	"fmt"
)

// ErrInvalidGraph is returned (possibly wrapped) when a graph is malformed.
var ErrInvalidGraph = errors.New("netsec: invalid graph")

// Builder is a small convenience builder for acyclic SSA graphs. Node IDs are
// assigned in emission order. Branch targets may be any existing nodes, so it
// is convenient to emit terminal nodes first and then emit the entry block.
type Builder struct {
	inputCount int
	nodes      []Node
}

// NewBuilder starts a graph with inputCount unsigned input bytes.
func NewBuilder(inputCount int) *Builder {
	return &Builder{inputCount: inputCount}
}

// Emit appends a node and assigns its ID.
func (b *Builder) Emit(n Node) NodeID {
	id := NodeID(len(b.nodes))
	n.ID = id
	b.nodes = append(b.nodes, n)
	return id
}

// Input emits an 8-bit unsigned input node.
func (b *Builder) Input(index int) NodeID {
	return b.Emit(Node{Op: OpInput, Width: 8, InputIndex: index})
}

// Const emits a constant of the given width.
func (b *Builder) Const(width uint8, value uint64) NodeID {
	return b.Emit(Node{Op: OpConst, Width: width, Value: value})
}

// Add emits a wrapping add.
func (b *Builder) Add(x, y NodeID, width uint8) NodeID {
	return b.Emit(Node{Op: OpAdd, Width: width, X: x, Y: y, Signed: true})
}

// AddChecked emits a wrapping add with an overflow-check range.
func (b *Builder) AddChecked(x, y NodeID, width, checkWidth uint8, signed bool) NodeID {
	return b.Emit(Node{
		Op: OpAdd, Width: width, X: x, Y: y, Signed: true,
		Check: &OverflowCheck{Width: checkWidth, Signed: signed},
	})
}

// Sub emits a wrapping subtraction.
func (b *Builder) Sub(x, y NodeID, width uint8) NodeID {
	return b.Emit(Node{Op: OpSub, Width: width, X: x, Y: y, Signed: true})
}

// SubChecked emits a wrapping subtraction with an overflow-check range.
func (b *Builder) SubChecked(x, y NodeID, width, checkWidth uint8, signed bool) NodeID {
	return b.Emit(Node{
		Op: OpSub, Width: width, X: x, Y: y, Signed: true,
		Check: &OverflowCheck{Width: checkWidth, Signed: signed},
	})
}

// Mul emits a wrapping multiplication.
func (b *Builder) Mul(x, y NodeID, width uint8) NodeID {
	return b.Emit(Node{Op: OpMul, Width: width, X: x, Y: y, Signed: true})
}

// MulChecked emits a wrapping multiplication with an overflow-check range.
func (b *Builder) MulChecked(x, y NodeID, width, checkWidth uint8, signed bool) NodeID {
	return b.Emit(Node{
		Op: OpMul, Width: width, X: x, Y: y, Signed: true,
		Check: &OverflowCheck{Width: checkWidth, Signed: signed},
	})
}

// Trunc emits a truncation to width.
func (b *Builder) Trunc(x NodeID, width uint8) NodeID {
	return b.Emit(Node{Op: OpTrunc, Width: width, X: x})
}

// Extend emits a zero- or sign-extension to width.
func (b *Builder) Extend(x NodeID, width uint8, signed bool) NodeID {
	return b.Emit(Node{Op: OpExtend, Width: width, X: x, Signed: signed})
}

// Cmp emits a one-bit comparison.
func (b *Builder) Cmp(x, y NodeID, pred CmpPred, signed bool) NodeID {
	return b.Emit(Node{Op: OpCmp, Width: 1, X: x, Y: y, Pred: pred, Signed: signed})
}

// Overflow emits a one-bit predicate for the check attached to x.
func (b *Builder) Overflow(x NodeID) NodeID {
	return b.Emit(Node{Op: OpOverflow, Width: 1, X: x})
}

// Branch emits a two-way control-flow branch.
func (b *Builder) Branch(cond, then, otherwise NodeID) NodeID {
	return b.Emit(Node{Op: OpBranch, X: cond, Then: then, Else: otherwise})
}

// Return emits a normal return.
func (b *Builder) Return(x NodeID) NodeID {
	return b.Emit(Node{Op: OpReturn, X: x})
}

// Error emits an error with a stable source node ID.
func (b *Builder) Error(source NodeID) NodeID {
	return b.Emit(Node{Op: OpError, Source: source})
}

// Build validates and returns the graph rooted at entry.
func (b *Builder) Build(entry NodeID) (*Graph, error) {
	g := &Graph{
		InputCount: b.inputCount,
		Entry:      entry,
		Nodes:      append([]Node(nil), b.nodes...),
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// MustBuild is Build but panics on an invalid graph.
func (b *Builder) MustBuild(entry NodeID) *Graph {
	g, err := b.Build(entry)
	if err != nil {
		panic(err)
	}
	return g
}

// Validate checks the SSA invariants and that every reachable control path
// ends in a return or an error.
func (g *Graph) Validate() error {
	if g == nil {
		return fmt.Errorf("%w: nil graph", ErrInvalidGraph)
	}
	if g.InputCount < 0 || g.InputCount > MaxInputs {
		return fmt.Errorf("%w: input count %d is outside [0,%d]", ErrInvalidGraph, g.InputCount, MaxInputs)
	}
	if len(g.Nodes) == 0 {
		return fmt.Errorf("%w: graph has no nodes", ErrInvalidGraph)
	}

	byID := make(map[NodeID]Node, len(g.Nodes))
	for i, n := range g.Nodes {
		if _, ok := byID[n.ID]; ok {
			return fmt.Errorf("%w: duplicate node id %d", ErrInvalidGraph, n.ID)
		}
		byID[n.ID] = n
		if n.Op == 0 {
			return fmt.Errorf("%w: node %d has no operation", ErrInvalidGraph, n.ID)
		}
		if n.Op != OpBranch && n.Op != OpReturn && n.Op != OpError {
			if n.Width == 0 || n.Width > 64 {
				return fmt.Errorf("%w: node %d has width %d", ErrInvalidGraph, n.ID, n.Width)
			}
		}
		if n.Check != nil && !n.Op.IsArithmetic() {
			return fmt.Errorf("%w: node %d has a check but is not arithmetic", ErrInvalidGraph, n.ID)
		}
		if n.Check != nil && n.Width > 32 {
			return fmt.Errorf("%w: checked arithmetic node %d has width %d, want at most 32", ErrInvalidGraph, n.ID, n.Width)
		}
		if n.Check != nil && (n.Check.Width == 0 || n.Check.Width > 64) {
			return fmt.Errorf("%w: node %d has check width %d", ErrInvalidGraph, n.ID, n.Check.Width)
		}
		_ = i
	}

	ref := func(from NodeID, operand NodeID, what string) error {
		if _, ok := byID[operand]; !ok {
			return fmt.Errorf("%w: node %d references missing %s node %d", ErrInvalidGraph, from, what, operand)
		}
		return nil
	}
	for _, n := range g.Nodes {
		switch n.Op {
		case OpInput:
			if n.Width != 8 {
				return fmt.Errorf("%w: input node %d has width %d, want 8", ErrInvalidGraph, n.ID, n.Width)
			}
			if n.InputIndex < 0 || n.InputIndex >= g.InputCount {
				return fmt.Errorf("%w: input node %d has index %d", ErrInvalidGraph, n.ID, n.InputIndex)
			}
		case OpConst:
			// Width was checked above.
		case OpAdd, OpSub, OpMul:
			if err := ref(n.ID, n.X, "x"); err != nil {
				return err
			}
			if err := ref(n.ID, n.Y, "y"); err != nil {
				return err
			}
			if byID[n.X].Width != n.Width || byID[n.Y].Width != n.Width {
				return fmt.Errorf("%w: arithmetic node %d operand width mismatch", ErrInvalidGraph, n.ID)
			}
		case OpTrunc:
			if err := ref(n.ID, n.X, "x"); err != nil {
				return err
			}
			if n.Width > byID[n.X].Width {
				return fmt.Errorf("%w: trunc node %d widens from %d to %d", ErrInvalidGraph, n.ID, byID[n.X].Width, n.Width)
			}
		case OpExtend:
			if err := ref(n.ID, n.X, "x"); err != nil {
				return err
			}
			if n.Width < byID[n.X].Width {
				return fmt.Errorf("%w: extend node %d narrows from %d to %d", ErrInvalidGraph, n.ID, byID[n.X].Width, n.Width)
			}
		case OpCmp:
			if n.Width != 1 {
				return fmt.Errorf("%w: cmp node %d has width %d, want 1", ErrInvalidGraph, n.ID, n.Width)
			}
			if n.Pred < CmpEq || n.Pred > CmpGe {
				return fmt.Errorf("%w: cmp node %d has predicate %d", ErrInvalidGraph, n.ID, n.Pred)
			}
			if err := ref(n.ID, n.X, "x"); err != nil {
				return err
			}
			if err := ref(n.ID, n.Y, "y"); err != nil {
				return err
			}
			if byID[n.X].Width != byID[n.Y].Width {
				return fmt.Errorf("%w: cmp node %d operand width mismatch", ErrInvalidGraph, n.ID)
			}
		case OpOverflow:
			if n.Width != 1 {
				return fmt.Errorf("%w: overflow node %d has width %d, want 1", ErrInvalidGraph, n.ID, n.Width)
			}
			if err := ref(n.ID, n.X, "x"); err != nil {
				return err
			}
			if !byID[n.X].Op.IsArithmetic() || byID[n.X].Check == nil {
				return fmt.Errorf("%w: overflow node %d must reference checked arithmetic", ErrInvalidGraph, n.ID)
			}
		case OpBranch:
			if err := ref(n.ID, n.X, "condition"); err != nil {
				return err
			}
			if err := ref(n.ID, n.Then, "then"); err != nil {
				return err
			}
			if err := ref(n.ID, n.Else, "else"); err != nil {
				return err
			}
			if !isControl(byID[n.Then].Op) || !isControl(byID[n.Else].Op) {
				return fmt.Errorf("%w: branch node %d targets a non-control node", ErrInvalidGraph, n.ID)
			}
		case OpReturn:
			if err := ref(n.ID, n.X, "return value"); err != nil {
				return err
			}
		case OpError:
			// Source is a provenance ID and need not name a node in this graph.
		default:
			return fmt.Errorf("%w: node %d has unknown op %d", ErrInvalidGraph, n.ID, n.Op)
		}
	}

	if _, ok := byID[g.Entry]; !ok {
		return fmt.Errorf("%w: entry node %d does not exist", ErrInvalidGraph, g.Entry)
	}
	if !isControl(byID[g.Entry].Op) {
		return fmt.Errorf("%w: entry node %d is not a control node", ErrInvalidGraph, g.Entry)
	}

	// Detect cycles over both data and control edges.
	const (
		unseen = iota
		active
		done
	)
	state := make(map[NodeID]uint8, len(g.Nodes))
	var visit func(NodeID) error
	visit = func(id NodeID) error {
		switch state[id] {
		case active:
			return fmt.Errorf("%w: cycle through node %d", ErrInvalidGraph, id)
		case done:
			return nil
		}
		state[id] = active
		n := byID[id]
		next := make([]NodeID, 0, 6)
		switch n.Op {
		case OpAdd, OpSub, OpMul, OpCmp:
			next = append(next, n.X, n.Y)
		case OpTrunc, OpExtend, OpOverflow, OpReturn:
			next = append(next, n.X)
		case OpBranch:
			next = append(next, n.X, n.Then, n.Else)
		}
		for _, child := range next {
			if err := visit(child); err != nil {
				return err
			}
		}
		state[id] = done
		return nil
	}
	if err := visit(g.Entry); err != nil {
		return err
	}
	return nil
}

func isControl(op Op) bool {
	return op == OpBranch || op == OpReturn || op == OpError
}
