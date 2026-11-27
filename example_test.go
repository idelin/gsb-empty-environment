package minijit_test

import (
	"fmt"

	"prescreen.local/ps-ccd62f6f18"
)

// buildExampleProgram builds box(a,b) that returns an object whose r field
// holds a+b. Integer overflow promotes to float, which the speculative tier
// treats as a failed integer-add guard.
func buildExampleProgram() *minijit.Program {
	prog := minijit.NewProgram()

	// box: local0=a, local1=b, local2=scratch object.
	box := minijit.NewFunction("box", 2, 3)
	kClass := box.AddConst(minijit.StringValue("Box"))
	kField := box.AddConst(minijit.StringValue("r"))
	box.Emit(minijit.OpLoadConst, 0, kClass)
	box.Emit(minijit.OpNewObject, 0, 0)
	box.Emit(minijit.OpStoreLocal, 2, 0) // local2 = new Box
	box.Emit(minijit.OpLoadLocal, 2, 0)  // obj
	box.Emit(minijit.OpLoadLocal, 0, 0)  // a
	box.Emit(minijit.OpLoadLocal, 1, 0)  // b
	box.Emit(minijit.OpAdd, 0, 0)        // a+b
	box.Emit(minijit.OpLoadConst, 0, kField)
	box.Emit(minijit.OpSetField, 0, 0) // obj.r = a+b
	box.Emit(minijit.OpLoadLocal, 2, 0)
	box.Emit(minijit.OpReturn, 0, 0)
	prog.Add(box)

	// main() calls box(MaxInt64, 1).
	main := minijit.NewFunction("main", 0, 1)
	kBox := main.AddConst(minijit.FunctionValue(box))
	kMax := main.AddConst(minijit.IntValue(9223372036854775807))
	kOne := main.AddConst(minijit.IntValue(1))
	main.Emit(minijit.OpLoadConst, 0, kBox)
	main.Emit(minijit.OpLoadConst, 0, kMax)
	main.Emit(minijit.OpLoadConst, 0, kOne)
	main.Emit(minijit.OpCall, 2, 0)
	main.Emit(minijit.OpReturn, 0, 0)
	prog.Add(main)
	return prog
}

func ExampleOptimizedRunner() {
	prog := buildExampleProgram()

	base, err := minijit.NewInterpreter(prog).Run("main", nil)
	if err != nil {
		panic(err)
	}

	runner := minijit.NewOptimizedRunner(prog)
	runner.OnDeopt = func(reason string, frames int) {
		fmt.Printf("deopt: %s across %d frames\n", reason, frames)
	}
	opt, err := runner.Run("main", nil)
	if err != nil {
		panic(err)
	}

	r, _ := opt.AsObject().Field("r")
	fmt.Printf("deopts=%d field=%v equal=%v\n",
		runner.DeoptCount(), r.AsFloat(), minijit.ResultsEquivalent(base, opt))
	// Output:
	// deopt: integer overflow in Add across 2 frames
	// deopts=1 field=9.223372036854776e+18 equal=true
}
