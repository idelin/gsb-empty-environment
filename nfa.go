package netsec

type nfaOp uint8

const (
	nfaChar nfaOp = iota
	nfaEpsilon
	nfaSplit
	nfaAssertStart
	nfaAssertEnd
	nfaMatch
)

type nfaState struct {
	op    nfaOp
	out1  int
	out2  int
	class *charClass
}

type program struct {
	states []nfaState
	start  int
	accept int
}

type fragment struct {
	start int
	outs  []int
}

type nfaBuilder struct {
	states []nfaState
}

func buildProgram(root *regexNode) *program {
	b := &nfaBuilder{}
	frag := b.build(root)
	accept := b.addState(nfaState{op: nfaMatch, out1: -1, out2: -1})
	b.patch(frag.outs, accept)
	return &program{states: b.states, start: frag.start, accept: accept}
}

func (b *nfaBuilder) addState(state nfaState) int {
	idx := len(b.states)
	b.states = append(b.states, state)
	return idx
}

func (b *nfaBuilder) patch(outs []int, target int) {
	for _, idx := range outs {
		state := &b.states[idx]
		if state.op != nfaSplit {
			state.out1 = target
			continue
		}
		if state.out1 == -1 {
			state.out1 = target
		} else {
			state.out2 = target
		}
	}
}

func (b *nfaBuilder) build(node *regexNode) fragment {
	switch node.kind {
	case nodeEmpty:
		idx := b.addState(nfaState{op: nfaEpsilon, out1: -1, out2: -1})
		return fragment{start: idx, outs: []int{idx}}
	case nodeLiteral, nodeClass:
		idx := b.addState(nfaState{op: nfaChar, out1: -1, out2: -1, class: node.class})
		return fragment{start: idx, outs: []int{idx}}
	case nodeAnchorStart:
		idx := b.addState(nfaState{op: nfaAssertStart, out1: -1, out2: -1})
		return fragment{start: idx, outs: []int{idx}}
	case nodeAnchorEnd:
		idx := b.addState(nfaState{op: nfaAssertEnd, out1: -1, out2: -1})
		return fragment{start: idx, outs: []int{idx}}
	case nodeConcat:
		left := b.build(node.left)
		right := b.build(node.right)
		b.patch(left.outs, right.start)
		return fragment{start: left.start, outs: right.outs}
	case nodeUnion:
		left := b.build(node.left)
		right := b.build(node.right)
		idx := b.addState(nfaState{
			op:   nfaSplit,
			out1: left.start,
			out2: right.start,
		})
		return fragment{start: idx, outs: append(left.outs, right.outs...)}
	case nodeStar:
		child := b.build(node.child)
		idx := b.addState(nfaState{op: nfaSplit, out1: child.start, out2: -1})
		b.patch(child.outs, idx)
		return fragment{start: idx, outs: []int{idx}}
	case nodePlus:
		child := b.build(node.child)
		idx := b.addState(nfaState{op: nfaSplit, out1: child.start, out2: -1})
		b.patch(child.outs, idx)
		return fragment{start: child.start, outs: []int{idx}}
	case nodeOptional:
		child := b.build(node.child)
		idx := b.addState(nfaState{op: nfaSplit, out1: child.start, out2: -1})
		return fragment{start: idx, outs: append(child.outs, idx)}
	default:
		panic("netsec: unknown regex node")
	}
}
