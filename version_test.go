package impact

import "testing"

func TestCompareSnapshots(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	c := prov("m", "p", "c.o", "C")
	oldA := sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}})
	newA := sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "c", x)}})
	oldSnap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		oldA, sign(t, m, Summary{Provenance: b}), sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"b": {bindTo(b)},
		"c": {bindTo(c)},
	})
	newSnap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		newA, sign(t, m, Summary{Provenance: b}), sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"b": {bindTo(b)},
		"c": {bindTo(c)},
	})
	change, err := CompareSnapshots(oldSnap, newSnap)
	if err != nil {
		t.Fatal(err)
	}
	if change.Summaries[a.Key()] != SummaryModified {
		t.Fatalf("summaries = %#v", change.Summaries)
	}
	if len(change.Objects) != 1 || change.Objects[0] != a.ObjectID() {
		t.Fatalf("objects = %#v", change.Objects)
	}
	if len(change.Bindings) != 0 {
		t.Fatalf("unexpected binding changes: %#v", change.Bindings)
	}

	// A weak-only change is reported even when a strong binding currently wins.
	newSnap2 := newSnap.Clone()
	if err := newSnap2.SetBindings("c", []Binding{
		bindTo(c),
		{Target: b.Key(), Provenance: b, Object: b.ObjectID(), Weak: true},
	}); err != nil {
		t.Fatal(err)
	}
	change, err = CompareSnapshots(newSnap, newSnap2)
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Bindings) != 1 || change.Bindings[0] != "c" {
		t.Fatalf("bindings = %#v", change.Bindings)
	}
}

func TestNewEngineFromVersions(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	oldSnap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a}),
		sign(t, m, Summary{Provenance: b}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	newSnap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}}),
		sign(t, m, Summary{Provenance: b}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	eng, change, err := NewEngineFromVersions(oldSnap, newSnap, []ObjectID{b.ObjectID()})
	if err != nil {
		t.Fatal(err)
	}
	if change.Summaries[a.Key()] != SummaryModified {
		t.Fatalf("change = %#v", change)
	}
	analysis, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := analysis.Result(a.Key())
	assertBDD(t, r.Affected, "x")
}

func TestAnalyzeVersions(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	oldSnap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a}),
		sign(t, m, Summary{Provenance: b}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	newSnap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}}),
		sign(t, m, Summary{Provenance: b}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	analysis, change, err := AnalyzeVersions(oldSnap, newSnap, []ObjectID{b.ObjectID()})
	if err != nil {
		t.Fatal(err)
	}
	if change.Summaries[a.Key()] != SummaryModified {
		t.Fatalf("change = %#v", change)
	}
	r, _ := analysis.Result(a.Key())
	assertBDD(t, r.Affected, "x")
}
