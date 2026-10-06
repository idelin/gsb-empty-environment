package testsel

import (
	"reflect"
	"testing"
)

func snap() *Snapshot { return NewSnapshot() }

func TestSelectModifiedFunction(t *testing.T) {
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Funcs["m.b"] = "h2"
	old.Coverage["m.TestA"] = []FuncID{"m.a"}
	old.Coverage["m.TestB"] = []FuncID{"m.b"}

	new := snap()
	new.Funcs["m.a"] = "h1-changed"
	new.Funcs["m.b"] = "h2"

	dec := Select(old, new)
	if dec.RunAll {
		t.Fatalf("unexpected RunAll: %v", dec.Reasons)
	}
	want := []TestID{"m.TestA"}
	if !reflect.DeepEqual(dec.Selected, want) {
		t.Fatalf("selected %v, want %v", dec.Selected, want)
	}
}

func TestSelectTransitiveThroughCallGraph(t *testing.T) {
	// TestT covers entry; entry -> mid -> leaf. Changing leaf must select
	// TestT via the reverse closure.
	old := snap()
	old.Funcs["m.leaf"] = "h1"
	old.Funcs["m.mid"] = "h2"
	old.Funcs["m.entry"] = "h3"
	old.Edges = []Edge{{"m.entry", "m.mid"}, {"m.mid", "m.leaf"}}
	old.Coverage["m.TestT"] = []FuncID{"m.entry", "m.mid", "m.leaf"}

	new := snap()
	new.Funcs["m.leaf"] = "h1-changed"
	new.Funcs["m.mid"] = "h2"
	new.Funcs["m.entry"] = "h3"
	new.Edges = old.Edges

	dec := Select(old, new)
	wantAff := []FuncID{"m.entry", "m.leaf", "m.mid"}
	if !reflect.DeepEqual(dec.Affected, wantAff) {
		t.Fatalf("affected %v, want %v", dec.Affected, wantAff)
	}
	if !reflect.DeepEqual(dec.Selected, []TestID{"m.TestT"}) {
		t.Fatalf("selected %v", dec.Selected)
	}
}

func TestSelectPropagatesAlongUnionOfOldAndNewGraph(t *testing.T) {
	// Old graph: entry -> leaf. New graph: edge removed (leaf now called
	// from elsewhere). leaf changes: the test must still be selected
	// because the OLD graph connected it.
	old := snap()
	old.Funcs["m.leaf"] = "h1"
	old.Funcs["m.entry"] = "h2"
	old.Edges = []Edge{{"m.entry", "m.leaf"}}
	old.Coverage["m.TestT"] = []FuncID{"m.entry"}

	new := snap()
	new.Funcs["m.leaf"] = "h1-changed"
	new.Funcs["m.entry"] = "h2-changed" // body changed to drop the call
	new.Edges = nil

	dec := Select(old, new)
	if !reflect.DeepEqual(dec.Selected, []TestID{"m.TestT"}) {
		t.Fatalf("old-graph propagation: selected %v", dec.Selected)
	}

	// Mirror image: edge only exists in the NEW graph. A test whose
	// coverage includes the new caller must be selected when the callee
	// changes, following the new edge.
	old2 := snap()
	old2.Funcs["m.leaf"] = "h1"
	old2.Funcs["m.entry"] = "h2"
	old2.Coverage["m.TestT"] = []FuncID{"m.entry"}

	new2 := snap()
	new2.Funcs["m.leaf"] = "h1-changed"
	new2.Funcs["m.entry"] = "h2-changed"
	new2.Edges = []Edge{{"m.entry", "m.leaf"}}

	dec2 := Select(old2, new2)
	if !reflect.DeepEqual(dec2.Selected, []TestID{"m.TestT"}) {
		t.Fatalf("new-graph propagation: selected %v", dec2.Selected)
	}
}

func TestSelectNewFunctionForcesRunAll(t *testing.T) {
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Coverage["m.TestA"] = []FuncID{"m.a"}

	new := snap()
	new.Funcs["m.a"] = "h1"
	new.Funcs["m.brandNew"] = "h9"

	dec := Select(old, new)
	if !dec.RunAll {
		t.Fatalf("expected RunAll for unknown coverage, got %+v", dec)
	}
}

func TestSelectDeletedFunction(t *testing.T) {
	// helper is deleted; TestDirect covered it, TestIndirect reached it
	// through the call graph, TestOther is unaffected.
	old := snap()
	old.Funcs["m.helper"] = "h1"
	old.Funcs["m.svc"] = "h2"
	old.Funcs["m.other"] = "h3"
	old.Edges = []Edge{{"m.svc", "m.helper"}}
	old.Coverage["m.TestDirect"] = []FuncID{"m.helper"}
	old.Coverage["m.TestIndirect"] = []FuncID{"m.svc"}
	old.Coverage["m.TestOther"] = []FuncID{"m.other"}

	new := snap()
	new.Funcs["m.svc"] = "h2"
	new.Funcs["m.other"] = "h3"

	dec := Select(old, new)
	want := []TestID{"m.TestDirect", "m.TestIndirect"}
	if !reflect.DeepEqual(dec.Selected, want) {
		t.Fatalf("selected %v, want %v", dec.Selected, want)
	}
}

func TestSelectDynamicDispatchChange(t *testing.T) {
	// svc.Close is invoked dynamically (interface). The (Impl).Close
	// implementation changes body: no static edge connects svc to it, but
	// the dynamic model must pull svc's test in.
	old := snap()
	old.Funcs["m.svc"] = "h1"
	old.Funcs["m.(Impl).Close"] = "h2"
	old.DynCalls["m.svc"] = []string{"Close"}
	old.DynImpls["Close"] = []FuncID{"m.(Impl).Close"}
	old.Coverage["m.TestSvc"] = []FuncID{"m.svc"}

	new := snap()
	new.Funcs["m.svc"] = "h1"
	new.Funcs["m.(Impl).Close"] = "h2-changed"
	new.DynCalls["m.svc"] = []string{"Close"}
	new.DynImpls["Close"] = []FuncID{"m.(Impl).Close"}

	dec := Select(old, new)
	if !reflect.DeepEqual(dec.Selected, []TestID{"m.TestSvc"}) {
		t.Fatalf("selected %v", dec.Selected)
	}
}

func TestSelectStaleCoverage(t *testing.T) {
	// Coverage references a function that neither snapshot knows: the
	// recording is out of date and the test must rerun.
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Coverage["m.TestA"] = []FuncID{"m.a", "m.ghost"}

	new := snap()
	new.Funcs["m.a"] = "h1"

	dec := Select(old, new)
	if !reflect.DeepEqual(dec.Selected, []TestID{"m.TestA"}) {
		t.Fatalf("selected %v", dec.Selected)
	}
}

func TestSelectNoChanges(t *testing.T) {
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Coverage["m.TestA"] = []FuncID{"m.a"}

	new := snap()
	new.Funcs["m.a"] = "h1"

	dec := Select(old, new)
	if dec.RunAll || len(dec.Selected) != 0 || len(dec.Affected) != 0 {
		t.Fatalf("expected no-op, got %+v", dec)
	}
}
