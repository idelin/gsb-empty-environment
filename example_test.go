package netsec

import "fmt"

// ExampleVerify demonstrates the headline scenario: a component whose return
// value is constant while a secret decides whether an exception is raised.
func ExampleVerify() {
	program := assertProgram()

	// The host is only needed for OpHost instructions; assertProgram uses none.
	result, err := Verify(program, nil, nil, 0)
	if err != nil {
		panic(err)
	}

	ce := result.Counterexample
	fmt.Println("verdict:", result.Verdict)
	fmt.Println("exc A:", ce.ObservationA.Exc)
	fmt.Println("exc B:", ce.ObservationB.Exc)

	// Replay each counterexample sequence independently, without a host.
	if _, err := Replay(program, ce.TraceA); err != nil {
		panic(err)
	}
	if _, err := Replay(program, ce.TraceB); err != nil {
		panic(err)
	}
	fmt.Println("replay: ok")

	// Output:
	// verdict: LeakFound
	// exc A: ExcAssertion
	// exc B: ExcNone
	// replay: ok
}

// ExampleRun shows a single bounded execution and its host-call trace.
func ExampleRun() {
	program := &Program{
		Vars: []Variable{
			{Name: "secret", Visibility: Secret, Bits: 8, Init: 6},
			{Name: "out", Visibility: Public, Bits: 8},
		},
		Code: []Instr{
			{Op: OpHost, Dst: 1, Src: 0, HostID: 2},
			{Op: OpReturn, Src: 1},
		},
	}
	host := HostFunc(func(id uint8, arg uint64) (uint64, bool) {
		return arg * arg, false
	})

	trace, err := Run(program, nil, host)
	if err != nil {
		panic(err)
	}
	obs := trace.Observe()
	fmt.Println("return:", obs.Return)
	fmt.Println("calls:", len(obs.Calls), "arg:", obs.Calls[0].Arg)

	// Output:
	// return: 36
	// calls: 1 arg: 6
}
