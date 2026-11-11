package typeset

import (
	"errors"
	"math/rand"
	"testing"
)

func mustAnalyzer(t *testing.T, classes []Class) *Analyzer {
	t.Helper()
	a, err := NewAnalyzer(classes)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	return a
}

func virtualDiamond() []Class {
	//   A { f virtual }
	//  / \
	// B   C  (both virtual-public A, both override f)
	//  \ /
	//   D
	return []Class{
		{Name: "A", Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "B", DirectBases: []Base{{Name: "A", Virtual: true}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "C", DirectBases: []Base{{Name: "A", Virtual: true}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "D", DirectBases: []Base{{Name: "B"}, {Name: "C"}}},
	}
}

func TestVirtualDiamondIsAmbiguous(t *testing.T) {
	a := mustAnalyzer(t, virtualDiamond())
	table, err := a.OverrideTableFor("D")
	if err != nil {
		t.Fatalf("OverrideTableFor: %v", err)
	}

	sharedA := subKey("V:A")
	outcome := table.Resolve(sharedA, "f")
	if !outcome.HasSlot || !outcome.Ambiguous {
		t.Fatalf("expected ambiguous shared slot, got %+v", outcome)
	}
	got := map[string]bool{}
	for _, decl := range outcome.Targets {
		got[decl.Class] = true
	}
	if !got["B"] || !got["C"] {
		t.Fatalf("expected contenders B and C, got %v", outcome.Targets)
	}

	// An override declared on the most-derived class resolves the ambiguity.
	classes := virtualDiamond()
	classes[3].Methods = []Method{{Name: "f", Virtual: true}}
	a2 := mustAnalyzer(t, classes)
	table2, err := a2.OverrideTableFor("D")
	if err != nil {
		t.Fatalf("OverrideTableFor: %v", err)
	}
	resolved := table2.Resolve(sharedA, "f")
	if resolved.Ambiguous || len(resolved.Targets) != 1 || resolved.Targets[0].Class != "D" {
		t.Fatalf("expected unique D override, got %+v", resolved)
	}
}

func TestNonVirtualRepeatedBasesAreDistinct(t *testing.T) {
	//   A { f virtual }
	//  / \
	// B   C (non-virtual A, distinct overrides)
	//  \ /
	//   D
	classes := []Class{
		{Name: "A", Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "B", DirectBases: []Base{{Name: "A"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "C", DirectBases: []Base{{Name: "A"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "D", DirectBases: []Base{{Name: "B"}, {Name: "C"}}},
	}
	a := mustAnalyzer(t, classes)
	table, err := a.OverrideTableFor("D")
	if err != nil {
		t.Fatalf("OverrideTableFor: %v", err)
	}

	left := table.Resolve(subKey("N:D:B:A"), "f")
	right := table.Resolve(subKey("N:D:C:A"), "f")
	if left.Ambiguous || right.Ambiguous {
		t.Fatalf("non-virtual occurrences must not be ambiguous: %+v %+v", left, right)
	}
	if len(left.Targets) != 1 || left.Targets[0].Class != "B" {
		t.Fatalf("left slot must resolve to B.f, got %+v", left)
	}
	if len(right.Targets) != 1 || right.Targets[0].Class != "C" {
		t.Fatalf("right slot must resolve to C.f, got %+v", right)
	}
}

func TestInheritanceCycleRejected(t *testing.T) {
	classes := []Class{
		{Name: "X", DirectBases: []Base{{Name: "Y"}}},
		{Name: "Y", DirectBases: []Base{{Name: "Z"}}},
		{Name: "Z", DirectBases: []Base{{Name: "X"}}},
	}
	_, err := NewAnalyzer(classes)
	var he *HierarchyError
	if !errors.As(err, &he) {
		t.Fatalf("expected HierarchyError, got %v", err)
	}
}

func TestUnknownBaseRejected(t *testing.T) {
	_, err := NewAnalyzer([]Class{{Name: "X", DirectBases: []Base{{Name: "Ghost"}}}})
	var he *HierarchyError
	if !errors.As(err, &he) {
		t.Fatalf("expected HierarchyError, got %v", err)
	}
}

func simpleHierarchy() []Class {
	// Base.f virtual; Mid.f override; Leaf inherits Mid.
	// Other is an unrelated class with a same-named method.
	return []Class{
		{Name: "Base", Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "Mid", DirectBases: []Base{{Name: "Base"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "Leaf", DirectBases: []Base{{Name: "Mid"}}},
		{Name: "Other", Methods: []Method{{Name: "f", Virtual: true}}},
	}
}

func TestUpcastResolvesDynamicTypeNotName(t *testing.T) {
	a := mustAnalyzer(t, simpleHierarchy())
	facts := []Fact{
		AllocFact{AllocPoint: "a1", Class: "Leaf", Dst: "obj"},
		CastFact{Src: "obj", Dst: "base", Path: []PathStep{
			{From: "Leaf", To: "Mid"},
			{From: "Mid", To: "Base"},
		}},
		CallFact{CallSite: "cs1", Recv: "base", Method: "f"},
		AllocFact{AllocPoint: "a2", Class: "Other", Dst: "other"},
	}
	sol, err := a.Analyze(facts)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(sol.Calls) != 1 {
		t.Fatalf("one call expected, got %d", len(sol.Calls))
	}
	call := sol.Calls[0]
	if len(call.Targets) != 1 {
		t.Fatalf("expected exactly one target, got %+v", call.Targets)
	}
	target := call.Targets[0]
	if target.Decl.Class != "Mid" {
		t.Fatalf("dynamic dispatch must reach Mid.f, got %s.f", target.Decl.Class)
	}
	if target.Origin.AllocPoint != "a1" || target.Origin.MostDerived != "Leaf" {
		t.Fatalf("origin must point to a1/Leaf, got %+v", target.Origin)
	}
	if len(target.Origin.Path) != 2 {
		t.Fatalf("origin path must carry both upcast edges, got %+v", target.Origin.Path)
	}
}

func TestFieldFlowSeparatesObjectsAndCarriesOrigin(t *testing.T) {
	a := mustAnalyzer(t, simpleHierarchy())
	facts := []Fact{
		AllocFact{AllocPoint: "a1", Class: "Leaf", Dst: "holder"},
		AllocFact{AllocPoint: "a2", Class: "Mid", Dst: "payload"},
		FieldStoreFact{Base: "holder", FieldName: "slot", Src: "payload"},
		FieldLoadFact{Base: "holder", FieldName: "slot", Dst: "loaded"},
		CallFact{CallSite: "cs1", Recv: "loaded", Method: "f"},
	}
	sol, err := a.Analyze(facts)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	call := sol.Calls[0]
	if len(call.Targets) != 1 {
		t.Fatalf("expected one target, got %+v", call.Targets)
	}
	target := call.Targets[0]
	if target.Decl.Class != "Mid" {
		t.Fatalf("field-carried object must dispatch to Mid.f, got %s.f", target.Decl.Class)
	}
	if target.Origin.AllocPoint != "a2" || target.Origin.MostDerived != "Mid" {
		t.Fatalf("origin must trace back to a2/Mid, got %+v", target.Origin)
	}
}

func TestEmptyReceiverMarkedUnreachable(t *testing.T) {
	a := mustAnalyzer(t, simpleHierarchy())
	sol, err := a.Analyze([]Fact{
		CallFact{CallSite: "cs1", Recv: "ghost", Method: "f"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !sol.Calls[0].EmptyRecv || len(sol.Calls[0].Targets) != 0 {
		t.Fatalf("expected empty receiver call, got %+v", sol.Calls[0])
	}
}

func TestInvalidCastPathRejected(t *testing.T) {
	a := mustAnalyzer(t, simpleHierarchy())
	_, err := a.Analyze([]Fact{
		AllocFact{AllocPoint: "a1", Class: "Leaf", Dst: "obj"},
		CastFact{Src: "obj", Dst: "bad", Path: []PathStep{{From: "Leaf", To: "Other"}}},
	})
	var pe *ProgramError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProgramError, got %v", err)
	}
}

func TestAnalyzeProgramConvenience(t *testing.T) {
	sol, err := AnalyzeProgram(&Program{
		Classes: simpleHierarchy(),
		Facts: []Fact{
			AllocFact{AllocPoint: "a1", Class: "Leaf", Dst: "obj"},
			CallFact{CallSite: "cs1", Recv: "obj", Method: "f"},
		},
	})
	if err != nil {
		t.Fatalf("AnalyzeProgram: %v", err)
	}
	if len(sol.Calls[0].Targets) != 1 || sol.Calls[0].Targets[0].Decl.Class != "Mid" {
		t.Fatalf("unexpected targets: %+v", sol.Calls[0].Targets)
	}
}

func TestSoundnessRandomPrograms(t *testing.T) {
	a := mustAnalyzer(t, virtualDiamondAmbiguityFree())
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		program := a.GenerateProgram(rng, 30)
		if err := a.CheckSoundness(program); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

func virtualDiamondAmbiguityFree() []Class {
	classes := virtualDiamond()
	// Add a derived class that overrides f itself to remove ambiguity.
	classes = append(classes, Class{
		Name:        "E",
		DirectBases: []Base{{Name: "D"}},
		Methods:     []Method{{Name: "f", Virtual: true}},
	})
	return classes
}

func TestSoundnessHandwrittenProgram(t *testing.T) {
	a := mustAnalyzer(t, simpleHierarchy())
	program := &ConcreteProgram{
		Instructions: []ExecInstr{
			{Op: OpAlloc, AllocPoint: "a1", Class: "Leaf", Dst: "obj"},
			{Op: OpCast, Src: "obj", Dst: "base", Path: []PathStep{
				{From: "Leaf", To: "Mid"}, {From: "Mid", To: "Base"},
			}},
		},
		Calls: [][]CallFact{
			nil,
			{{CallSite: "cs1", Recv: "base", Method: "f"}},
		},
	}
	if err := a.CheckSoundness(program); err != nil {
		t.Fatalf("CheckSoundness: %v", err)
	}
}

func TestNoSlotReportedForNonVirtualMethod(t *testing.T) {
	classes := []Class{
		{Name: "P", Methods: []Method{{Name: "plain", Virtual: false}}},
	}
	a := mustAnalyzer(t, classes)
	sol, err := a.Analyze([]Fact{
		AllocFact{AllocPoint: "a1", Class: "P", Dst: "obj"},
		CallFact{CallSite: "cs1", Recv: "obj", Method: "plain"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	call := sol.Calls[0]
	if len(call.NoSlot) != 1 || call.NoSlot[0].AllocPoint != "a1" {
		t.Fatalf("expected one no-slot origin, got %+v", call)
	}
	if len(call.Targets) != 0 {
		t.Fatalf("non-virtual method must not resolve as target, got %+v", call.Targets)
	}
}

func TestCallResultSurfacesAmbiguityAndDistinctPaths(t *testing.T) {
	a := mustAnalyzer(t, virtualDiamond())
	facts := []Fact{
		AllocFact{AllocPoint: "a1", Class: "D", Dst: "obj"},
		CallFact{CallSite: "amb", Recv: "obj", Method: "f"},
	}
	sol, err := a.Analyze(facts)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	call := sol.Calls[0]
	if len(call.Ambiguities) != 1 {
		t.Fatalf("expected one ambiguity, got %+v", call.Ambiguities)
	}
	amb := call.Ambiguities[0]
	if amb.Origin.AllocPoint != "a1" {
		t.Fatalf("ambiguity must carry allocation origin, got %+v", amb.Origin)
	}
	contenders := map[string]bool{}
	for _, c := range amb.Contenders {
		contenders[c.Class] = true
	}
	if !contenders["B"] || !contenders["C"] {
		t.Fatalf("expected B/C contenders, got %v", amb.Contenders)
	}

	// Non-virtual repetition: two explicit paths yield two distinct targets.
	classes := []Class{
		{Name: "A", Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "B", DirectBases: []Base{{Name: "A"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "C", DirectBases: []Base{{Name: "A"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "D", DirectBases: []Base{{Name: "B"}, {Name: "C"}}},
	}
	a2 := mustAnalyzer(t, classes)
	sol2, err := a2.Analyze([]Fact{
		AllocFact{AllocPoint: "a2", Class: "D", Dst: "obj"},
		CastFact{Src: "obj", Dst: "left", Path: []PathStep{{From: "D", To: "B"}, {From: "B", To: "A"}}},
		CastFact{Src: "obj", Dst: "right", Path: []PathStep{{From: "D", To: "C"}, {From: "C", To: "A"}}},
		CallFact{CallSite: "l", Recv: "left", Method: "f"},
		CallFact{CallSite: "r", Recv: "right", Method: "f"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(sol2.Calls) != 2 || sol2.Calls[0].Targets[0].Decl.Class != "B" ||
		sol2.Calls[1].Targets[0].Decl.Class != "C" {
		t.Fatalf("path-distinct slots must resolve to B and C, got %+v", sol2.Calls)
	}
	if sol2.Calls[0].Targets[0].Origin.Path[0].To != "B" {
		t.Fatalf("origin path must disambiguate the left subobject, got %+v",
			sol2.Calls[0].Targets[0].Origin.Path)
	}
}
