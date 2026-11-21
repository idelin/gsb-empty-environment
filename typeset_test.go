package typeset

import (
	"errors"
	"reflect"
	"testing"
)

func TestSingleInheritanceOverride(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "C", Bases: []Base{{Class: "B"}}, Methods: []Method{{Name: "f"}}},
		},
		Allocations: []Allocation{{ID: "a1", Class: "C", Site: "new C"}},
		Calls:       []VirtualCall{{ID: "c1", Receiver: "a1", StaticType: "A", Method: "f"}},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"C::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if !result.Validated || !result.Validation.OK {
		t.Fatalf("validation failed: %+v", result.Validation)
	}
	provenance := result.Calls[0].Provenance
	if len(provenance) != 1 || provenance[0].Allocation != "a1" || provenance[0].AllocationSite != "new C" {
		t.Fatalf("provenance = %+v", provenance)
	}
	if got, want := provenance[0].Path, []string{"C", "B", "A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
}

func TestSharedVirtualSlotAmbiguityAndDerivedOverride(t *testing.T) {
	classes := []Class{
		{Name: "A", Methods: []Method{{Name: "f"}}},
		{Name: "B", Bases: []Base{{Class: "A", Virtual: true}}, Methods: []Method{{Name: "f"}}},
		{Name: "C", Bases: []Base{{Class: "A", Virtual: true}}, Methods: []Method{{Name: "f"}}},
		{Name: "D", Bases: []Base{{Class: "B"}, {Class: "C"}}},
	}
	model := Model{
		Classes:     classes,
		Allocations: []Allocation{{ID: "d1", Class: "D"}},
		Calls: []VirtualCall{
			{ID: "through_b", Receiver: "d1", StaticType: "B", Method: "f"},
			{ID: "through_d", Receiver: "d1", StaticType: "D", Method: "f"},
		},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range result.Calls {
		if got, want := targetStrings(call.Targets), []string{"B::f", "C::f"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s targets = %v, want %v", call.ID, got, want)
		}
		if !call.Ambiguous {
			t.Fatalf("%s should be ambiguous", call.ID)
		}
	}

	_, err = AnalyzeStrict(model)
	if !errors.Is(err, ErrAmbiguousVirtualSlot) {
		t.Fatalf("strict err = %v, want ambiguous virtual slot", err)
	}

	classes[3].Methods = []Method{{Name: "f"}}
	model.Classes = classes
	result, err = AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range result.Calls {
		if got, want := targetStrings(call.Targets), []string{"D::f"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s targets with D override = %v, want %v", call.ID, got, want)
		}
		if call.Ambiguous {
			t.Fatalf("%s should no longer be ambiguous: %+v", call.ID, call.Ambiguities)
		}
	}
}

func TestRepeatedNonVirtualBasesUseSubobjectPaths(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "C", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "D", Bases: []Base{{Class: "B"}, {Class: "C"}}},
		},
		Allocations: []Allocation{{ID: "d1", Class: "D"}},
		Calls: []VirtualCall{
			{ID: "via_b", Receiver: "d1", StaticType: "A", Method: "f", Path: []string{"B", "A"}},
			{ID: "via_c", Receiver: "d1", StaticType: "A", Method: "f", Path: []string{"C", "A"}},
		},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("via B = %v, want %v", got, want)
	}
	if got, want := targetStrings(result.Calls[1].Targets), []string{"C::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("via C = %v, want %v", got, want)
	}
	paths := []string{
		result.Calls[0].Provenance[0].ReceiverPath.String(),
		result.Calls[1].Provenance[0].ReceiverPath.String(),
	}
	if paths[0] == paths[1] {
		t.Fatalf("non-virtual subobject paths were not distinguished: %v", paths)
	}
}

