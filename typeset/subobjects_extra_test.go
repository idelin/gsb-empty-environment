package typeset

import "testing"

func TestMixedVirtualNonVirtualKeys(t *testing.T) {
	//      V
	//   v/   \v
	//  M1     M2
	//   \     /
	//    \   / (non-virtual bases of D)
	//      D
	classes := []Class{
		{Name: "V"},
		{Name: "M1", DirectBases: []Base{{Name: "V", Virtual: true}}},
		{Name: "M2", DirectBases: []Base{{Name: "V", Virtual: true}}},
		{Name: "D", DirectBases: []Base{{Name: "M1"}, {Name: "M2"}}},
	}
	a := mustAnalyzer(t, classes)
	subs, err := a.SubobjectTableFor("D")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := subs.Subobjects[subKey("V:V")]; !ok {
		t.Fatalf("virtual base must be a single shared subobject: %+v", subs.Subobjects)
	}
	if _, ok := subs.Subobjects[subKey("N:D:M1")]; !ok {
		t.Fatalf("non-virtual M1 path missing")
	}
	if _, ok := subs.Subobjects[subKey("N:D:M2")]; !ok {
		t.Fatalf("non-virtual M2 path missing")
	}

	// Both virtual traversals canonicalize to the same key.
	left := canonicalKey([]PathStep{
		{From: "D", To: "M1"},
		{From: "M1", To: "V", Virtual: true},
	})
	right := canonicalKey([]PathStep{
		{From: "D", To: "M2"},
		{From: "M2", To: "V", Virtual: true},
	})
	if left != right || left != subKey("V:V") {
		t.Fatalf("virtual paths must share key V:V, got %q and %q", left, right)
	}

	// Casting through either route reaches the same canonical target.
	program := &ConcreteProgram{
		Instructions: []ExecInstr{
			{Op: OpAlloc, AllocPoint: "a1", Class: "D", Dst: "d"},
			{Op: OpCast, Src: "d", Dst: "m1", Path: []PathStep{{From: "D", To: "M1"}}},
			{Op: OpCast, Src: "m1", Dst: "v1", Path: []PathStep{{From: "M1", To: "V", Virtual: true}}},
			{Op: OpCast, Src: "d", Dst: "m2", Path: []PathStep{{From: "D", To: "M2"}}},
			{Op: OpCast, Src: "m2", Dst: "v2", Path: []PathStep{{From: "M2", To: "V", Virtual: true}}},
		},
	}
	sol, err := a.Analyze(program.ToFacts())
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Vars["v1"]) != 1 || len(sol.Vars["v2"]) != 1 {
		t.Fatalf("both virtual casts must resolve: %+v %+v", sol.Vars["v1"], sol.Vars["v2"])
	}
	if sol.Vars["v1"][0].key != sol.Vars["v2"][0].key {
		t.Fatalf("shared virtual base must collapse to one key")
	}
}
