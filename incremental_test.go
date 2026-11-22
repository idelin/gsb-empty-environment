package impact

import "testing"

func TestIncrementalEntryGranularity(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	e1 := prov("m", "p", "e1.o", "E1")
	e2 := prov("m", "p", "e2.o", "E2")
	t1 := prov("m", "p", "t1.o", "T1")
	t2 := prov("m", "p", "t2.o", "T2")
	snap := mustSnapshot(t, m, m.True(),
		[]SummaryKey{e1.Key(), e2.Key()},
		[]SignedSummary{
			sign(t, m, Summary{Provenance: e1, Body: []Event{call("e1-t", "t1", x)}}),
			sign(t, m, Summary{Provenance: e2, Body: []Event{call("e2-t", "t2", x)}}),
			sign(t, m, Summary{Provenance: t1}),
			sign(t, m, Summary{Provenance: t2}),
		},
		map[string][]Binding{
			"t1": {bindTo(t1)},
			"t2": {bindTo(t2)},
		})
	eng, err := NewEngineWithRevocations(snap, t1.ObjectID(), t2.ObjectID())
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []Provenance{e1, e2} {
		r, _ := first.Result(entry.Key())
		assertBDD(t, r.Affected, "x")
	}

	// Replacing E1's summary affects only E1.
	replacement := sign(t, m, Summary{Provenance: e1, Body: []Event{call("e1-t", "t1", x.Not())}})
	if _, err := eng.ReplaceSummary(replacement); err != nil {
		t.Fatal(err)
	}
	second, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if got := eng.Stats(); got.EntriesRecomputed != 1 || got.EntriesReused != 1 {
		t.Fatalf("after replace, stats=%+v, want one recompute and one reuse", got)
	}
	r1, _ := second.Result(e1.Key())
	r2, _ := second.Result(e2.Key())
	assertBDD(t, r1.Affected, "!x")
	assertBDD(t, r2.Affected, "x")

	// Revoking an unrelated object does not recompute anything.
	eng.RevokeObject(prov("m", "p", "unrelated.o", "U").ObjectID())
	if _, err := eng.Analyze(); err != nil {
		t.Fatal(err)
	}
	if got := eng.Stats(); got.EntriesRecomputed != 0 || got.EntriesReused != 2 {
		t.Fatalf("after unrelated revoke, stats=%+v", got)
	}

	// Restoring T2 affects E2 only.
	eng.RestoreObject(t2.ObjectID())
	if _, err := eng.Analyze(); err != nil {
		t.Fatal(err)
	}
	if got := eng.Stats(); got.EntriesRecomputed != 1 || got.EntriesReused != 1 {
		t.Fatalf("after restore, stats=%+v", got)
	}
	r2, _ = second.Result(e2.Key())
	_ = r2
}

func TestAddingPreviouslyMissingSummaryInvalidates(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	root := prov("m", "p", "root.o", "Root")
	target := prov("m", "p", "target.o", "Target")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{root.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: root, Body: []Event{call("r-t", "target", x)}}),
	}, map[string][]Binding{"target": {bindTo(target)}})
	eng := MustNewEngine(snap)
	first, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := first.Result(root.Key())
	assertBDD(t, r.Unknown, "x")
	eng.RevokeObject(target.ObjectID())
	if _, err := eng.ReplaceSummary(sign(t, m, Summary{Provenance: target})); err != nil {
		t.Fatal(err)
	}
	second, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ = second.Result(root.Key())
	assertBDD(t, r.Affected, "x")
	assertBDD(t, r.Unknown, "0")
}

func TestBindingChangeInvalidatesAllUsers(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	e1 := prov("m", "p", "e1.o", "E1")
	e2 := prov("m", "p", "e2.o", "E2")
	safe := prov("m", "p", "safe.o", "F")
	bad := prov("m", "p", "bad.o", "F")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{e1.Key(), e2.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: e1, Body: []Event{call("e1-f", "f", x)}}),
		sign(t, m, Summary{Provenance: e2, Body: []Event{call("e2-f", "f", x)}}),
		sign(t, m, Summary{Provenance: safe}),
		sign(t, m, Summary{Provenance: bad}),
	}, map[string][]Binding{"f": {bindTo(safe)}})
	eng, err := NewEngineWithRevocations(snap, bad.ObjectID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Analyze(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SetBinding("f", bindTo(bad)); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Analyze(); err != nil {
		t.Fatal(err)
	}
	if got := eng.Stats(); got.EntriesRecomputed != 2 {
		t.Fatalf("binding change should invalidate both users, stats=%+v", got)
	}
}

func TestSetValidInvalidatesAll(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	root := prov("m", "p", "root.o", "Root")
	dep := prov("m", "p", "dep.o", "Dep")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{root.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: root, Body: []Event{call("r-d", "dep", x)}}),
		sign(t, m, Summary{Provenance: dep}),
	}, map[string][]Binding{"dep": {bindTo(dep)}})
	eng := MustNewEngine(snap, dep.ObjectID())
	first, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := first.Result(root.Key())
	assertBDD(t, r.Affected, "x")
	if err := eng.SetValid(x.Not()); err != nil {
		t.Fatal(err)
	}
	second, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if got := eng.Stats(); got.EntriesRecomputed != 1 || got.EntriesReused != 0 {
		t.Fatalf("valid change should invalidate all entries, stats=%+v", got)
	}
	r, _ = second.Result(root.Key())
	assertBDD(t, r.Affected, "0")
}
