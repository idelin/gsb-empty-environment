package loopkit

// EmitConst emits an integer constant.
func (b *Block) EmitConst(name string, typ Type, val int64) *Instr {
	i := b.Func.newInstr(OpConst, typ)
	i.Name, i.Int, i.Block = name, val, b
	b.Instrs = append(b.Instrs, i)
	return i
}

func (b *Block) emitBin(op Op, typ Type, x, y *Instr) *Instr {
	i := b.Func.newInstr(op, typ)
	i.Operands, i.Block = []*Instr{x, y}, b
	b.Instrs = append(b.Instrs, i)
	return i
}

func (b *Block) EmitAdd(typ Type, x, y *Instr) *Instr  { return b.emitBin(OpAdd, typ, x, y) }
func (b *Block) EmitSub(typ Type, x, y *Instr) *Instr  { return b.emitBin(OpSub, typ, x, y) }
func (b *Block) EmitMul(typ Type, x, y *Instr) *Instr  { return b.emitBin(OpMul, typ, x, y) }
func (b *Block) EmitUDiv(typ Type, x, y *Instr) *Instr { return b.emitBin(OpUDiv, typ, x, y) }
func (b *Block) EmitSDiv(typ Type, x, y *Instr) *Instr { return b.emitBin(OpSDiv, typ, x, y) }
func (b *Block) EmitURem(typ Type, x, y *Instr) *Instr { return b.emitBin(OpURem, typ, x, y) }
func (b *Block) EmitSRem(typ Type, x, y *Instr) *Instr { return b.emitBin(OpSRem, typ, x, y) }
func (b *Block) EmitAnd(typ Type, x, y *Instr) *Instr  { return b.emitBin(OpAnd, typ, x, y) }
func (b *Block) EmitOr(typ Type, x, y *Instr) *Instr   { return b.emitBin(OpOr, typ, x, y) }
func (b *Block) EmitXor(typ Type, x, y *Instr) *Instr  { return b.emitBin(OpXor, typ, x, y) }
func (b *Block) EmitShl(typ Type, x, y *Instr) *Instr  { return b.emitBin(OpShl, typ, x, y) }
func (b *Block) EmitLShr(typ Type, x, y *Instr) *Instr { return b.emitBin(OpLShr, typ, x, y) }
func (b *Block) EmitAShr(typ Type, x, y *Instr) *Instr { return b.emitBin(OpAShr, typ, x, y) }

// EmitICmp emits an integer comparison producing I1.
func (b *Block) EmitICmp(p Pred, x, y *Instr) *Instr {
	i := b.Func.newInstr(OpICmp, I1)
	i.Pred, i.Operands, i.Block = p, []*Instr{x, y}, b
	b.Instrs = append(b.Instrs, i)
	return i
}

// EmitAlloca reserves an in-memory cell; size is informational.
func (b *Block) EmitAlloca(name string, elem Type, size int) *Instr {
	i := b.Func.newInstr(OpAlloca, Ptr)
	i.Name, i.Elem, i.Int, i.Block = name, elem, int64(size), b
	b.Instrs = append(b.Instrs, i)
	return i
}

// EmitLoad loads *addr with result type typ.
func (b *Block) EmitLoad(typ Type, addr *Instr) *Instr {
	i := b.Func.newInstr(OpLoad, typ)
	i.Operands, i.Block = []*Instr{addr}, b
	b.Instrs = append(b.Instrs, i)
	return i
}

// EmitStore stores val to addr.
func (b *Block) EmitStore(addr, val *Instr) *Instr {
	i := b.Func.newInstr(OpStore, Void)
	i.Operands, i.Block = []*Instr{addr, val}, b
	b.Instrs = append(b.Instrs, i)
	return i
}

// EmitGEP computes address base + offset(elements).
func (b *Block) EmitGEP(base *Instr, offset int64) *Instr {
	i := b.Func.newInstr(OpGEP, Ptr)
	i.Operands, i.GepIdx, i.Block = []*Instr{base}, offset, b
	b.Instrs = append(b.Instrs, i)
	return i
}

// EmitCall emits a call to a registered host function.
func (b *Block) EmitCall(name string, typ Type, args ...*Instr) *Instr {
	i := b.Func.newInstr(OpCall, typ)
	i.Callee, i.Operands, i.Block = name, args, b
	b.Instrs = append(b.Instrs, i)
	return i
}

// PhiEdge pairs an incoming value with its predecessor block.
type PhiEdge struct {
	From  *Block
	Value *Instr
}

// EmitPhi emits a phi node.
func (b *Block) EmitPhi(typ Type, edges []PhiEdge) *Instr {
	i := b.Func.newInstr(OpPhi, typ)
	i.Block = b
	for _, e := range edges {
		i.Operands = append(i.Operands, e.Value)
		i.PredBlocks = append(i.PredBlocks, e.From)
	}
	b.Instrs = append(b.Instrs, i)
	return i
}

