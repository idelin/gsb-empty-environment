package impact

import "testing"

func TestOriginalCertificateSurvivesEngineUpdate(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	oldA := sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}})
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		oldA,
		sign(t, m, Summary{Provenance: b}),
	}, map[string][]Binding{"b": {bindTo(b)}})
	snap.SetKeyRing(testRing)
	oldDigest := oldA.Certificate.Digest

	eng, err := NewEngine(snap)
	if err != nil {
		t.Fatal(err)
	}
	eng.RevokeObject(b.ObjectID())
	if _, err := eng.Analyze(); err != nil {
		t.Fatal(err)
	}
	newA := sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x.Not())}})
	if _, err := eng.ReplaceSummary(newA); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Analyze(); err != nil {
		t.Fatal(err)
	}

	// The original snapshot and its certificate were never mutated.
	if err := snap.VerifyCertificates(testRing); err != nil {
		t.Fatalf("original snapshot certificate no longer verifies: %v", err)
	}
	got, ok := snap.Summary(a.Key())
	if !ok {
		t.Fatal("original summary disappeared")
	}
	if got.Certificate == nil || got.Certificate.Digest != oldDigest {
		t.Fatal("original certificate was mutated")
	}
	if !got.Summary.Body[0].Guard.Equal(x) {
		t.Fatal("original summary body was mutated")
	}
}

func TestMissingCertificateMakesReachablePathUnknown(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{call("a1", "b", x)}}),
		{Summary: Summary{Provenance: b}}, // deliberately unsigned
	}, map[string][]Binding{"b": {bindTo(b)}})
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "x")
}

func TestTamperedSignatureIsMissingEvidence(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	body := Summary{Provenance: a, Body: []Event{call("a1", "a", m.True())}}
	signed := sign(t, m, body)
	// Change the summary after signing. Provenance still matches, but the
	// digest does not.
	tampered := signed
	tampered.Summary.Body = append([]Event(nil), body.Body...)
	tampered.Summary.Body[0].Guard = m.True().Not()
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{tampered}, nil)
	res := mustAnalyze(t, snap)
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "0")
	assertBDD(t, r.Unknown, "1")
}

func TestCertificateProvenanceMismatchIsRejected(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	a := prov("m", "p", "a.o", "A")
	b := prov("m", "p", "b.o", "B")
	cert := sign(t, m, Summary{Provenance: a})
	snap := mustSnapshot(t, m, m.True(), nil, nil, nil)
	if err := snap.AddSummary(SignedSummary{
		Summary:     Summary{Provenance: b},
		Certificate: cert.Certificate,
	}); err == nil {
		t.Fatal("expected provenance mismatch to be rejected")
	}
}
