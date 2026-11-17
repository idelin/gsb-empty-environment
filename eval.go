package netsec

import (
	"fmt"
	"math/big"
)

type concreteValue struct {
	bits  uint64
	width uint8
}

// Interpret executes a graph independently of the symbolic verifier. Input
// bytes are unsigned; all integer results are two's-complement bit vectors.
// The returned Outcome is the graph's only observable result.
func Interpret(g *Graph, input []byte) (Outcome, error) {
	if err := g.Validate(); err != nil {
		return Outcome{}, err
	}
	return interpretValidated(g, input)
}

func interpretValidated(g *Graph, input []byte) (Outcome, error) {
	if len(input) != g.InputCount {
		return Outcome{}, fmt.Errorf("netsec: got %d input bytes, want %d", len(input), g.InputCount)
	}
	byID := make(map[NodeID]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	ev := &evaluator{
		graph:  g,
		byID:   byID,
		input:  input,
		values: make(map[NodeID]concreteValue, len(g.Nodes)),
	}
	return ev.control(g.Entry)
}

type evaluator struct {
	graph  *Graph
	byID   map[NodeID]Node
	input  []byte
	values map[NodeID]concreteValue
}

func (e *evaluator) value(id NodeID) (concreteValue, error) {
	if v, ok := e.values[id]; ok {
		return v, nil
	}
	n, ok := e.byID[id]
	if !ok {
		return concreteValue{}, fmt.Errorf("netsec: missing node %d", id)
	}

	var v concreteValue
	switch n.Op {
	case OpInput:
		if n.InputIndex < 0 || n.InputIndex >= len(e.input) {
			return concreteValue{}, fmt.Errorf("netsec: input index %d out of range", n.InputIndex)
		}
		v = concreteValue{bits: uint64(e.input[n.InputIndex]), width: 8}
	case OpConst:
		v = concreteValue{bits: n.Value & maskBits(n.Width), width: n.Width}
	case OpAdd, OpSub, OpMul:
		x, err := e.value(n.X)
		if err != nil {
			return concreteValue{}, err
		}
		y, err := e.value(n.Y)
		if err != nil {
			return concreteValue{}, err
		}
		switch n.Op {
		case OpAdd:
			v = concreteValue{bits: (x.bits + y.bits) & maskBits(n.Width), width: n.Width}
		case OpSub:
			v = concreteValue{bits: (x.bits - y.bits) & maskBits(n.Width), width: n.Width}
		case OpMul:
			v = concreteValue{bits: (x.bits * y.bits) & maskBits(n.Width), width: n.Width}
		}
	case OpTrunc:
		x, err := e.value(n.X)
		if err != nil {
			return concreteValue{}, err
		}
		v = concreteValue{bits: x.bits & maskBits(n.Width), width: n.Width}
	case OpExtend:
		x, err := e.value(n.X)
		if err != nil {
			return concreteValue{}, err
		}
		bits := x.bits & maskBits(x.width)
		if n.Signed && x.width > 0 && x.width < 64 && bits&(uint64(1)<<(x.width-1)) != 0 {
			bits |= ^maskBits(x.width)
		}
		v = concreteValue{bits: bits & maskBits(n.Width), width: n.Width}
	case OpCmp:
		x, err := e.value(n.X)
		if err != nil {
			return concreteValue{}, err
		}
		y, err := e.value(n.Y)
		if err != nil {
			return concreteValue{}, err
		}
		if compareValues(x, y, n.Pred, n.Signed) {
			v = concreteValue{bits: 1, width: 1}
		} else {
			v = concreteValue{bits: 0, width: 1}
		}
	case OpOverflow:
		overflow, err := e.overflow(n.X)
		if err != nil {
			return concreteValue{}, err
		}
		v = concreteValue{width: 1}
		if overflow {
			v.bits = 1
		}
	default:
		return concreteValue{}, fmt.Errorf("netsec: node %d is control flow (%s), not a value", id, n.Op)
	}
	e.values[id] = v
	return v, nil
}

func (e *evaluator) overflow(id NodeID) (bool, error) {
	n, ok := e.byID[id]
	if !ok {
		return false, fmt.Errorf("netsec: missing overflow operand %d", id)
	}
	if !n.Op.IsArithmetic() || n.Check == nil {
		return false, fmt.Errorf("netsec: node %d does not carry an overflow check", id)
	}
	x, err := e.value(n.X)
	if err != nil {
		return false, err
	}
	y, err := e.value(n.Y)
	if err != nil {
		return false, err
	}
	return checkOverflow(n.Op, n.Width, x.bits, y.bits, *n.Check), nil
}

func compareValues(x, y concreteValue, pred CmpPred, signed bool) bool {
	var a, b *big.Int
	if signed {
		a = signedBig(x.bits, x.width)
		b = signedBig(y.bits, y.width)
	} else {
		a = unsignedBig(x.bits, x.width)
		b = unsignedBig(y.bits, y.width)
	}
	cmp := a.Cmp(b)
	switch pred {
	case CmpEq:
		return cmp == 0
	case CmpNe:
		return cmp != 0
	case CmpLt:
		return cmp < 0
	case CmpLe:
		return cmp <= 0
	case CmpGt:
		return cmp > 0
	case CmpGe:
		return cmp >= 0
	default:
		return false
	}
}

func (e *evaluator) control(id NodeID) (Outcome, error) {
	n, ok := e.byID[id]
	if !ok {
		return Outcome{}, fmt.Errorf("netsec: missing control node %d", id)
	}
	switch n.Op {
	case OpReturn:
		v, err := e.value(n.X)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{
			Kind:  OutcomeReturn,
			Value: signedInt64(v.bits, v.width),
			Width: v.width,
		}, nil
	case OpError:
		return Outcome{Kind: OutcomeError, Source: n.Source}, nil
	case OpBranch:
		cond, err := e.value(n.X)
		if err != nil {
			return Outcome{}, err
		}
		if cond.bits&maskBits(cond.width) != 0 {
			return e.control(n.Then)
		}
		return e.control(n.Else)
	default:
		return Outcome{}, fmt.Errorf("netsec: node %d is not control flow", id)
	}
}
