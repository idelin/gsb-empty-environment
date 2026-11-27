package wasmvalidate

import (
	"errors"
	"testing"
)

// Every numeric signature is exercised once, including conversion, 0xFC
// sign-extension/saturating-truncation and the v128 producer used by select.
func TestNumericSignatures(t *testing.T) {
	mod := baseModule()
	for op, sig := range numericSigs {
		instrs := make([]Instr, 0, len(sig.ins)+len(sig.out)+2)
		for _, t := range sig.ins {
			instrs = append(instrs, constFor(t))
		}
		instrs = append(instrs, Instr{Op: op})
		// Validate inside a function whose results match the producer output.
		b := body(append([]ValType(nil), sig.out...), nil, instrs...)
		if err := Validate(mod, b); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				t.Fatalf("opcode %#x: unexpected %s at %d: %s",
					uint16(op), ve.Kind, ve.InstrIndex, ve.Message)
			}
			t.Fatalf("opcode %#x: %v", uint16(op), err)
		}
	}
}

// Wrong operand types on representative unary/binary instructions are
// rejected even after unreachable (concrete suffix conflict rule).
func TestNumericPolymorphicConflict(t *testing.T) {
	mod := baseModule()

	cases := []struct {
		name string
		op   Opcode
	}{
		{"i64 add", OpI64Add},
		{"f32 add", OpF32Add},
		{"f64 sqrt", OpF64Sqrt},
		{"i32 trunc f32 signed", OpI32TruncF32S},
		{"i64 extend 8", OpI64Extend8S},
		{"i64 trunc sat f64 unsigned", OpI64TruncSatF64U},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := numericSigs[tc.op]
			instrs := []Instr{{Op: OpUnreachable}}
			// Push one concrete wrong-typed value on top of the Unknown base.
			instrs = append(instrs, Instr{Op: OpI32Const})
			instrs = append(instrs, Instr{Op: tc.op})
			b := body(append([]ValType(nil), sig.out...), nil, instrs...)
			assertErr(t, Validate(mod, b), ErrTypeMismatch, 2)
		})
	}
}

// v128.const feeds a vector type through untyped select and drop.
func TestV128Select(t *testing.T) {
	mod := baseModule()

	ok := body(nil, nil,
		Instr{Op: OpV128Const},
		Instr{Op: OpV128Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpSelect},
		Instr{Op: OpDrop},
	)
	assertOK(t, Validate(mod, ok))

	// v128 versus i32 operands conflict in untyped select.
	clash := body(nil, nil,
		Instr{Op: OpV128Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpI32Const},
		Instr{Op: OpSelect},
	)
	assertErr(t, Validate(mod, clash), ErrTypeMismatch, 3)

	// Two Unknowns under unreachable keep Unknown, which drop accepts.
	poly := body(nil, nil,
		Instr{Op: OpUnreachable},
		Instr{Op: OpSelect},
		Instr{Op: OpDrop},
	)
	assertOK(t, Validate(mod, poly))
}

func constFor(t ValType) Instr {
	switch t {
	case I32:
		return Instr{Op: OpI32Const}
	case I64:
		return Instr{Op: OpI64Const}
	case F32:
		return Instr{Op: OpF32Const}
	case F64:
		return Instr{Op: OpF64Const}
	case V128:
		return Instr{Op: OpV128Const}
	default:
		return Instr{Op: OpNop}
	}
}
