package impact

import (
	"testing"
)

var testRing = func() *KeyRing {
	r := NewKeyRing()
	_ = r.Add("test", []byte("test-secret"))
	return r
}()

func sign(t *testing.T, m *Manager, s Summary) SignedSummary {
	t.Helper()
	cert, err := testRing.Sign("test", m, s)
	if err != nil {
		t.Fatalf("sign %s: %v", s.Provenance, err)
	}
	return SignedSummary{Summary: s, Certificate: cert}
}

func prov(module, pkg, object, symbol string) Provenance {
	return Provenance{Module: module, Version: "v1", Package: pkg, Object: object, Symbol: symbol, BuildID: object + "-build"}
}

func call(site SiteID, target string, guard BDD) Event {
	return Event{Kind: EventCall, Site: site, Target: SymbolRef{Name: target}, Guard: guard}
}

func ret() Event { return Event{Kind: EventReturn} }

func bindTo(p Provenance) Binding {
	return Binding{Target: p.Key(), Provenance: p, Object: p.ObjectID()}
}

func mustSnapshot(t *testing.T, m *Manager, valid BDD, entries []SummaryKey, summaries []SignedSummary, bindings map[string][]Binding) *Snapshot {
	t.Helper()
	s, err := BuildSnapshot(m, valid, entries, summaries, bindings)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	return s
}

func mustAnalyze(t *testing.T, snap *Snapshot, revoked ...ObjectID) *Analysis {
	t.Helper()
	a, err := Analyze(snap, revoked)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if err := NewVerifier().VerifyAnalysis(snap, revoked, a); err != nil {
		t.Fatalf("independent verifier: %v", err)
	}
	return a
}

func assertBDD(t *testing.T, got BDD, wantExpr string) {
	t.Helper()
	m := got.Manager()
	want, err := m.Parse(wantExpr)
	if err != nil {
		t.Fatalf("parse %q: %v", wantExpr, err)
	}
	if !got.Equal(want) {
		t.Fatalf("BDD mismatch:\n got: %s\nwant: %s\n expr: %s",
			got.String(), want.String(), wantExpr)
	}
}

func TestBDDCanonicalAndEnumerate(t *testing.T) {
	m, err := NewManager([]string{"x", "y", "z"})
	if err != nil {
		t.Fatal(err)
	}
	x, y := m.Var("x"), m.Var("y")
	expr := x.And(y)
	if !expr.Equal(m.MustParse("x & y")) {
		t.Fatal("canonical identity failed")
	}
	if expr.SatCount() != 2 {
		t.Fatalf("SatCount = %d, want 2", expr.SatCount())
	}
	all := m.True()
	if got := all.SatCount(); got != 8 {
		t.Fatalf("true SatCount = %d, want 8", got)
	}
	if got := len(all.Enumerate()); got != 8 {
		t.Fatalf("true Enumerate length = %d, want 8", got)
	}
	if got := len(x.Enumerate()); got != 4 {
		t.Fatalf("x Enumerate length = %d, want 4", got)
	}
	if got := m.Var("y").SatCount(); got != 4 {
		t.Fatalf("y SatCount = %d, want 4", got)
	}
	if got := m.Var("x").Or(m.Var("z")).SatCount(); got != 6 {
		t.Fatalf("x|z SatCount = %d, want 6", got)
	}
	exists, err := expr.Exists("x")
	if err != nil {
		t.Fatal(err)
	}
	if !exists.Equal(m.Var("y")) {
		t.Fatalf("Exists(x) = %s, want y", exists.String())
	}
	forall, err := expr.ForAll("x")
	if err != nil {
		t.Fatal(err)
	}
	if !forall.IsFalse() {
		t.Fatalf("ForAll(x) = %s, want 0", forall.String())
	}
	if err := expr.IsCanonical(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerVariableLimit(t *testing.T) {
	vars := make([]string, MaxVariables+1)
	for i := range vars {
		vars[i] = string(rune('a' + i))
	}
	if _, err := NewManager(vars); err == nil {
		t.Fatal("expected more than 20 variables to be rejected")
	}
}

func TestReachabilityTwoEdges(t *testing.T) {
	m, _ := NewManager([]string{"x", "y"})
	x, y := m.Var("x"), m.Var("y")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	c := prov("m", "p", "c.o", "C")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}}),
		sign(t, m, Summary{Provenance: b, Body: []Event{call("b1", "c", y)}}),
		sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"b": {bindTo(b)},
		"c": {bindTo(c)},
	})
	res := mustAnalyze(t, snap, c.ObjectID())
	r, ok := res.Result(a.Key())
	if !ok {
		t.Fatal("missing result")
	}
	assertBDD(t, r.Affected, "x & y")
	assertBDD(t, r.Unknown, "0")
}

