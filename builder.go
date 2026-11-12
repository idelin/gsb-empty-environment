package netsec

// Builder constructs validated SSA graphs with fresh sequential IDs.
// It is intended for tests and for hosts lowering their IR into this one.
type Builder struct {
	g       *Graph
	current *Block
	nextVal int
	nextBlk int
}

// NewBuilder starts a graph consuming inputs bytes.
func NewBuilder(name string, inputs int) *Builder {
	b := &Builder{
		g: &Graph{
			Name:   name,
			Inputs: inputs,
			Values: map[int]*Value{},
		},
		nextVal: 1,
		nextBlk: 0,
	}
	b.current = b.NewBlock()
	b.g.Entry = b.current.ID
	return b
}

// NewBlock appends a block but does not switch to it.
func (b *Builder) NewBlock() *Block {
	blk := &Block{ID: b.nextBlk}
	b.nextBlk++
	b.g.Blocks = append(b.g.Blocks, blk)
	return blk
}

// UseBlock makes blk the block receiving subsequent definitions.
func (b *Builder) UseBlock(blk *Block) {
	b.current = blk
}

// Entry returns the entry block.
func (b *Builder) Entry() *Block {
	return b.g.Blocks[0]
}

func (b *Builder) add(v *Value) int {
	v.ID = b.nextVal
	b.nextVal++
	b.g.Values[v.ID] = v
	b.current.Insts = append(b.current.Insts, v.ID)
	return v.ID
}

// Input exposes input byte index as a width-8 value.
func (b *Builder) Input(index int) int {
	return b.add(&Value{Node: index, Op: OpInput, Width: 8, Input: index})
}

// Const adds a width-bit constant.
func (b *Builder) Const(node int, width int, imm uint64) int {
	return b.add(&Value{Node: node, Op: OpConst, Width: width, Imm: imm})
}

// Binary adds an Add/Sub/Mul value of the operand width.
func (b *Builder) Binary(node int, op Op, x, y int) int {
	width := b.g.Values[x].Width
	return b.add(&Value{Node: node, Op: op, Width: width, X: x, Y: y})
}

// Overflow records the signed overflow flag of an arithmetic value.
func (b *Builder) Overflow(node, arithID int) int {
	return b.add(&Value{Node: node, Op: OpOverflow, Width: 1, X: arithID})
}

// Trunc narrows x to width.
func (b *Builder) Trunc(node, x, width int) int {
	return b.add(&Value{Node: node, Op: OpTrunc, Width: width, X: x})
}

// SExt sign-extends x to width.
func (b *Builder) SExt(node, x, width int) int {
	return b.add(&Value{Node: node, Op: OpSExt, Width: width, X: x})
}

// ZExt zero-extends x to width.
func (b *Builder) ZExt(node, x, width int) int {
	return b.add(&Value{Node: node, Op: OpZExt, Width: width, X: x})
}

// Compare adds a width-1 comparison.
func (b *Builder) Compare(node int, op Op, x, y int) int {
	return b.add(&Value{Node: node, Op: op, Width: 1, X: x, Y: y})
}

// Not boolean-negates x.
func (b *Builder) Not(node, x int) int {
	return b.add(&Value{Node: node, Op: OpNot, Width: 1, X: x})
}

// And boolean-ands x and y.
func (b *Builder) And(node, x, y int) int {
	return b.add(&Value{Node: node, Op: OpAnd, Width: 1, X: x, Y: y})
}

// Or boolean-ors x and y.
func (b *Builder) Or(node, x, y int) int {
	return b.add(&Value{Node: node, Op: OpOr, Width: 1, X: x, Y: y})
}

// Return terminates the current block returning v.
func (b *Builder) Return(v int) *Graph {
	b.current.Term = TermReturn
	b.current.Ret = v
	return b.finish()
}

// Error terminates the current block with a source-node error.
func (b *Builder) Error(node int) {
	b.current.Term = TermError
	b.current.ErrNode = node
}

// Branch terminates the current block with a conditional jump.
func (b *Builder) Branch(cond int, thenBlk, elseBlk *Block) {
	b.current.Term = TermBranch
	b.current.Cond = cond
	b.current.Then = thenBlk.ID
	b.current.Else = elseBlk.ID
}

func (b *Builder) finish() *Graph {
	if err := b.g.Validate(); err != nil {
		panic(err)
	}
	return b.g
}
