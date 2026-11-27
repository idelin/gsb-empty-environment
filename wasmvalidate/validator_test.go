package wasmvalidate

import (
	"errors"
	"testing"
)

func btEmpty() BlockType           { return BlockType{Empty: true} }
func btSingle(t ValType) BlockType { return BlockType{SingleValid: true, Single: t} }
func btType(idx int64) BlockType   { return BlockType{TypeIndex: idx} }

func baseModule() *Module {
	return &Module{
		Types: []FuncType{
			{Params: []ValType{I32}, Results: []ValType{I64}}, // type 0: [i32]->[i64]
			{Params: []ValType{I32}, Results: []ValType{I32}}, // type 1: [i32]->[i32]
			{Params: []ValType{I32}, Results: nil},            // type 2: [i32]->[]
		},
		Funcs: []FuncType{
			{Params: []ValType{I32}, Results: []ValType{I64}}, // func 0 uses type 0
		},
		Tables:  []TableType{{ElemType: FuncRef}},
		Globals: []GlobalType{{ValType: I32, Mutable: false}, {ValType: I64, Mutable: true}},
	}
}

func body(results []ValType, locals []ValType, instrs ...Instr) *Body {
	return &Body{
		Type:   FuncType{Results: results},
		Locals: locals,
		Instrs: instrs,
	}
}

func assertErr(t *testing.T, err error, kind ErrorKind, index int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %q at index %d, validation passed", kind, index)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Kind != kind {
		t.Fatalf("expected kind %q, got %q (message: %s)", kind, ve.Kind, ve.Message)
	}
	if ve.InstrIndex != index {
		t.Fatalf("expected instruction index %d, got %d (message: %s)", index, ve.InstrIndex, ve.Message)
	}
}

func assertOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Fatalf("unexpected validation error kind %q at index %d: %s", ve.Kind, ve.InstrIndex, ve.Message)
		}
		t.Fatalf("unexpected error: %v", err)
	}
}

// 1. Polymorphic unreachable stack: concrete suffix conflict must be rejected,
// while fully unknown operands unify legally.
func TestPolymorphicUnreachable(t *testing.T) {
	mod := baseModule()

	// unreachable; i32.const 1; i64.add -> known i32 conflicts with i64.add.
	reject := body(nil, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpI32Const},
		Instr{Op: OpI64Add},
	)
	assertErr(t, Validate(mod, reject), ErrTypeMismatch, 2)

	// unreachable; i64.add -> two Unknowns unify with i64, result i64 pushed.
	accept := body([]ValType{I64}, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpI64Add},
	)
	assertOK(t, Validate(mod, accept))

	// Unreachable code is still checked: an unknown opcode after unreachable
	// must not be silently skipped.
	badOpcode := body(nil, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: Opcode(0x09)},
	)
	assertErr(t, Validate(mod, badOpcode), ErrUnknownOpcode, 1)
}

// 2. Multi-value block (param i32) (result i64) via type index blocktype.
func TestMultiValueBlock(t *testing.T) {
	mod := baseModule()

	accept := body([]ValType{I64}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpBlock, BlockType: btType(0)},
		Instr{Op: OpI32Const},
		Instr{Op: OpI64ExtendI32S},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, accept))

	// Feeding i64 where the block parameter demands i32.
	wrongParam := body(nil, nil,
		Instr{Op: OpI64Const},
		Instr{Op: OpBlock, BlockType: btType(0)},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, wrongParam), ErrTypeMismatch, 1)

	// Block end leaves the wrong result type.
	wrongEnd := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpBlock, BlockType: btType(0)},
		Instr{Op: OpI32Const},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, wrongEnd), ErrTypeMismatch, 3)

	// Unknown block type index.
	badType := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btType(99)},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, badType), ErrUnknownTypeIndex, 0)
}

// 3. Loop labels carry start_types while block labels carry end_types.
func TestLoopVsBlockLabelReversal(t *testing.T) {
	mod := baseModule()

	// loop [i32]->[i64]: br 0 labels are the start types [i32], so an i32
	// operand satisfies the branch.
	loopOK := body([]ValType{I64}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpLoop, BlockType: btType(0)},
		Instr{Op: OpI32Const},
		Instr{Op: OpBr, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, loopOK))

	// Same shape with a block: br 0 labels are end types [i64]; an i32
	// operand now fails, proving the label-type reversal.
	blockReject := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpBlock, BlockType: btType(0)},
		Instr{Op: OpI32Const},
		Instr{Op: OpBr, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, blockReject), ErrTypeMismatch, 3)

	// The block form becomes valid once the branch carries an i64.
	blockOK := body([]ValType{I64}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpBlock, BlockType: btType(0)},
		Instr{Op: OpI64Const},
		Instr{Op: OpBr, Depth: 0},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, blockOK))
}

// 4. An if without else requires identical start/end signatures; adding an
// else lifts that restriction.
func TestIfWithoutElse(t *testing.T) {
	mod := baseModule()

	noElseReject := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpIf, BlockType: btType(0)}, // [i32]->[i64], signatures differ
		Instr{Op: OpI64Const},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, noElseReject), ErrIfNoElseMultiValue, 4)

	withElseOK := body([]ValType{I64}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpIf, BlockType: btType(0)},
		Instr{Op: OpI64Const},
		Instr{Op: OpElse},
		Instr{Op: OpI64ExtendI32S},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, withElseOK))

	// Equal start/end signatures ([i32]->[i32]) are fine without else.
	equalSigsOK := body([]ValType{I32}, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpIf, BlockType: btType(1)},
		Instr{Op: OpI32Const},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, equalSigsOK))

	// if still consumes an i32 condition.
	missingCond := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpIf, BlockType: btType(1)},
		Instr{Op: OpI32Const},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, missingCond), ErrStackUnderflow, 1)
}
