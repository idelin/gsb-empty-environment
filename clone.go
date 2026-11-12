package netsec

// Clone 深拷贝一个 SSA 函数，使插桩结果不影响原图（便于解释器对照）。
func Clone(f *Func) *Func {
	if f == nil {
		return nil
	}
	c := &Func{
		Name:      f.Name,
		Params:    append([]Reg(nil), f.Params...),
		NumRegs:   f.NumRegs,
		nextBlock: f.nextBlock,
		nextInstr: f.nextInstr,
	}
	for _, b := range f.Blocks {
		nb := &Block{ID: b.ID}
		for _, ins := range b.Instrs {
			ni := *ins
			ni.Args = append([]ValueID(nil), ins.Args...)
			ni.Succ = append([]BlockID(nil), ins.Succ...)
			ni.Incoming = append([]PhiEdge(nil), ins.Incoming...)
			nb.Instrs = append(nb.Instrs, &ni)
		}
		c.Blocks = append(c.Blocks, nb)
	}
	return c
}
