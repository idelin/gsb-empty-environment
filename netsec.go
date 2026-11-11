package netsec

import "errors"

// Width of a bit-vector value, in bits.
type Width uint8

const (
	W1  Width = 1 // boolean, produced by Cmp, consumed by Branch
	W8  Width = 8
	W16 Width = 16
	W32 Width = 32
)

// max is the largest representable value of the width.
func (w Width) max() uint64 {
	if w >= 32 {
		return 1<<32 - 1
	}
	return 1<<uint(w) - 1
}

// bytes is the number of input bytes a value of this width occupies.
func (w Width) bytes() int { return (int(w) + 7) / 8 }

// arith reports whether the width may carry arithmetic values.
func (w Width) arith() bool { return w == W8 || w == W16 || w == W32 }

// Pred is an unsigned comparison predicate.
type Pred uint8

const (
	LT Pred = iota
	LE
	GT
	GE
	EQ
	NE
)

// MaxInputs is the maximum number of input bytes a program may read.
const MaxInputs = 8

var (
	ErrWidth     = errors.New("netsec: incompatible or unsupported width")
	ErrInput     = errors.New("netsec: input byte range outside the 8 input bytes")
	ErrBlock     = errors.New("netsec: invalid control-flow block")
	ErrCycle     = errors.New("netsec: control-flow edge would create a cycle")
	ErrDominance = errors.New("netsec: operand does not dominate its use")
)

// NodeID identifies an SSA value within a Graph.
type NodeID int

type op int

const (
	opInput op = iota // imm: byte offset into the 8 input bytes
	opConst           // imm: constant value (masked to width)
	opAdd
	opMul
	opTrunc // imm: target width
	opExt   // imm: target width (zero extension)
	opCmp   // imm: Pred
	opAlloc // a: allocation size in bytes (marker)
	opWrite // a: number of bytes written (marker)
)

type node struct {
	op   op
	w    Width
	a, b NodeID
	imm  uint32
	blk  int
}

type block struct {
	nodes []NodeID
	cond  NodeID // -1: no conditional branch
	next  [2]int // [0]: else/fallthrough, [1]: then; -1: none
	term  bool   // control flow already sealed
}

// Graph is a loop-free SSA program with allocation and write markers.
// Construct it with Builder; it is immutable once Build returns.
type Graph struct {
	nodes  []node
	blocks []block
	nIn    int // number of input bytes referenced
}

// NumInputBytes reports how many of the 8 input bytes the graph reads.
func (g *Graph) NumInputBytes() int { return g.nIn }

// Builder constructs a Graph block by block. The entry block is block 0
// and is current when the Builder is created. The first error encountered
// is sticky and reported by Build.
type Builder struct {
	g   *Graph
	cur int
	err error
}

// NewBuilder returns a Builder with an empty entry block selected.
func NewBuilder() *Builder {
	b := &Builder{g: &Graph{}}
	b.g.blocks = append(b.g.blocks, block{cond: -1, next: [2]int{-1, -1}})
	return b
}

func (b *Builder) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *Builder) add(n node) NodeID {
	n.blk = b.cur
	n.a, n.b = -1, -1
	id := NodeID(len(b.g.nodes))
	b.g.nodes = append(b.g.nodes, n)
	blk := &b.g.blocks[b.cur]
	blk.nodes = append(blk.nodes, id)
	return id
}

func (b *Builder) operand(id NodeID) (node, bool) {
	if id < 0 || int(id) >= len(b.g.nodes) {
		return node{}, false
	}
	return b.g.nodes[id], true
}

// Input reads w.bytes() little-endian bytes starting at offset off of the
// 8 input bytes.
func (b *Builder) Input(off int, w Width) NodeID {
	if b.err != nil {
		return -1
	}
	if !w.arith() {
		b.fail(ErrWidth)
		return -1
	}
	if off < 0 || off+w.bytes() > MaxInputs {
		b.fail(ErrInput)
		return -1
	}
	if end := off + w.bytes(); end > b.g.nIn {
		b.g.nIn = end
	}
	return b.add(node{op: opInput, w: w, imm: uint32(off)})
}

