package taskpkg

import "testing"

func TestMaxInputsFullEnumeration(t *testing.T) {
	// A balanced rule over all MaxInputs positions: full enumeration is
	// 2^12 = 4096 assignments per executor.
	checks := make([]*Stmt, MaxInputs)
	for i := 0; i < MaxInputs; i++ {
		checks[i] = checkStmt(100+i, pureCheck(i, "f"))
	}
	body := checks[0]
	for i := 1; i < MaxInputs; i++ {
		body = seqStmt(200+i, body, checks[i])
	}
	rule := &Rule{Inputs: MaxInputs, Body: body}
	prog := mustCompile(t, rule)

	for mask := 0; mask < 1<<MaxInputs; mask++ {
		inputs := decode(mask, MaxInputs)
		want, err := Interpret(rule, inputs)
		if err != nil {
			t.Fatal(err)
		}
		got, err := prog.Evaluate(inputs)
		if err != nil {
			t.Fatal(err)
		}
		if err := CompareTraces(want, got); err != nil {
			t.Fatalf("mask %b: %v", mask, err)
		}
	}
}
