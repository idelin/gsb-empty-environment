package impact

import "testing"

func TestOptionalSymbolAbsenceDoesNotCreateUnknown(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", m.True())}}),
		sign(t, m, Summary{Provenance: b, OptionalSymbols: []OptionalSymbol{{Name: "b", Guard: x}}}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	res := mustAnalyze(t, snap, b.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "x")
	assertBDD(t, r.Unknown, "0")
}

func TestAmbiguousStrongBindingsAreUnknown(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	c := prov("m", "p", "c.o", "C")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "f", m.True())}}),
		sign(t, m, Summary{Provenance: b}),
		sign(t, m, Summary{Provenance: c}),
	}, map[string][]Binding{
		"f": {bindTo(b), bindTo(c)},
	})
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestNoBindingsIsUnknown(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "missing", m.True())}}),
	}, nil)
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestRootObjectRevokedWithoutSummaryIsAffected(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	root := prov("m", "p", "root.o", "Root")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{root.Key()}, nil, nil)
	res := mustAnalyze(t, snap, root.ObjectID())
	r, _ := res.Result(root.Key())
	assertBDD(t, r.Affected, "1")
	assertBDD(t, r.Unknown, "0")
}

func TestRegistrationOfRevokedOriginIsAffected(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	cb := prov("m", "p", "cb.o", "Cb")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{{
			Kind: EventRegister, Site: "r", Target: SymbolRef{Name: "cb"},
			Handle: "h", Origin: &cb, Guard: x,
		}, {
			Kind: EventUnregister, Site: "u", Handle: "h", Guard: x,
		}}}),
		sign(t, m, Summary{Provenance: cb}),
	}, map[string][]Binding{"cb": {bindTo(cb)}})
	// The handle is unregistered under the same guard, but binding it to a
	// revoked object was already a touch.
	res := mustAnalyze(t, snap, cb.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "x")
	assertBDD(t, r.Unknown, "0")
}

func TestInvalidSignatureWithKeyRing(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	signed := sign(t, m, Summary{Provenance: a})
	signed.Certificate.Signature[0] ^= 0xff
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{signed}, nil)
	snap.SetKeyRing(testRing)
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestEmptySignatureIsMissingEvidenceWithoutKeyRing(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	signed := sign(t, m, Summary{Provenance: a})
	signed.Certificate.Signature = nil
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{signed}, nil)
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestEmptyValidSetYieldsEmptyResults(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	root := prov("m", "p", "root.o", "Root")
	snap := mustSnapshot(t, m, m.False(), []SummaryKey{root.Key()}, nil, nil)
	res := mustAnalyze(t, snap, root.ObjectID())
	r, _ := res.Result(root.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "0")
	if !r.IsEmpty() {
		t.Fatal("empty valid set should yield an empty decision")
	}
}

func TestObjectRevocationWildcardsBuildID(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	root := prov("m", "p", "root.o", "Root")
	dep := prov("m", "p", "dep.o", "Dep")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{root.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: root, Body: []Event{call("r-d", "dep", x)}}),
		sign(t, m, Summary{Provenance: dep}),
	}, map[string][]Binding{"dep": {bindTo(dep)}})
	eng := MustNewEngine(snap)
	first, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := first.Result(root.Key())
	assertBDD(t, r.Affected, "0")
	wildcard := dep.ObjectID()
	wildcard.BuildID = ""
	eng.RevokeObject(wildcard)
	second, err := eng.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	r, _ = second.Result(root.Key())
	assertBDD(t, r.Affected, "x")
}

func TestVerifierChecksCallReturnPairing(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	callWithPair := Event{
		Kind: EventCall, Site: "a-b", Target: SymbolRef{Name: "b"},
		ReturnTo: "b-return", Guard: m.True(),
	}
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{callWithPair}}),
		sign(t, m, Summary{Provenance: b, Body: []Event{{Kind: EventReturn, Site: "b-return"}}}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	if err := NewVerifier().VerifySnapshot(snap); err != nil {
		t.Fatalf("valid pairing rejected: %v", err)
	}
	bad := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{{
			Kind: EventCall, Site: "a-b", Target: SymbolRef{Name: "b"},
			ReturnTo: "not-present", Guard: m.True(),
		}}}),
		sign(t, m, Summary{Provenance: b, Body: []Event{{Kind: EventReturn, Site: "b-return"}}}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	if err := NewVerifier().VerifySnapshot(bad); err == nil {
		t.Fatal("unpaired call return was accepted")
	}
}

func TestCallbackReturnMustUseRegistrationSite(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	summary := Summary{Provenance: a, Body: []Event{{
		Kind: EventRegister, Site: "reg", Target: SymbolRef{Name: "b"},
		Handle: "h", Origin: &b, ReturnTo: "other",
	}}}
	_, err := testRing.Sign("test", m, summary)
	if err == nil {
		t.Fatal("callback return to a non-registration site was accepted")
	}
}

func TestMissingEvidenceDoesNotPolluteOtherEntries(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	e1 := prov("m", "p", "e1.o", "E1")
	e2 := prov("m", "p", "e2.o", "E2")
	unsigned := prov("m", "p", "u.o", "U")
	safe := prov("m", "p", "s.o", "S")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{e1.Key(), e2.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: e1, Body: []Event{call("e1-u", "u", x)}}),
		sign(t, m, Summary{Provenance: e2, Body: []Event{call("e2-s", "s", x)}}),
		{Summary: Summary{Provenance: unsigned}},
		sign(t, m, Summary{Provenance: safe}),
	}, map[string][]Binding{
		"u": {bindTo(unsigned)},
		"s": {bindTo(safe)},
	})
	res := mustAnalyze(t, snap)
	r1, _ := res.Result(e1.Key())
	r2, _ := res.Result(e2.Key())
	assertBDD(t, r1.Unknown, "x")
	assertBDD(t, r1.Affected, "0")
	assertBDD(t, r2.Unknown, "0")
	assertBDD(t, r2.Affected, "0")
}

func TestRepeatedRegistrationAtSameSiteUsesDistinctHandles(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	root := prov("m", "p", "root.o", "Root")
	cb := prov("m", "p", "cb.o", "Cb")
	d := prov("m", "p", "d.o", "D")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{root.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: root, Body: []Event{
			{Kind: EventRegister, Site: "same-site", Target: SymbolRef{Name: "cb"}, Handle: "h1", Origin: &cb},
			{Kind: EventRegister, Site: "same-site", Target: SymbolRef{Name: "cb"}, Handle: "h2", Origin: &cb},
			{Kind: EventUnregister, Site: "u", Handle: "h1"},
		}}),
		sign(t, m, Summary{Provenance: cb, Body: []Event{call("cb-d", "d", m.True())}}),
		sign(t, m, Summary{Provenance: d}),
	}, map[string][]Binding{
		"cb": {bindTo(cb)},
		"d":  {bindTo(d)},
	})
	res := mustAnalyze(t, snap, d.ObjectID())
	r, _ := res.Result(root.Key())
	assertBDD(t, r.Affected, "1")
	assertBDD(t, r.Unknown, "0")
}