// Const creates a constant of the given width; the value is masked to it.
func (b *Builder) Const(w Width, v uint32) NodeID {
	if b.err != nil {
		return -1
	}
	if !w.arith() {
		b.fail(ErrWidth)
		return -1
	}
	return b.add(node{op: opConst, w: w, imm: uint32(uint64(v) & w.max())})
}

// arith2 validates a binary arithmetic operation.
func (b *Builder) arith2(o op, x, y NodeID) NodeID {
	if b.err != nil {
		return -1
	}
	nx, okx := b.operand(x)
	ny, oky := b.operand(y)
	if !okx || !oky || nx.w != ny.w || !nx.w.arith() {
		b.fail(ErrWidth)
		return -1
	}
	n := b.add(node{op: o, w: nx.w})
	b.g.nodes[n].a = x
	b.g.nodes[n].b = y
	return n
}

// Add returns x+y modulo 2^w.
func (b *Builder) Add(x, y NodeID) NodeID { return b.arith2(opAdd, x, y) }

// Mul returns x*y modulo 2^w.
func (b *Builder) Mul(x, y NodeID) NodeID { return b.arith2(opMul, x, y) }

// Trunc keeps the low w bits of x; w must be narrower than x.
func (b *Builder) Trunc(x NodeID, w Width) NodeID {
	if b.err != nil {
		return -1
	}
	nx, ok := b.operand(x)
	if !ok || !nx.w.arith() || !w.arith() || w >= nx.w {
		b.fail(ErrWidth)
		return -1
	}
	n := b.add(node{op: opTrunc, w: w, imm: uint32(w)})
	b.g.nodes[n].a = x
	return n
}

// Ext zero-extends x to w; w must be wider than x.
func (b *Builder) Ext(x NodeID, w Width) NodeID {
	if b.err != nil {
		return -1
	}
	nx, ok := b.operand(x)
	if !ok || !nx.w.arith() || !w.arith() || w <= nx.w {
		b.fail(ErrWidth)
		return -1
	}
	n := b.add(node{op: opExt, w: w, imm: uint32(w)})
	b.g.nodes[n].a = x
	return n
}

// Cmp compares x and y as unsigned values and returns a W1 boolean.
func (b *Builder) Cmp(p Pred, x, y NodeID) NodeID {
	if b.err != nil {
		return -1
	}
	nx, okx := b.operand(x)
	ny, oky := b.operand(y)
	if !okx || !oky || nx.w != ny.w || p > NE {
		b.fail(ErrWidth)
		return -1
	}
	n := b.add(node{op: opCmp, w: W1, imm: uint32(p)})
	b.g.nodes[n].a = x
	b.g.nodes[n].b = y
	return n
}

// Alloc marks the allocation of size bytes in the current block.
func (b *Builder) Alloc(size NodeID) NodeID { return b.marker(opAlloc, size) }

// Write marks a write of count bytes in the current block. A write is out
// of bounds when its count exceeds the size of the most recent Alloc on
// the same path.
func (b *Builder) Write(count NodeID) NodeID { return b.marker(opWrite, count) }

func (b *Builder) marker(o op, arg NodeID) NodeID {
	if b.err != nil {
		return -1
	}
	na, ok := b.operand(arg)
	if !ok || !na.w.arith() {
		b.fail(ErrWidth)
		return -1
	}
	n := b.add(node{op: o})
	b.g.nodes[n].a = arg
	return n
}

// NewBlock appends a fresh block and returns its index.
func (b *Builder) NewBlock() int {
	b.g.blocks = append(b.g.blocks, block{cond: -1, next: [2]int{-1, -1}})
	return len(b.g.blocks) - 1
}

