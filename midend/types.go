package midend

// pointeeStruct resolves the struct type addressed by a FieldAddr using the
// enclosing function's register-type table.
func pointeeStruct(types map[string]Type, fa *FieldAddr) *StructType {
	pt := typeOfReg(types, fa.Ptr).(*PtrType)
	return pt.Elem.(*StructType)
}

// typeOfReg resolves an operand type through the function type table.
func typeOfReg(types map[string]Type, o Operand) Type {
	if o.IsConst() {
		if o.Ty != nil {
			return o.Ty
		}
		return I64
	}
	return types[o.Reg]
}

// regTypes builds a map from virtual register name to its static type for a
// function. Parameters and every value-producing instruction contribute.
func regTypes(f *Func) map[string]Type {
	m := map[string]Type{}
	for _, p := range f.Params {
		m[p.Name] = p.Ty
	}
	for _, b := range f.Blocks {
		for _, ins := range b.Insts {
			if ins.GetName() != "" {
				m[ins.GetName()] = ins.GetType()
			}
		}
	}
	return m
}

// typeOf resolves the static type of an operand inside a function.
func (in *Interpreter) typeOf(o Operand) Type {
	if o.IsConst() {
		if o.Ty != nil {
			return o.Ty
		}
		return I64
	}
	f := in.cur().fn
	if ty, ok := in.typeCache[f][o.Reg]; ok {
		return ty
	}
	panic("midend: cannot resolve type of %" + o.Reg)
}