func TestRepeatedDirectBasesHaveDistinctPaths(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "D", Bases: []Base{{Class: "B"}, {Class: "B"}}},
		},
		Allocations: []Allocation{{ID: "d1", Class: "D"}},
		Calls: []VirtualCall{
			{ID: "first", Receiver: "d1", StaticType: "A", Method: "f", Path: []string{"B[0]", "A"}},
			{ID: "second", Receiver: "d1", StaticType: "A", Method: "f", Path: []string{"B[1]", "A"}},
		},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	first := result.Calls[0].Provenance[0].ReceiverPath.String()
	second := result.Calls[1].Provenance[0].ReceiverPath.String()
	if first == second {
		t.Fatalf("direct repeated base paths not distinguished: %q", first)
	}
}

func TestCallOnNonVirtualDiamondIsAmbiguous(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "C", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "D", Bases: []Base{{Class: "B"}, {Class: "C"}}},
		},
		Allocations: []Allocation{{ID: "d1", Class: "D"}},
		Calls:       []VirtualCall{{ID: "diamond", Receiver: "d1", StaticType: "D", Method: "f"}},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	call := result.Calls[0]
	if got, want := targetStrings(call.Targets), []string{"B::f", "C::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if !call.Ambiguous {
		t.Fatalf("call should be ambiguous: %+v", call)
	}
}

func TestTypeReceiverOnNonVirtualDiamondReportsConversionAmbiguity(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "C", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "D", Bases: []Base{{Class: "B"}, {Class: "C"}}},
		},
		Allocations: []Allocation{{ID: "d1", Class: "D"}},
		Calls:       []VirtualCall{{ID: "typed", Receiver: "A", StaticType: "A", Method: "f"}},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	call := result.Calls[0]
	if got, want := targetStrings(call.Targets), []string{"B::f", "C::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if !call.Ambiguous {
		t.Fatalf("typed receiver should report conversion ambiguity: %+v", call)
	}
}

func TestVirtualBaseSubtreeIsShared(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "X", Methods: []Method{{Name: "f"}}},
			{Name: "A", Bases: []Base{{Class: "X"}}},
			{Name: "B", Bases: []Base{{Class: "A", Virtual: true}}, Methods: []Method{{Name: "f"}}},
			{Name: "C", Bases: []Base{{Class: "A", Virtual: true}}},
			{Name: "D", Bases: []Base{{Class: "B"}, {Class: "C"}}},
		},
		Allocations: []Allocation{{ID: "d1", Class: "D"}},
		Calls: []VirtualCall{{
			ID: "nested", Receiver: "d1", StaticType: "X", Method: "f",
			Path: []string{"C", "A", "X"},
		}},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	call := result.Calls[0]
	if got, want := targetStrings(call.Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if call.Ambiguous {
		t.Fatalf("shared subtree should resolve to B::f: %+v", call.Ambiguities)
	}
	if got, want := call.Provenance[0].Path, []string{"D", "C", "A", "X"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("provenance path = %v, want %v", got, want)
	}
}

func TestNonVirtualMethodIsNotOverridden(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f", NonVirtual: true}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f", Virtual: true}}},
		},
		Allocations: []Allocation{{ID: "b1", Class: "B"}},
		Calls: []VirtualCall{
			{ID: "through_a", Receiver: "b1", StaticType: "A", Method: "f"},
			{ID: "through_b", Receiver: "b1", StaticType: "B", Method: "f"},
		},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"A::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("through A = %v, want %v", got, want)
	}
	if !result.Calls[0].NonVirtual {
		t.Fatalf("A::f should be statically bound")
	}
	if got, want := targetStrings(result.Calls[1].Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("through B = %v, want %v", got, want)
	}
}