func TestEdgeConditionsCannotBothHold(t *testing.T) {
	m, _ := NewManager([]string{"x", "y"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	c := prov("m", "p", "c.o", "C")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}}),
		sign(t, m, Summary{Provenance: b, Body: []Event{call("b1", "c", x.Not())}}),
		sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"b": {bindTo(b)},
		"c": {bindTo(c)},
	})
	res := mustAnalyze(t, snap, c.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "0")
}

func TestUnknownOnlyOnReachablePath(t *testing.T) {
	m, _ := NewManager([]string{"x", "y"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	unreachable := prov("m", "p", "u.o", "U")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}}),
		// B has no summary.
		sign(t, m, Summary{Provenance: unreachable}),
	}, map[string][]Binding{
		"b": {bindTo(b)},
	})
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "x")
}

func TestMissingRootSummary(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	root := prov("m", "p", "root.o", "Root")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{root.Key()}, nil, nil)
	res := mustAnalyze(t, snap)
	r, _ := res.Result(root.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestValidConstraintRestrictsResults(t *testing.T) {
	m, _ := NewManager([]string{"x", "y"})
	x, y := m.Var("x"), m.Var("y")
	a := prov("m", "p", "a.o", "A")
	c := prov("m", "p", "c.o", "C")
	snap := mustSnapshot(t, m, x, []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "c", y)}}),
		sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{"c": {bindTo(c)}})
	res := mustAnalyze(t, snap, c.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "x & y")
	assertBDD(t, r.Unknown, "0")
}

func TestCallbackUnregistrationAndDistinctHandles(t *testing.T) {
	m, _ := NewManager([]string{"x", "y", "z"})
	x, y, z := m.Var("x"), m.Var("y"), m.Var("z")
	a := prov("m", "p", "a.o", "A")
	c := prov("m", "p", "c.o", "Cb")
	d := prov("m", "p", "d.o", "D")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{
			{Kind: EventRegister, Site: "r1", Target: SymbolRef{Name: "cb"}, Handle: "h1", Origin: &c, Guard: x},
			{Kind: EventRegister, Site: "r2", Target: SymbolRef{Name: "cb"}, Handle: "h2", Origin: &c, Guard: z},
			{Kind: EventUnregister, Site: "u1", Handle: "h1", Guard: y},
		}}),
		sign(t, m, Summary{Provenance: c, Body: []Event{call("c1", "d", m.True())}}),
		sign(t, m, Summary{Provenance: d}),
	}, map[string][]Binding{
		"cb": {bindTo(c)},
		"d":  {bindTo(d)},
	})
	res := mustAnalyze(t, snap, d.ObjectID())
	r, _ := res.Result(a.Key())
	// h1 is live only when x and not y; h2 is an independent handle.
	assertBDD(t, r.Affected, "(x & !y) | z")
	assertBDD(t, r.Unknown, "0")
}

func TestCallbackTargetReturnIsPairedWithRegistration(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	cb := prov("m", "p", "cb.o", "Cb")
	d := prov("m", "p", "d.o", "D")
	// The callback returns before the registration summary does.  Its effect
	// must be attributed to A's exit, not to some unrelated caller.
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{
			{Kind: EventRegister, Site: "reg", Target: SymbolRef{Name: "cb"}, Handle: "h", Origin: &cb, Guard: x},
			ret(),
		}}),
		sign(t, m, Summary{Provenance: cb, Body: []Event{call("cb1", "d", m.True()), ret()}}),
		sign(t, m, Summary{Provenance: d}),
	}, map[string][]Binding{
		"cb": {bindTo(cb)},
		"d":  {bindTo(d)},
	})
	res := mustAnalyze(t, snap, d.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "x")
	assertBDD(t, r.Unknown, "0")
}

func TestWeakBindingOnlyChangeDoesNotAffectStrongWinner(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	strong := prov("m", "p", "strong.o", "F")
	weak := prov("m", "p", "weak.o", "F")
	otherWeak := prov("m", "p", "other.o", "F")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "f", m.True())}}),
		sign(t, m, Summary{Provenance: strong}),
		sign(t, m, Summary{Provenance: weak}),
		sign(t, m, Summary{Provenance: otherWeak}),
	}, map[string][]Binding{
		"f": {
			{Target: strong.Key(), Provenance: strong, Object: strong.ObjectID()},
			{Target: weak.Key(), Provenance: weak, Object: weak.ObjectID(), Weak: true},
		},
	})
	eng := MustNewEngine(snap)
	eng.RevokeObject(strong.ObjectID())
	eng.RevokeObject(weak.ObjectID())
	eng.RevokeObject(otherWeak.ObjectID())
	a1, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := a1.Result(a.Key())
	assertBDD(t, r1.Affected, "1")
	// The weak candidate is not consulted while the strong binding is enabled.
	if _, err := eng.SetBindings("f", []Binding{
		{Target: strong.Key(), Provenance: strong, Object: strong.ObjectID()},
		{Target: otherWeak.Key(), Provenance: otherWeak, Object: otherWeak.ObjectID(), Weak: true},
	}); err != nil {
		t.Fatal(err)
	}
	a2, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := a2.Result(a.Key())
	assertBDD(t, r2.Affected, "1")
	if eng.Stats().EntriesRecomputed != 1 {
		t.Fatalf("binding change should recompute the dependent entry, stats=%+v", eng.Stats())
	}
}

