package wasmvalidate

import (
	"errors"
	"strings"
	"testing"
)

// br_table label arity mismatch (different lengths) is reported precisely.
func TestBrTableArityMismatch(t *testing.T) {
	mod := baseModule()

	arity := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btEmpty()},
		Instr{Op: OpBlock, BlockType: btSingle(I32)},
		Instr{Op: OpI32Const},
		Instr{Op: OpBrTable, Targets: []uint32{1}, Depth: 0},
		Instr{Op: OpEnd},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, arity), ErrBranchTypeMismatch, 3)

	// Out-of-range br_if depth.
	// Function frame (depth 0) with empty result label only needs a condition.
	brIfFuncFrame := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpBrIf, Depth: 0},
	)
	assertOK(t, Validate(mod, brIfFuncFrame))
	badBrIf2 := body(nil, nil, Instr{Op: OpBrIf, Depth: 2})
	assertErr(t, Validate(mod, badBrIf2), ErrUnknownLabel, 0)
}

// Extra operands inside a structured block are rejected at its end.
func TestBlockExtraOperands(t *testing.T) {
	mod := baseModule()

	extra := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btEmpty()},
		Instr{Op: OpI32Const},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, extra), ErrExtraOperands, 2)

	// An unreachable block still must produce the declared end types.
	missingEnd := body([]ValType{I64}, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I64)},
		Instr{Op: OpUnreachable},
		Instr{Op: OpEnd},
	)
	assertOK(t, Validate(mod, missingEnd)) // Unknown unifies with i64

	// ...but a concrete wrong type inside unreachable code still conflicts.
	concreteWrong := body(nil, nil,
		Instr{Op: OpBlock, BlockType: btSingle(I64)},
		Instr{Op: OpUnreachable},
		Instr{Op: OpI32Const},
		Instr{Op: OpI64Add}, // Unknown + concrete i32 fails against i64
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, concreteWrong), ErrTypeMismatch, 3)
}

// Invalid block value types and duplicate else are classified correctly.
func TestInvalidBlockAndElse(t *testing.T) {
	mod := baseModule()

	badSingle := body(nil, nil,
		Instr{Op: OpBlock, BlockType: BlockType{SingleValid: true, Single: Unknown}},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, badSingle), ErrInvalidValType, 0)

	dupElseOK := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpIf, BlockType: btEmpty()},
		Instr{Op: OpElse},
		Instr{Op: OpElse},
		Instr{Op: OpEnd},
	)
	assertErr(t, Validate(mod, dupElseOK), ErrUnexpectedElse, 3)

	badCallType := body(nil, nil,
		Instr{Op: OpI32Const},
		Instr{Op: OpCallIndirect, TableIndex: 0, TypeIndex: 42},
	)
	assertErr(t, Validate(mod, badCallType), ErrUnknownTypeIndex, 1)
}

// ValidationError carries a readable message including the instruction index,
// and negative-index errors use the non-indexed form.
func TestValidationErrorFormat(t *testing.T) {
	mod := baseModule()
	err := Validate(mod, body(nil, nil, Instr{Op: OpI32Const}))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if ve.InstrIndex != -1 || !strings.Contains(ve.Error(), "extra operands") {
		t.Fatalf("unexpected error: %v", ve)
	}

	err = Validate(mod, body(nil, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpI32Const},
		Instr{Op: OpI64Add},
	))
	if !errors.As(err, &ve) || ve.InstrIndex != 2 ||
		!strings.Contains(ve.Error(), "at instruction 2") {
		t.Fatalf("expected indexed error, got %v", err)
	}

	if got := V128.String(); got != "v128" {
		t.Fatalf("V128.String() = %q", got)
	}
	if got := (ValType(0x01)).String(); got != "invalid" {
		t.Fatalf("invalid String() = %q", got)
	}

	if err := Validate(nil, nil); err == nil {
		t.Fatal("expected error for nil inputs")
	}
}
