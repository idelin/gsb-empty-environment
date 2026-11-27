package midend_test

import (
	"fmt"

	midend "prescreen.local/ps-4a45323f6e/midend"
)

// Example demonstrates the full pipeline: build IR, run interprocedural
// escape analysis, apply SROA, and compare the interpreter before/after.
func Example() {
	prog := midend.NewProgram()
	prog.Entry = "main"
	ty := midend.StructT("box", midend.F("a", midend.I64), midend.F("b", midend.I64))
	b := midend.NewBuilder(prog)

	b.Func("main", midend.I64)
	b.Alloc("o", true, ty)
	b.Field("pa", midend.R("o"), 0, midend.PtrT(midend.I64))
	b.Store(midend.R("pa"), midend.C(40, midend.I64))
	b.Field("pb", midend.R("o"), 1, midend.PtrT(midend.I64))
	b.Store(midend.R("pb"), midend.C(2, midend.I64))
	b.Load("x", midend.R("pa"), midend.I64)
	b.Load("y", midend.R("pb"), midend.I64)
	b.Bin("sum", "add", midend.I64, midend.R("x"), midend.R("y"))
	b.Print(midend.R("sum"))
	b.Ret(midend.R("sum"))

	esc := midend.AnalyzeEscape(prog)
	fmt.Println("o escapes:", esc.AllocEscaped(prog.Func("main"), "o"))

	before := midend.NewInterpreter(prog)
	beforeRes := before.Run()

	opt, sroa := midend.RunSROA(prog, esc)
	after := midend.NewInterpreter(opt)
	afterRes := after.Run()

	fmt.Println("heap allocs eliminated:", sroa.EliminatedHeapAllocs)
	fmt.Println("results equal:", beforeRes.Int == afterRes.Int)
	fmt.Println("output equal:", fmt.Sprint(before.Output) == fmt.Sprint(after.Output))
	// Output:
	// o escapes: false
	// heap allocs eliminated: 1
	// results equal: true
	// output equal: true
}
