package midend

// Builder is a small fluent helper for constructing functions. It keeps
// acceptance tests readable while every emitted node is a plain IR value.
type Builder struct {
	prog *Program
	fn   *Func
	cur  *Block
}

// NewBuilder attaches a builder to an existing program.
func NewBuilder(prog *Program) *Builder { return &Builder{prog: prog} }

// Func starts a new function and selects its entry block.
func (b *Builder) Func(name string, ret Type, params ...*Param) *Builder {
	f := &Func{Name: name, Ret: ret, Params: params}
	b.prog.AddFunc(f)
	b.fn = f
	return b.Block("entry")
}

// Block creates and selects a basic block.
func (b *Builder) Block(name string) *Builder {
	blk := &Block{Name: name}
	b.fn.Blocks = append(b.fn.Blocks, blk)
	b.cur = blk
	return b
}

// BlockRef returns a block previously created in the current function.
func (b *Builder) BlockRef(name string) *Block {
	return b.prog.Block(b.fn, name)
}

func (b *Builder) emit(i Inst) Operand {
	b.cur.Insts = append(b.cur.Insts, i)
	if i.GetName() != "" {
		return R(i.GetName())
	}
	return Operand{}
}

// Alloc emits a heap or stack aggregate allocation.
func (b *Builder) Alloc(name string, heap bool, ty *StructType) Operand {
	return b.emit(&Alloc{Name: name, Heap: heap, Ty: ty})
}

// GlobalAddr takes the address of a global.
func (b *Builder) GlobalAddr(name string, g *Global) Operand {
	return b.emit(&GlobalAddr{Name: name, G: g})
}

// Field takes the address of field index fi.
func (b *Builder) Field(name string, ptr Operand, fi int, ty Type) Operand {
	return b.emit(&FieldAddr{Name: name, Ty: ty, Ptr: ptr, Field: fi})
}

// AddrAdd computes a pointer with a dynamic offset.
func (b *Builder) AddrAdd(name string, ptr, off Operand, ty Type) Operand {
	return b.emit(&AddrAdd{Name: name, Ptr: ptr, Off: off, Ty: ty})
}

// Load reads through ptr.
func (b *Builder) Load(name string, ptr Operand, ty Type) Operand {
	return b.emit(&Load{Name: name, Ty: ty, Ptr: ptr})
}

// Store writes val through ptr.
func (b *Builder) Store(ptr, val Operand) *Builder {
	b.cur.Insts = append(b.cur.Insts, &Store{Ptr: ptr, Val: val})
	return b
}

// Phi emits a phi node.
func (b *Builder) Phi(name string, ty Type, edges ...PhiEdge) Operand {
	return b.emit(&Phi{Name: name, Ty: ty, Incoming: edges})
}

// Call invokes an internal function.
func (b *Builder) Call(name, callee string, ty Type, args ...Operand) Operand {
	return b.emit(&Call{Name: name, Ty: ty, Callee: callee, Args: args})
}

// ExtCall invokes an opaque external function.
func (b *Builder) ExtCall(name, callee string, ty Type, args ...Operand) Operand {
	return b.emit(&ExtCall{Name: name, Ty: ty, Callee: callee, Args: args})
}

// Bin emits an integer binary operation.
func (b *Builder) Bin(name, op string, ty Type, x, y Operand) Operand {
	return b.emit(&BinOp{Name: name, Ty: ty, Op: op, X: x, Y: y})
}

// Print emits an observable integer value.
func (b *Builder) Print(v Operand) *Builder {
	b.cur.Insts = append(b.cur.Insts, &PrintInst{Val: v})
	return b
}

// Jump terminates the block with an unconditional jump.
func (b *Builder) Jump(to string) *Builder {
	b.cur.Term = &Jump{To: to}
	return b
}

// Branch terminates the block with a conditional branch.
func (b *Builder) Branch(cond Operand, t, f string) *Builder {
	b.cur.Term = &Branch{Cond: cond, T: t, F: f}
	return b
}

// Ret terminates the block with a return.
func (b *Builder) Ret(v Operand) *Builder {
	b.cur.Term = &Return{Val: v}
	return b
}

// Raw appends a fully constructed instruction (used by optimizations).
func (b *Builder) Raw(i Inst) { b.cur.Insts = append(b.cur.Insts, i) }