// SetRet terminates the block with return value (nil for void).
func (b *Block) SetRet(v *Instr) {
	b.Term = &Terminator{Op: TermRet, Args: nilIf(v)}
}

func nilIf(v *Instr) []*Instr {
	if v == nil {
		return nil
	}
	return []*Instr{v}
}

// SetBr terminates the block with an unconditional branch.
func (b *Block) SetBr(target *Block) {
	b.Term = &Terminator{Op: TermBr, Succs: []*Block{target}}
}

// SetCondBr terminates the block with a conditional branch.
func (b *Block) SetCondBr(cond *Instr, thenB, elseB *Block) {
	b.Term = &Terminator{Op: TermCond, Args: []*Instr{cond},
		Succs: []*Block{thenB, elseB}}
}

// Retarget replaces every terminator edge from old to new.
func (b *Block) Retarget(old, new *Block) {
	for idx, s := range b.Term.Succs {
		if s == old {
			b.Term.Succs[idx] = new
		}
	}
}

// Clone deep-copies the function, allocating fresh IDs for new
// instructions/blocks while preserving the structure and local names.
func (f *Func) Clone() *Func {
	c := NewFunc(f.Name)
	blockMap := map[*Block]*Block{}
	instrMap := map[*Instr]*Instr{}

	for _, b := range f.Blocks {
		nb := c.NewBlock(b.Name)
		blockMap[b] = nb
	}
	for _, p := range f.Params {
		np := c.newInstr(OpConst, p.Typ)
		np.Name, np.Int = p.Name, p.Int
		c.Params = append(c.Params, np)
		instrMap[p] = np
	}
	cloneInstr := func(i *Instr) *Instr {
		if ni, ok := instrMap[i]; ok {
			return ni
		}
		ni := c.newInstr(i.Op, i.Typ)
		ni.Name, ni.Int, ni.Pred, ni.GepIdx, ni.Callee, ni.Elem =
			i.Name, i.Int, i.Pred, i.GepIdx, i.Callee, i.Elem
		instrMap[i] = ni
		return ni
	}
	for _, b := range f.Blocks {
		nb := blockMap[b]
		for _, i := range b.Instrs {
			ni := cloneInstr(i)
			ni.Block = nb
			for _, op := range i.Operands {
				ni.Operands = append(ni.Operands, cloneInstr(op))
			}
			for _, pb := range i.PredBlocks {
				ni.PredBlocks = append(ni.PredBlocks, blockMap[pb])
			}
			nb.Instrs = append(nb.Instrs, ni)
		}
		if b.Term != nil {
			nt := &Terminator{Op: b.Term.Op}
			for _, a := range b.Term.Args {
				nt.Args = append(nt.Args, cloneInstr(a))
			}
			for _, s := range b.Term.Succs {
				nt.Succs = append(nt.Succs, blockMap[s])
			}
			nb.Term = nt
		}
	}
	return c
}

// VerifyError describes an IR structural problem.
type VerifyError struct{ Msg string }

func (e *VerifyError) Error() string { return "ir verify: " + e.Msg }

// Verify checks CFG/SSA structural well-formedness.
func (f *Func) Verify() error {
	if len(f.Blocks) == 0 {
		return &VerifyError{"no blocks"}
	}
	declared := map[*Instr]bool{}
	for _, p := range f.Params {
		declared[p] = true
	}
	for _, b := range f.Blocks {
		if b.Term == nil {
			return &VerifyError{"block " + b.Name + " has no terminator"}
		}
		seenPhi := true
		for _, i := range b.Instrs {
			if i.Block != b {
				return &VerifyError{"instr block linkage broken"}
			}
			declared[i] = true
			if i.Op != OpPhi {
				seenPhi = false
			} else if !seenPhi {
				return &VerifyError{"phi after non-phi in " + b.Name}
			}
			if i.Op == OpPhi && len(i.Operands) != len(i.PredBlocks) {
				return &VerifyError{"phi edge mismatch in " + b.Name}
			}
		}
	}
	for _, b := range f.Blocks {
		for _, s := range b.Term.Succs {
			if s == nil || s.Func != f {
				return &VerifyError{"bad successor from " + b.Name}
			}
		}
		for _, i := range b.Instrs {
			for _, op := range i.Operands {
				if !declared[op] {
					return &VerifyError{"use of undeclared value " + op.ValueName()}
				}
			}
			if i.Op == OpPhi {
				predSet := map[*Block]bool{}
				for _, pb := range i.PredBlocks {
					predSet[pb] = true
				}
				for _, pb := range predsOf(b) {
					if !predSet[pb] {
						return &VerifyError{"phi missing edge from " + pb.Name}
					}
				}
			}
		}
	}
	return nil
}

func predsOf(b *Block) []*Block {
	var out []*Block
	for _, p := range b.Func.Blocks {
		for _, s := range p.Succs() {
			if s == b {
				out = append(out, p)
			}
		}
	}
	return out
}