func TestWeakBindingResolvesWhenStrongGuardFalse(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	strong := prov("m", "p", "strong.o", "F")
	weak := prov("m", "p", "weak.o", "F")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "f", m.True())}}),
		sign(t, m, Summary{Provenance: strong}),
		sign(t, m, Summary{Provenance: weak}),
	}, map[string][]Binding{
		"f": {
			{Target: strong.Key(), Provenance: strong, Object: strong.ObjectID(), Guard: x},
			{Target: weak.Key(), Provenance: weak, Object: weak.ObjectID(), Weak: true},
		},
	})
	res := mustAnalyze(t, snap, weak.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "!x")
	assertBDD(t, r.Unknown, "0")
}

func TestSameNamedSafeObjectIsNotBorrowed(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	revoked := prov("m", "p", "revoked.o", "Shared")
	safe := prov("m", "p", "safe.o", "Shared")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "shared", m.True())}}),
		sign(t, m, Summary{Provenance: revoked}),
		sign(t, m, Summary{Provenance: safe}),
	}, map[string][]Binding{
		"shared": {{Target: revoked.Key(), Provenance: revoked, Object: revoked.ObjectID()}},
	})
	res := mustAnalyze(t, snap, revoked.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "1")
	assertBDD(t, r.Unknown, "0")
}

func TestMissingBindingDoesNotBorrowSameName(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	missing := prov("m", "p", "missing.o", "F")
	safe := prov("m", "p", "safe.o", "F")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "f", m.True())}}),
		sign(t, m, Summary{Provenance: safe}),
	}, map[string][]Binding{
		"f": {{Target: missing.Key(), Provenance: missing, Object: missing.ObjectID()}},
	})
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestUnboundedMutualRecursionFixedPoint(t *testing.T) {
	m, _ := NewManager([]string{"x", "y", "z"})
	x, y, z := m.Var("x"), m.Var("y"), m.Var("z")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	c := prov("m", "p", "c.o", "C")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{
			{Kind: EventReturn, Site: "a-base", Guard: z},
			call("a-b", "b", x),
		}}),
		sign(t, m, Summary{Provenance: b, Body: []Event{
			call("b-a", "a", m.True()),
			call("b-c", "c", y),
		}}),
		sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"a": {bindTo(a)},
		"b": {bindTo(b)},
		"c": {bindTo(c)},
	})
	res := mustAnalyze(t, snap, c.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "x & y")
	assertBDD(t, r.Unknown, "0")
}

func TestIncrementalCycleLosesLastSupportingPath(t *testing.T) {
	m, _ := NewManager([]string{"x", "y", "z"})
	x, y, z := m.Var("x"), m.Var("y"), m.Var("z")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	c := prov("m", "p", "c.o", "C")
	signedA := sign(t, m, Summary{Provenance: a, Body: []Event{
		{Kind: EventReturn, Site: "a-base", Guard: z},
		call("a-b", "b", x),
	}})
	signedB := sign(t, m, Summary{Provenance: b, Body: []Event{
		call("b-a", "a", m.True()),
		call("b-c", "c", y),
	}})
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		signedA, signedB, sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"a": {bindTo(a)},
		"b": {bindTo(b)},
		"c": {bindTo(c)},
	})
	eng, err := NewEngineWithRevocations(snap, c.ObjectID())
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := first.Result(a.Key())
	assertBDD(t, r.Affected, "x & y")

	// Remove the only call from the recursive cycle to the revoked object.
	replacement := sign(t, m, Summary{Provenance: b, Body: []Event{
		call("b-a", "a", m.True()),
	}})
	if _, err := eng.ReplaceSummary(replacement); err != nil {
		t.Fatal(err)
	}
	second, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ = second.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "0")
	if eng.Stats().EntriesRecomputed != 1 {
		t.Fatalf("cycle change should recompute entry, stats=%+v", eng.Stats())
	}
}

func TestUnknownPropagatesThroughRecursiveCycle(t *testing.T) {
	m, _ := NewManager([]string{"x", "y"})
	x, y := m.Var("x"), m.Var("y")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	missing := prov("m", "p", "m.o", "M")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a-b", "b", x)}}),
		sign(t, m, Summary{Provenance: b, Body: []Event{
			call("b-a", "a", m.True()),
			call("b-m", "m", y),
		}}),
	}, map[string][]Binding{
		"a": {bindTo(a)},
		"b": {bindTo(b)},
		"m": {bindTo(missing)},
	})
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "x & y")
}