func TestUpcastAndFieldAssignmentFlow(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "Holder", Fields: []Field{{Name: "slot"}}},
		},
		Allocations: []Allocation{
			{ID: "holder", Class: "Holder", Site: "new Holder"},
			{ID: "obj", Class: "B", Site: "new B"},
		},
		FieldAssignments: []FieldAssignment{
			{ID: "store", Object: "holder", Field: "slot", Value: "obj"},
		},
		Calls: []VirtualCall{
			{ID: "field_call", Receiver: "holder.slot", StaticType: "A", Method: "f"},
		},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	call := result.Calls[0]
	if got, want := targetStrings(call.Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if len(call.Provenance) != 1 || call.Provenance[0].Allocation != "obj" {
		t.Fatalf("provenance = %+v", call.Provenance)
	}
	if call.Provenance[0].AllocationSite != "new B" {
		t.Fatalf("allocation site = %q", call.Provenance[0].AllocationSite)
	}
	foundField := false
	for _, step := range call.Provenance[0].Trace {
		if step.Kind == "field_assignment" && step.Field == "slot" {
			foundField = true
		}
	}
	if !foundField {
		t.Fatalf("field assignment missing from trace: %+v", call.Provenance[0].Trace)
	}
}

func TestFieldsOnRepeatedBasesStayDistinct(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}, Fields: []Field{{Name: "slot"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "C", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
			{Name: "D", Bases: []Base{{Class: "B"}, {Class: "C"}}},
		},
		Allocations: []Allocation{
			{ID: "d", Class: "D"},
			{ID: "bobj", Class: "B"},
			{ID: "cobj", Class: "C"},
		},
		Upcasts: []Upcast{
			{ID: "bview", From: "d", To: "B", Path: []string{"B"}},
			{ID: "cview", From: "d", To: "C", Path: []string{"C"}},
		},
		FieldAssignments: []FieldAssignment{
			{ID: "sb", Object: "bview", Field: "slot", Value: "bobj"},
			{ID: "sc", Object: "cview", Field: "slot", Value: "cobj"},
		},
		Calls: []VirtualCall{
			{ID: "cb", Receiver: "bview.slot", StaticType: "A", Method: "f"},
			{ID: "cc", Receiver: "cview.slot", StaticType: "A", Method: "f"},
		},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B field targets = %v, want %v", got, want)
	}
	if got, want := targetStrings(result.Calls[1].Targets), []string{"C::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("C field targets = %v, want %v", got, want)
	}
}

func TestExplicitUpcastValue(t *testing.T) {
	model := Model{
		Classes: []Class{
			{Name: "A", Methods: []Method{{Name: "f"}}},
			{Name: "B", Bases: []Base{{Class: "A"}}, Methods: []Method{{Name: "f"}}},
		},
		Allocations: []Allocation{{ID: "b1", Class: "B", Site: "new B"}},
		Upcasts:     []Upcast{{ID: "to_a", From: "b1", To: "A", Path: []string{"A"}}},
		Calls:       []VirtualCall{{ID: "c", Receiver: "to_a", Method: "f"}},
	}
	result, err := AnalyzeModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestInheritanceCycleRejected(t *testing.T) {
	_, err := AnalyzeModel(Model{Classes: []Class{
		{Name: "A", Bases: []Base{{Class: "B"}}},
		{Name: "B", Bases: []Base{{Class: "A"}}},
	}})
	if !errors.Is(err, ErrInheritanceCycle) {
		t.Fatalf("err = %v, want inheritance cycle", err)
	}
}

func TestVariadicAnalyze(t *testing.T) {
	classes := []Class{{Name: "A", Methods: []Method{{Name: "f"}}}}
	allocations := []Allocation{{ID: "a", Class: "A"}}
	calls := []VirtualCall{{ID: "c", Receiver: "a", Method: "f"}}
	result, err := Analyze(classes, allocations, []Upcast{}, []FieldAssignment{}, calls)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"A::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestAnalyzeJSON(t *testing.T) {
	data := `{
		"classes": [
			{"name":"A","methods":[{"name":"f"}]},
			{"name":"B","bases":[{"class":"A"}],"methods":[{"name":"f"}]}
		],
		"allocations": [{"id":"b","class":"B","site":"new B"}],
		"calls": [{"id":"c","receiver":"b","static_type":"A","method":"f"}]
	}`
	result, err := AnalyzeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := targetStrings(result.Calls[0].Targets), []string{"B::f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func targetStrings(targets []Target) []string {
	out := make([]string, len(targets))
	for i, target := range targets {
		out[i] = target.Key()
	}
	return out
}
