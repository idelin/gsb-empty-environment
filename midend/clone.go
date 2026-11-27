package midend

// CloneProgram returns a deep structural copy so optimization passes never
// mutate the input module.
func CloneProgram(p *Program) *Program {
	out := NewProgram()
	for _, g := range p.Globals {
		out.AddGlobal(&Global{Name: g.Name, Ty: g.Ty})
	}
	// Re-link global references to the cloned global objects.
	globMap := map[*Global]*Global{}
	for i, g := range p.Globals {
		globMap[g] = out.Globals[i]
	}
	for _, f := range p.Funcs {
		out.AddFunc(cloneFunc(f, globMap))
	}
	out.Entry = p.Entry
	return out
}

func cloneFunc(f *Func, globMap map[*Global]*Global) *Func {
	nf := &Func{Name: f.Name, Ret: f.Ret}
	for _, pm := range f.Params {
		nf.Params = append(nf.Params, &Param{Name: pm.Name, Ty: pm.Ty})
	}
	for _, b := range f.Blocks {
		nb := &Block{Name: b.Name, Term: cloneTerm(b.Term)}
		for _, ins := range b.Insts {
			nb.Insts = append(nb.Insts, cloneInst(ins, globMap))
		}
		nf.Blocks = append(nf.Blocks, nb)
	}
	return nf
}

func cloneTerm(t Term) Term {
	switch x := t.(type) {
	case *Jump:
		return &Jump{To: x.To}
	case *Branch:
		return &Branch{Cond: x.Cond, T: x.T, F: x.F}
	case *Return:
		return &Return{Val: x.Val}
	default:
		return t
	}
}

func cloneEdges(edges []PhiEdge) []PhiEdge {
	out := make([]PhiEdge, len(edges))
	for i, e := range edges {
		out[i] = PhiEdge{Block: e.Block, Val: e.Val}
	}
	return out
}

func cloneOps(ops []Operand) []Operand {
	out := make([]Operand, len(ops))
	copy(out, ops)
	return out
}

func cloneInst(ins Inst, globMap map[*Global]*Global) Inst {
	switch x := ins.(type) {
	case *Alloc:
		return &Alloc{Name: x.Name, Heap: x.Heap, Ty: x.Ty}
	case *GlobalAddr:
		return &GlobalAddr{Name: x.Name, G: globMap[x.G]}
	case *FieldAddr:
		return &FieldAddr{Name: x.Name, Ty: x.Ty, Ptr: x.Ptr, Field: x.Field}
	case *AddrAdd:
		return &AddrAdd{Name: x.Name, Ptr: x.Ptr, Off: x.Off, Ty: x.Ty}
	case *Load:
		return &Load{Name: x.Name, Ty: x.Ty, Ptr: x.Ptr}
	case *Store:
		return &Store{Ptr: x.Ptr, Val: x.Val}
	case *Phi:
		return &Phi{Name: x.Name, Ty: x.Ty, Incoming: cloneEdges(x.Incoming)}
	case *Call:
		return &Call{Name: x.Name, Ty: x.Ty, Callee: x.Callee, Args: cloneOps(x.Args)}
	case *ExtCall:
		return &ExtCall{Name: x.Name, Ty: x.Ty, Callee: x.Callee, Args: cloneOps(x.Args)}
	case *BinOp:
		return &BinOp{Name: x.Name, Ty: x.Ty, Op: x.Op, X: x.X, Y: x.Y}
	case *PrintInst:
		return &PrintInst{Val: x.Val}
	default:
		return ins
	}
}

// rewriteOperands applies fn to every operand referenced inside a function,
// including phi edges, call arguments and terminators.
func rewriteOperands(f *Func, fn func(Operand) Operand) {
	for _, b := range f.Blocks {
		for _, ins := range b.Insts {
			switch x := ins.(type) {
			case *FieldAddr:
				x.Ptr = fn(x.Ptr)
			case *AddrAdd:
				x.Ptr = fn(x.Ptr)
				x.Off = fn(x.Off)
			case *Load:
				x.Ptr = fn(x.Ptr)
			case *Store:
				x.Ptr = fn(x.Ptr)
				x.Val = fn(x.Val)
			case *Phi:
				for i := range x.Incoming {
					x.Incoming[i].Val = fn(x.Incoming[i].Val)
				}
			case *Call:
				for i := range x.Args {
					x.Args[i] = fn(x.Args[i])
				}
			case *ExtCall:
				for i := range x.Args {
					x.Args[i] = fn(x.Args[i])
				}
			case *BinOp:
				x.X = fn(x.X)
				x.Y = fn(x.Y)
			case *PrintInst:
				x.Val = fn(x.Val)
			}
		}
		switch t := b.Term.(type) {
		case *Branch:
			t.Cond = fn(t.Cond)
		case *Return:
			t.Val = fn(t.Val)
		}
	}
}
