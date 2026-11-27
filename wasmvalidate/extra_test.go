package wasmvalidate

import "testing"

// 5. Untyped select bidirectional constraints on polymorphic stacks.
func TestUntypedSelectPolymorphic(t *testing.T) {
	mod := baseModule()

	// unreachable; i32; i64; i32(cond); select -> i32 vs i64 conflict.
	mismatch := body(nil, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpI32Const},
		Instr{Op: OpI64Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpSelect},
	)
	assertErr(t, Validate(mod, mismatch), ErrTypeMismatch, 4)

	// unreachable; select -> condition and both operands are Unknown, so the
	// result stays Unknown and unifies with the i32 function result.
	allUnknown := body([]ValType{I32}, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpSelect},
	)
	assertOK(t, Validate(mod, allUnknown))

	// Concrete equal numeric operands type-check normally.
	concrete := body([]ValType{I32}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpSelect},
	)
	assertOK(t, Validate(mod, concrete))

	// Reference types are not numeric/vector and cannot feed untyped select.
	refReject := body(nil, []ValType{FuncRef},
		Instr{Op: OpLocalGet, LocalIndex: 0},
		Instr{Op: OpLocalGet, LocalIndex: 0},
		Instr{Op: OpI32Const},
		Instr{Op: OpSelect},
	)
	assertErr(t, Validate(mod, refReject), ErrSelectType, 3)

	// One Unknown plus one concrete numeric type unifies to the concrete one.
	oneUnknown := body([]ValType{I64}, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpI64Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpSelect},
	)
	assertOK(t, Validate(mod, oneUnknown))

	// Typed select enforces the declared result type on both operands.
	typedReject := body(nil, nil,
		Instr{Op: OpI64Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpTypedSelect, ValTypes: []ValType{I32}},
	)
	assertErr(t, Validate(mod, typedReject), ErrTypeMismatch, 3)
}

// 6. br_table checks every target label, defaults, arity and depth bounds.
func TestBrTable(t *testing.T) {
	mod := baseModule()

	happy := body([]ValType{I32}, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{0, 1}, Depth: 0},
		Instr{Op: OpEnd},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, happy))

	// Outer block exposes [i64] label types: target 1 clashes with the [i32]
	// default label.
	clash := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I64)},
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{0, 1}, Depth: 0},
		Instr{Op: OpEnd},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, clash), ErrBranchTypeMismatch, 4)

	// Out-of-range target depth.
	outOfRange := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{5}, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, outOfRange), ErrUnknownLabel, 3)

	// Out-of-range default depth is checked too.
	badDefault := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{0}, Depth: 4},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, badDefault), ErrUnknownLabel, 3)

	// Nested blocks: inner label depth 0 = [i32], outer label depth 1 = [i64].
	// A target whose label arity differs from the default is rejected below;
	// here both label lists are made arity-compatible via matching i32 frames.
	loopTable := body([]ValType{I32}, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{1}, Depth: 1},
		Instr{Op: OpEnd},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, loopTable))

	// Empty target list is malformed.
	empty := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: nil, Depth: 0},
	)
	assertErr(t, Validate(mod, empty), ErrEmptyBrTable, 1)

	// Plain br depth out of range.
	badBr := body(nil, nil,
		Instr{Op: OpBr, Depth: 1}, // only the function frame (depth 0) exists
	)
	assertErr(t, Validate(mod, badBr), ErrUnknownLabel, 0)

	// The outermost function frame is a valid label with the result types.
	brFuncFrame := body([]ValType{I64}, nil,
		Instr{Op: OpI64Const},
		Instr{Op: OpBr, Depth: 0},
	)
	assertOK(t, Validate(mod, brFuncFrame))

	// br_table inside an empty loop: loop label is [] (no params), the
	// condition operand is the only stack requirement.
	loopLabel := body(nil, nil,
		Instr{Op: OpLoop, BlockType: btEmpty()},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{0}, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, loopLabel))

	// A loop [i32]->[i64] exposes start types [i32] on its label. Feeding an
	// i64 to a br_table targeting it fails even though its end type is i64,
	// proving the loop label is taken from start_types.
	loopLabelWrong := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpLoop, BlockType: btType(0)}, // type 0: [i32]->[i64]
		Instr{Op: OpI64Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{0}, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, loopLabelWrong), ErrTypeMismatch, 4)
}

// 7. Function end stack must match the result signature exactly.
func TestFunctionEndArity(t *testing.T) {
	mod := baseModule()

	emptyOK := body(nil, nil, Instr{Op: OpNop})
	assertOK(t, Validate(mod, emptyOK))

	missing := body([]ValType{I32}, nil, Instr{Op: OpNop})
	assertErr(t, Validate(mod, missing), ErrStackUnderflow, -1)

	extra := body(nil, nil, Instr{Op: OpI32Const})
	assertErr(t, Validate(mod, extra), ErrExtraOperands, -1)

	wrongType := body([]ValType{I64}, nil, Instr{Op: OpI32Const})
	assertErr(t, Validate(mod, wrongType), ErrTypeMismatch, -1)

	unclosed := body(nil, nil, Instr{Op: OpBlock, BlockType: btEmpty()})
	assertErr(t, Validate(mod, unclosed), ErrUnclosedBlock, -1)

	strayEnd := body(nil, nil, Instr{Op: OpEnd})
	assertErr(t, Validate(mod, strayEnd), ErrUnexpectedEnd, 0)
}

