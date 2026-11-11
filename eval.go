package netsec

import "errors"

// EventKind distinguishes the marker events produced by Execute.
type EventKind int

const (
	EventAlloc EventKind = iota
	EventWrite
)

// Event is an Alloc or Write marker hit during concrete execution.
type Event struct {
	Kind  EventKind
	Value uint32 // allocated size or written byte count
	Node  NodeID // marker node that produced the event
}

// Execute runs the graph concretely on the given 8 input bytes and returns
// the Alloc/Write events in execution order.
func Execute(g *Graph, in [MaxInputs]byte) ([]Event, error) {
	vals := make([]uint32, len(g.nodes))
	def := make([]bool, len(g.nodes))
	var events []Event
	blk := 0
	for blk >= 0 {
		b := &g.blocks[blk]
		for _, id := range b.nodes {
			v, err := g.evalNode(vals, def, id, &in)
			if err != nil {
				return nil, err
			}
			vals[id] = v
			def[id] = true
			switch g.nodes[id].op {
			case opAlloc:
				events = append(events, Event{Kind: EventAlloc, Value: v, Node: id})
			case opWrite:
				events = append(events, Event{Kind: EventWrite, Value: v, Node: id})
			}
		}
		if b.cond >= 0 {
			if !def[b.cond] {
				return nil, ErrDominance
			}
			if vals[b.cond] != 0 {
				blk = b.next[1]
			} else {
				blk = b.next[0]
			}
		} else {
			blk = b.next[0]
		}
	}
	return events, nil
}

// Violates reports whether concrete execution on in performs a write that
// exceeds the most recent allocation on the same path. A write with no
// preceding allocation counts as a violation of a zero-byte buffer.
func Violates(g *Graph, in [MaxInputs]byte) (bool, error) {
	events, err := Execute(g, in)
	if err != nil {
		return false, err
	}
	var alloc uint64
	for _, e := range events {
		switch e.Kind {
		case EventAlloc:
			alloc = uint64(e.Value)
		case EventWrite:
			if uint64(e.Value) > alloc {
				return true, nil
			}
		}
	}
	return false, nil
}

var errBadOp = errors.New("netsec: unknown node op")

func (g *Graph) evalNode(vals []uint32, def []bool, id NodeID, in *[MaxInputs]byte) (uint32, error) {
	n := g.nodes[id]
	operand := func(x NodeID) (uint32, error) {
		if x < 0 || !def[x] {
			return 0, ErrDominance
		}
		return vals[x], nil
	}
	switch n.op {
	case opInput:
		var v uint32
		for i := 0; i < n.w.bytes(); i++ {
			v |= uint32(in[int(n.imm)+i]) << (8 * i)
		}
		return v, nil
	case opConst:
		return n.imm, nil
	case opAdd, opMul:
		a, err := operand(n.a)
		if err != nil {
			return 0, err
		}
		b, err := operand(n.b)
		if err != nil {
			return 0, err
		}
		if n.op == opAdd {
			return uint32((uint64(a) + uint64(b)) & n.w.max()), nil
		}
		return uint32((uint64(a) * uint64(b)) & n.w.max()), nil
	case opTrunc:
		a, err := operand(n.a)
		if err != nil {
			return 0, err
		}
		return uint32(uint64(a) & Width(n.imm).max()), nil
	case opExt:
		return operand(n.a)
	case opCmp:
		a, err := operand(n.a)
		if err != nil {
			return 0, err
		}
		b, err := operand(n.b)
		if err != nil {
			return 0, err
		}
		var r bool
		switch Pred(n.imm) {
		case LT:
			r = a < b
		case LE:
			r = a <= b
		case GT:
			r = a > b
		case GE:
			r = a >= b
		case EQ:
			r = a == b
		case NE:
			r = a != b
		}
		if r {
			return 1, nil
		}
		return 0, nil
	case opAlloc, opWrite:
		return operand(n.a)
	}
	return 0, errBadOp
}
