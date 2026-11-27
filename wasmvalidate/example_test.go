package wasmvalidate_test

import (
	"fmt"

	"prescreen.local/ps-69c96972ee/wasmvalidate"
)

// This example validates the two headline polymorphic-stack cases: a concrete
// type suffix after unreachable that conflicts with a numeric instruction is
// rejected, while a fully unknown operand stack unifies legally.
func ExampleValidate() {
	mod := &wasmvalidate.Module{
		Types: []wasmvalidate.FuncType{{
			Params:  []wasmvalidate.ValType{wasmvalidate.I32},
			Results: []wasmvalidate.ValType{wasmvalidate.I64},
		}},
		Funcs:  []wasmvalidate.FuncType{},
		Tables: []wasmvalidate.TableType{{ElemType: wasmvalidate.FuncRef}},
	}

	conflict := &wasmvalidate.Body{
		Type: wasmvalidate.FuncType{},
		Instrs: []wasmvalidate.Instr{
			{Op: wasmvalidate.OpUnreachable},
			{Op: wasmvalidate.OpI32Const},
			{Op: wasmvalidate.OpI64Add},
		},
	}
	if err := wasmvalidate.Validate(mod, conflict); err != nil {
		fmt.Println("rejected:", err.(*wasmvalidate.ValidationError).Kind)
	}

	unified := &wasmvalidate.Body{
		Type: wasmvalidate.FuncType{Results: []wasmvalidate.ValType{wasmvalidate.I64}},
		Instrs: []wasmvalidate.Instr{
			{Op: wasmvalidate.OpUnreachable},
			{Op: wasmvalidate.OpI64Add},
		},
	}
	if err := wasmvalidate.Validate(mod, unified); err == nil {
		fmt.Println("accepted: unknown operands unify with i64")
	}

	// Output:
	// rejected: type mismatch
	// accepted: unknown operands unify with i64
}
