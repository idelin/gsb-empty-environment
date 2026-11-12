package netsec

import "strconv"

// BOp is the node kind of a hash-consed boolean expression DAG.
type BOp uint8

const (
	BConst BOp = iota // constant 0/1
	BVar              // primary input variable
	BNot
	BAnd
	BOr
	BXor
)

// BNode is one hash-consed boolean expression. Factory interns nodes so that
// structural identity implies pointer identity, which the SAT layer exploits.
type BNode struct {
	id   int
	op   BOp
	imm  bool // BConst value
	x, y *BNode
	name string // variable namespace for BVar; debug label otherwise
}

func (n *BNode) ID() int      { return n.id }
func (n *BNode) Op() BOp      { return n.op }
func (n *BNode) Const() bool  { return n.imm }
func (n *BNode) X() *BNode    { return n.x }
func (n *BNode) Y() *BNode    { return n.y }
func (n *BNode) Name() string { return n.name }

// BFactory builds canonical boolean expression DAGs and keeps the variable
// namespace shared across all graphs of one verification call.
type BFactory struct {
	nodes  []*BNode
	intern map[uint64]*BNode
	vars   map[string]*BNode
}

// NewBFactory creates an empty factory.
func NewBFactory() *BFactory {
	f := &BFactory{
		intern: map[uint64]*BNode{},
		vars:   map[string]*BNode{},
	}
	// Reserve ids 0/1 for the two constants.
	f.Const(false)
	f.Const(true)
	return f
}

// Nodes returns every interned node in id order.
func (f *BFactory) Nodes() []*BNode { return f.nodes }

// Const returns the canonical constant node.
func (f *BFactory) Const(v bool) *BNode {
	key := nodeKey(BConst, 0, 0, v)
	if n, ok := f.intern[key]; ok {
		return n
	}
	n := &BNode{id: len(f.nodes), op: BConst, imm: v, name: strconv.FormatBool(v)}
	f.nodes = append(f.nodes, n)
	f.intern[key] = n
	return n
}

// Var returns the canonical variable for a namespaced key.
func (f *BFactory) Var(key string) *BNode {
	if n, ok := f.vars[key]; ok {
		return n
	}
	n := &BNode{id: len(f.nodes), op: BVar, name: key}
	f.nodes = append(f.nodes, n)
	f.vars[key] = n
	return n
}

// Not canonicalizes double negations and constants.
func (f *BFactory) Not(a *BNode) *BNode {
	switch a.op {
	case BConst:
		return f.Const(!a.imm)
	case BNot:
		return a.x
	}
	key := nodeKey(BNot, a.id, 0, false)
	if n, ok := f.intern[key]; ok {
		return n
	}
	n := &BNode{id: len(f.nodes), op: BNot, x: a, name: "not" + strconv.Itoa(a.id)}
	f.nodes = append(f.nodes, n)
	f.intern[key] = n
	return n
}

// And canonicalizes constants, repeated operands and negation pairs.
func (f *BFactory) And(a, b *BNode) *BNode {
	if a.op == BConst {
		if !a.imm {
			return f.Const(false)
		}
		return b
	}
	if b.op == BConst {
		return f.And(b, a)
	}
	if a == b {
		return a
	}
	if isNegPair(a, b) {
		return f.Const(false)
	}
	lo, hi := orderNodes(a, b)
	key := nodeKey(BAnd, lo.id, hi.id, false)
	if n, ok := f.intern[key]; ok {
		return n
	}
	n := &BNode{id: len(f.nodes), op: BAnd, x: lo, y: hi,
		name: "and" + strconv.Itoa(lo.id) + "_" + strconv.Itoa(hi.id)}
	f.nodes = append(f.nodes, n)
	f.intern[key] = n
	return n
}

// Or canonicalizes constants, repeated operands and negation pairs.
func (f *BFactory) Or(a, b *BNode) *BNode {
	if a.op == BConst {
		if a.imm {
			return f.Const(true)
		}
		return b
	}
	if b.op == BConst {
		return f.Or(b, a)
	}
	if a == b {
		return a
	}
	if isNegPair(a, b) {
		return f.Const(true)
	}
	lo, hi := orderNodes(a, b)
	key := nodeKey(BOr, lo.id, hi.id, false)
	if n, ok := f.intern[key]; ok {
		return n
	}
	n := &BNode{id: len(f.nodes), op: BOr, x: lo, y: hi,
		name: "or" + strconv.Itoa(lo.id) + "_" + strconv.Itoa(hi.id)}
	f.nodes = append(f.nodes, n)
	f.intern[key] = n
	return n
}

// Xor builds an exclusive-or without a dedicated node kind, keeping the SAT
// Tseitin rules small.
func (f *BFactory) Xor(a, b *BNode) *BNode {
	if a.op == BConst {
		if a.imm {
			return f.Not(b)
		}
		return b
	}
	if b.op == BConst {
		return f.Xor(b, a)
	}
	if a == b {
		return f.Const(false)
	}
	return f.Or(f.And(a, f.Not(b)), f.And(f.Not(a), b))
}

// Ite builds if-then-else: c ? t : e.
func (f *BFactory) Ite(c, t, e *BNode) *BNode {
	if c.op == BConst {
		if c.imm {
			return t
		}
		return e
	}
	if t == e {
		return t
	}
	return f.Or(f.And(c, t), f.And(f.Not(c), e))
}

func isNegPair(a, b *BNode) bool {
	return (a.op == BNot && a.x == b) || (b.op == BNot && b.x == a)
}

func orderNodes(a, b *BNode) (*BNode, *BNode) {
	if a.id <= b.id {
		return a, b
	}
	return b, a
}

// nodeKey packs an op and up to two child ids into one hash-cons key.
// Child ids fit in 30 bits for the graph sizes this package targets.
func nodeKey(op BOp, x, y int, imm bool) uint64 {
	k := uint64(op&15) << 60
	k |= uint64(uint32(x)) << 30
	k |= uint64(uint32(y))
	if imm {
		k |= 1 << 63
	}
	return k
}