// Switch selects block blk as the current block for subsequent nodes.
func (b *Builder) Switch(blk int) {
	if b.err != nil {
		return
	}
	if blk < 0 || blk >= len(b.g.blocks) || b.g.blocks[blk].term {
		b.fail(ErrBlock)
		return
	}
	b.cur = blk
}

// Branch terminates the current block with a conditional branch: cond
// selects thenBlk when true and elseBlk otherwise. Both targets must be
// later blocks, which keeps the graph acyclic.
func (b *Builder) Branch(cond NodeID, thenBlk, elseBlk int) {
	if b.err != nil {
		return
	}
	if b.g.blocks[b.cur].term {
		b.fail(ErrBlock)
		return
	}
	if cond < 0 || int(cond) >= len(b.g.nodes) || b.g.nodes[cond].w != W1 {
		b.fail(ErrWidth)
		return
	}
	if thenBlk <= b.cur || elseBlk <= b.cur ||
		thenBlk >= len(b.g.blocks) || elseBlk >= len(b.g.blocks) {
		b.fail(ErrCycle)
		return
	}
	blk := &b.g.blocks[b.cur]
	blk.cond = cond
	blk.next = [2]int{elseBlk, thenBlk}
	blk.term = true
}

// Fallthrough terminates the current block with an unconditional edge to
// next, which must be a later block.
func (b *Builder) Fallthrough(next int) {
	if b.err != nil {
		return
	}
	if b.g.blocks[b.cur].term {
		b.fail(ErrBlock)
		return
	}
	if next <= b.cur || next >= len(b.g.blocks) {
		b.fail(ErrCycle)
		return
	}
	blk := &b.g.blocks[b.cur]
	blk.next = [2]int{next, -1}
	blk.term = true
}

// Build validates the graph and returns it.
func (b *Builder) Build() (*Graph, error) {
	if b.err != nil {
		return nil, b.err
	}
	if err := b.g.validate(); err != nil {
		return nil, err
	}
	return b.g, nil
}

// validate checks structural properties: well-formed blocks, forward-only
// edges (hence acyclic), reachability, and operand dominance.
func (g *Graph) validate() error {
	n := len(g.blocks)
	if n == 0 {
		return ErrBlock
	}
	preds := make([][]int, n)
	for i := 0; i < n; i++ {
		blk := &g.blocks[i]
		if blk.cond >= 0 {
			if g.nodes[blk.cond].w != W1 || blk.next[0] < 0 || blk.next[1] < 0 {
				return ErrBlock
			}
		}
		for _, s := range blk.next {
			if s < 0 {
				continue
			}
			if s <= i || s >= n {
				return ErrCycle
			}
			preds[s] = append(preds[s], i)
		}
	}
	// Edges point forward, so dominators are computed in one pass.
	dom := make([]map[int]bool, n)
	dom[0] = map[int]bool{0: true}
	for i := 1; i < n; i++ {
		if len(preds[i]) == 0 {
			return ErrBlock // unreachable block
		}
		inter := map[int]bool{}
		for j, p := range preds[i] {
			if j == 0 {
				for k := range dom[p] {
					inter[k] = true
				}
			} else {
				for k := range inter {
					if !dom[p][k] {
						delete(inter, k)
					}
				}
			}
		}
		inter[i] = true
		dom[i] = inter
	}
	check := func(use int, id NodeID) error {
		if id < 0 {
			return nil // no operand
		}
		if int(id) >= len(g.nodes) {
			return ErrDominance
		}
		d := g.nodes[id].blk
		if d != use && !dom[use][d] {
			return ErrDominance
		}
		return nil
	}
	for i := 0; i < n; i++ {
		blk := &g.blocks[i]
		for _, id := range blk.nodes {
			nd := g.nodes[id]
			if err := check(i, nd.a); err != nil {
				return err
			}
			if err := check(i, nd.b); err != nil {
				return err
			}
		}
		if blk.cond >= 0 {
			if err := check(i, blk.cond); err != nil {
				return err
			}
		}
	}
	return nil
}
