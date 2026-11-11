package typeset

import "testing"

func TestOuterOverrideHealsSharedVirtualSlot(t *testing.T) {
	// D overrides f itself, healing the B/C virtual-diamond ambiguity.
	classes := virtualDiamond()
	classes[3].Methods = []Method{{Name: "f", Virtual: true}}
	a := mustAnalyzer(t, classes)
	table, err := a.OverrideTableFor("D")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []subKey{rootKey("D"), subKey("V:A"), subKey("N:D:B"), subKey("N:D:C")} {
		outcome := table.Resolve(key, "f")
		if outcome.Ambiguous || len(outcome.Targets) != 1 || outcome.Targets[0].Class != "D" {
			t.Fatalf("key %s must resolve uniquely to D.f, got %+v", key, outcome)
		}
	}
}

func TestDeeperNonVirtualOverrideWins(t *testing.T) {
	// A.f <- B.f <- D.f; the D root and the A subobject both see D.f.
	classes := []Class{
		{Name: "A", Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "B", DirectBases: []Base{{Name: "A"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
		{Name: "D", DirectBases: []Base{{Name: "B"}},
			Methods: []Method{{Name: "f", Virtual: true}}},
	}
	a := mustAnalyzer(t, classes)
	table, err := a.OverrideTableFor("D")
	if err != nil {
		t.Fatal(err)
	}
	outcome := table.Resolve(subKey("N:D:B:A"), "f")
	if outcome.Ambiguous || outcome.Targets[0].Class != "D" {
		t.Fatalf("deepest override must win, got %+v", outcome)
	}
}

func TestSameNameUnrelatedClassNotIncluded(t *testing.T) {
	a := mustAnalyzer(t, simpleHierarchy())
	table, err := a.OverrideTableFor("Leaf")
	if err != nil {
		t.Fatal(err)
	}
	outcome := table.Resolve(rootKey("Leaf"), "f")
	for _, decl := range outcome.Targets {
		if decl.Class == "Other" {
			t.Fatalf("unrelated same-named class leaked into target set: %+v", outcome.Targets)
		}
	}
}
