package typeset_test

import (
	"fmt"

	"example.org/netsec/q0395/typeset"
)

func Example() {
	classes := []typeset.Class{
		{Name: "Base", Methods: []typeset.Method{{Name: "f", Virtual: true}}},
		{Name: "Mid", DirectBases: []typeset.Base{{Name: "Base"}},
			Methods: []typeset.Method{{Name: "f", Virtual: true}}},
		{Name: "Leaf", DirectBases: []typeset.Base{{Name: "Mid"}}},
		{Name: "Unrelated", Methods: []typeset.Method{{Name: "f", Virtual: true}}},
	}
	analyzer, err := typeset.NewAnalyzer(classes)
	if err != nil {
		panic(err)
	}

	facts := []typeset.Fact{
		typeset.AllocFact{AllocPoint: "alloc-7", Class: "Leaf", Dst: "obj"},
		typeset.CastFact{
			Src: "obj",
			Dst: "basePtr",
			Path: []typeset.PathStep{
				{From: "Leaf", To: "Mid"},
				{From: "Mid", To: "Base"},
			},
		},
		typeset.CallFact{CallSite: "call-1", Recv: "basePtr", Method: "f"},
		// A same-named method on an unrelated class never enters the set.
		typeset.AllocFact{AllocPoint: "alloc-8", Class: "Unrelated", Dst: "other"},
	}

	solution, err := analyzer.Analyze(facts)
	if err != nil {
		panic(err)
	}
	for _, call := range solution.Calls {
		for _, target := range call.Targets {
			fmt.Printf("allow %s.%s from %s via %d edges\n",
				target.Decl.Class, target.Decl.Method,
				target.Origin.AllocPoint, len(target.Origin.Path))
		}
	}
	// Output: allow Mid.f from alloc-7 via 2 edges
}

func ExampleAnalyzer_CheckSoundness() {
	classes := []typeset.Class{
		{Name: "Base", Methods: []typeset.Method{{Name: "f", Virtual: true}}},
		{Name: "Derived", DirectBases: []typeset.Base{{Name: "Base"}},
			Methods: []typeset.Method{{Name: "f", Virtual: true}}},
	}
	analyzer, err := typeset.NewAnalyzer(classes)
	if err != nil {
		panic(err)
	}

	program := &typeset.ConcreteProgram{
		Instructions: []typeset.ExecInstr{
			{Op: typeset.OpAlloc, AllocPoint: "alloc-1", Class: "Derived", Dst: "obj"},
			{Op: typeset.OpCast, Src: "obj", Dst: "basePtr",
				Path: []typeset.PathStep{{From: "Derived", To: "Base"}}},
		},
		Calls: [][]typeset.CallFact{
			nil,
			{{CallSite: "call-1", Recv: "basePtr", Method: "f"}},
		},
	}

	if err := analyzer.CheckSoundness(program); err != nil {
		panic(err)
	}
	fmt.Println("sound")
	// Output: sound
}