// 8. Globals, locals, calls, else placement and return.
func TestVariablesCallsAndMisc(t *testing.T) {
	mod := baseModule()

	// Writing the immutable global 0 is rejected; global 1 is mutable.
	immutable := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpGlobalSet, GlobalIndex: 0},
	)
	assertErr(t, Validate(mod, immutable), ErrImmutableGlobal, 1)

	mutableOK := body(nil, nil,
		Instr{Op: OpI64Const},
		Instr{Op: OpGlobalSet, GlobalIndex: 1},
	)
	assertOK(t, Validate(mod, mutableOK))

	badGlobal := body(nil, nil, Instr{Op: OpGlobalGet, GlobalIndex: 9})
	assertErr(t, Validate(mod, badGlobal), ErrUnknownGlobal, 0)

	// locals: parameter 0 is i32, local 1 is i64.
	localOK := body([]ValType{I64}, []ValType{I32, I64},
		Instr{Op: OpLocalGet, LocalIndex: 0},
		Instr{Op: OpLocalSet, LocalIndex: 0},
		Instr{Op: OpLocalGet, LocalIndex: 1},
	)
	assertOK(t, Validate(mod, localOK))

	localTypeMismatch := body(nil, []ValType{I32},
		Instr{Op: OpI64Const},
		Instr{Op: OpLocalSet, LocalIndex: 0},
	)
	assertErr(t, Validate(mod, localTypeMismatch), ErrTypeMismatch, 1)

	badLocal := body(nil, nil, Instr{Op: OpLocalGet, LocalIndex: 0})
	assertErr(t, Validate(mod, badLocal), ErrUnknownLocal, 0)

	// call: func 0 is [i32]->[i64].
	callOK := body([]ValType{I64}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpCall, FuncIndex: 0},
	)
	assertOK(t, Validate(mod, callOK))

	callBadArg := body(nil, nil,
		Instr{Op: OpI64Const},
		Instr{Op: OpCall, FuncIndex: 0},
	)
	assertErr(t, Validate(mod, callBadArg), ErrTypeMismatch, 1)

	badFunc := body(nil, nil, Instr{Op: OpCall, FuncIndex: 7})
	assertErr(t, Validate(mod, badFunc), ErrUnknownFunc, 0)

	// call_indirect through table 0 with type 1 [i32]->[i32].
	indirectOK := body([]ValType{I32}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpCallIndirect, TableIndex: 0, TypeIndex: 1},
	)
	assertOK(t, Validate(mod, indirectOK))

	badTable := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpCallIndirect, TableIndex: 3, TypeIndex: 1},
	)
	assertErr(t, Validate(mod, badTable), ErrUnknownTable, 1)

	// else outside an if is illegal.
	strayElse := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btEmpty()},
		Instr{Op: OpElse},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, strayElse), ErrUnexpectedElse, 1)

	// return must match the function result types.
	returnOK := body([]ValType{I64}, nil,
		Instr{Op: OpI64Const},
		Instr{Op: OpReturn},
	)
	assertOK(t, Validate(mod, returnOK))

	returnMismatch := body([]ValType{I64}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpReturn},
	)
	assertErr(t, Validate(mod, returnMismatch), ErrTypeMismatch, 1)

	// Code after return is still type-checked polymorphically.
	afterReturn := body(nil, nil,
		Instr{Op: OpReturn},
		Instr{Op: OpI32Const},
		Instr{Op: OpI64Add},
	)
	assertErr(t, Validate(mod, afterReturn), ErrTypeMismatch, 2)

	// local.tee pushes the value back and must match the local type.
	teeOK := body([]ValType{I32}, []ValType{I32},
		Instr{Op: OpI32Const},
		Instr{Op: OpLocalTee, LocalIndex: 0},
	)
	assertOK(t, Validate(mod, teeOK))

	teeMismatch := body(nil, []ValType{I32},
		Instr{Op: OpI64Const},
		Instr{Op: OpLocalTee, LocalIndex: 0},
	)
	assertErr(t, Validate(mod, teeMismatch), ErrTypeMismatch, 1)
}

// 9. br_if pops condition and label inputs, then pushes the label types.
func TestBrIfPolymorphic(t *testing.T) {
	mod := baseModule()

	ok := body([]ValType{I64}, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I64)},
		Instr{Op: OpI64Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrIf, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, ok))

	// Missing condition underflows; in a reachable frame that is an error.
	noCond := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btEmpty()},
		Instr{Op: OpBrIf, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, noCond), ErrStackUnderflow, 1)

	// Unreachable frame supplies an Unknown condition and Unknown label input.
	poly := body([]ValType{I64}, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I64)},
		Instr{Op: OpUnreachable},
		Instr{Op: OpBrIf, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, poly))
}
