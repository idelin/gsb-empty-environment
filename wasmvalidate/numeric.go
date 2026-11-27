package wasmvalidate

// numSig is the fixed type signature of a numeric instruction.
type numSig struct {
	ins []ValType
	out []ValType
}

func bin(a, r ValType) numSig   { return numSig{ins: []ValType{a, a}, out: []ValType{r}} }
func unary(a, r ValType) numSig { return numSig{ins: []ValType{a}, out: []ValType{r}} }

// numericSigs covers every MVP numeric/comparison/conversion opcode plus the
// 0xFC sign-extension and saturating-truncation instructions and v128.const.
var numericSigs = map[Opcode]numSig{
	OpI32Const:  {out: []ValType{I32}},
	OpI64Const:  {out: []ValType{I64}},
	OpF32Const:  {out: []ValType{F32}},
	OpF64Const:  {out: []ValType{F64}},
	OpV128Const: {out: []ValType{V128}},

	OpI32Eqz: unary(I32, I32),
	OpI32Eq:  bin(I32, I32), OpI32Ne: bin(I32, I32),
	OpI32LtS: bin(I32, I32), OpI32LtU: bin(I32, I32),
	OpI32GtS: bin(I32, I32), OpI32GtU: bin(I32, I32),
	OpI32LeS: bin(I32, I32), OpI32LeU: bin(I32, I32),
	OpI32GeS: bin(I32, I32), OpI32GeU: bin(I32, I32),

	OpI64Eqz: unary(I64, I32),
	OpI64Eq:  bin(I64, I32), OpI64Ne: bin(I64, I32),
	OpI64LtS: bin(I64, I32), OpI64LtU: bin(I64, I32),
	OpI64GtS: bin(I64, I32), OpI64GtU: bin(I64, I32),
	OpI64LeS: bin(I64, I32), OpI64LeU: bin(I64, I32),
	OpI64GeS: bin(I64, I32), OpI64GeU: bin(I64, I32),

	OpF32Eq: bin(F32, I32), OpF32Ne: bin(F32, I32),
	OpF32Lt: bin(F32, I32), OpF32Gt: bin(F32, I32),
	OpF32Le: bin(F32, I32), OpF32Ge: bin(F32, I32),
	OpF64Eq: bin(F64, I32), OpF64Ne: bin(F64, I32),
	OpF64Lt: bin(F64, I32), OpF64Gt: bin(F64, I32),
	OpF64Le: bin(F64, I32), OpF64Ge: bin(F64, I32),

	OpI32Clz: unary(I32, I32), OpI32Ctz: unary(I32, I32), OpI32Popcnt: unary(I32, I32),
	OpI32Add: bin(I32, I32), OpI32Sub: bin(I32, I32), OpI32Mul: bin(I32, I32),
	OpI32DivS: bin(I32, I32), OpI32DivU: bin(I32, I32),
	OpI32RemS: bin(I32, I32), OpI32RemU: bin(I32, I32),
	OpI32And: bin(I32, I32), OpI32Or: bin(I32, I32), OpI32Xor: bin(I32, I32),
	OpI32Shl: bin(I32, I32), OpI32ShrS: bin(I32, I32), OpI32ShrU: bin(I32, I32),
	OpI32Rotl: bin(I32, I32), OpI32Rotr: bin(I32, I32),

	OpI64Clz: unary(I64, I64), OpI64Ctz: unary(I64, I64), OpI64Popcnt: unary(I64, I64),
	OpI64Add: bin(I64, I64), OpI64Sub: bin(I64, I64), OpI64Mul: bin(I64, I64),
	OpI64DivS: bin(I64, I64), OpI64DivU: bin(I64, I64),
	OpI64RemS: bin(I64, I64), OpI64RemU: bin(I64, I64),
	OpI64And: bin(I64, I64), OpI64Or: bin(I64, I64), OpI64Xor: bin(I64, I64),
	OpI64Shl: bin(I64, I64), OpI64ShrS: bin(I64, I64), OpI64ShrU: bin(I64, I64),
	OpI64Rotl: bin(I64, I64), OpI64Rotr: bin(I64, I64),

	OpF32Abs: unary(F32, F32), OpF32Neg: unary(F32, F32),
	OpF32Ceil: unary(F32, F32), OpF32Floor: unary(F32, F32),
	OpF32Trunc: unary(F32, F32), OpF32Nearest: unary(F32, F32), OpF32Sqrt: unary(F32, F32),
	OpF32Add: bin(F32, F32), OpF32Sub: bin(F32, F32), OpF32Mul: bin(F32, F32),
	OpF32Div: bin(F32, F32), OpF32Min: bin(F32, F32), OpF32Max: bin(F32, F32),
	OpF32Copysign: bin(F32, F32),

	OpF64Abs: unary(F64, F64), OpF64Neg: unary(F64, F64),
	OpF64Ceil: unary(F64, F64), OpF64Floor: unary(F64, F64),
	OpF64Trunc: unary(F64, F64), OpF64Nearest: unary(F64, F64), OpF64Sqrt: unary(F64, F64),
	OpF64Add: bin(F64, F64), OpF64Sub: bin(F64, F64), OpF64Mul: bin(F64, F64),
	OpF64Div: bin(F64, F64), OpF64Min: bin(F64, F64), OpF64Max: bin(F64, F64),
	OpF64Copysign: bin(F64, F64),

	OpI32WrapI64:   unary(I64, I32),
	OpI32TruncF32S: unary(F32, I32), OpI32TruncF32U: unary(F32, I32),
	OpI32TruncF64S: unary(F64, I32), OpI32TruncF64U: unary(F64, I32),
	OpI64ExtendI32S: unary(I32, I64), OpI64ExtendI32U: unary(I32, I64),
	OpI64TruncF32S: unary(F32, I64), OpI64TruncF32U: unary(F32, I64),
	OpI64TruncF64S: unary(F64, I64), OpI64TruncF64U: unary(F64, I64),
	OpF32ConvertI32S: unary(I32, F32), OpF32ConvertI32U: unary(I32, F32),
	OpF32ConvertI64S: unary(I64, F32), OpF32ConvertI64U: unary(I64, F32),
	OpF32DemoteF64:   unary(F64, F32),
	OpF64ConvertI32S: unary(I32, F64), OpF64ConvertI32U: unary(I32, F64),
	OpF64ConvertI64S: unary(I64, F64), OpF64ConvertI64U: unary(I64, F64),
	OpF64PromoteF32:     unary(F32, F64),
	OpI32ReinterpretF32: unary(F32, I32), OpI64ReinterpretF64: unary(F64, I64),
	OpF32ReinterpretI32: unary(I32, F32), OpF64ReinterpretI64: unary(I64, F64),

	OpI32Extend8S: unary(I32, I32), OpI32Extend16S: unary(I32, I32),
	OpI64Extend8S: unary(I64, I64), OpI64Extend16S: unary(I64, I64),
	OpI64Extend32S:    unary(I64, I64),
	OpI32TruncSatF32S: unary(F32, I32), OpI32TruncSatF32U: unary(F32, I32),
	OpI32TruncSatF64S: unary(F64, I32), OpI32TruncSatF64U: unary(F64, I32),
	OpI64TruncSatF32S: unary(F32, I64), OpI64TruncSatF32U: unary(F32, I64),
	OpI64TruncSatF64S: unary(F64, I64), OpI64TruncSatF64U: unary(F64, I64),
}

// execNumeric applies a fixed numeric signature against the polymorphic
// operand stack and pushes the result types.
func (v *validator) execNumeric(ins Instr) {
	sig, ok := numericSigs[ins.Op]
	if !ok {
		v.fail(ErrUnknownOpcode, "opcode %#x is not supported by the function-body validator",
			uint16(ins.Op))
	}
	v.expectVals(sig.ins)
	for _, t := range sig.out {
		v.pushVal(t)
	}
}
