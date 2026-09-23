package negotiate

import (
	"strings"
	"testing"
)

func prof(name string, feats ...FeatureDecl) *Profile {
	return &Profile{Name: name, Features: feats}
}

func TestRequiredUnsupportedFails(t *testing.T) {
	a := prof("a", FeatureDecl{Name: "tls", Versions: VersionRange{1, 3}, Required: true})
	b := prof("b", FeatureDecl{Name: "compression", Versions: VersionRange{1, 2}})
	res := Negotiate(a, b, -1)
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %v, want infeasible", res.Status)
	}
	joined := strings.Join(res.Reasons, "\n")
	if !strings.Contains(joined, `required by a but not supported by b`) {
		t.Fatalf("reasons missing required-unsupported explanation: %v", res.Reasons)
	}
	if len(res.Core) == 0 {
		t.Fatalf("expected conflict core, got none")
	}
}

func TestEmptyRangeIntersectionFails(t *testing.T) {
	a := prof("a", FeatureDecl{Name: "tls", Versions: VersionRange{1, 2}, Required: true})
	b := prof("b", FeatureDecl{Name: "tls", Versions: VersionRange{3, 4}})
	res := Negotiate(a, b, -1)
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %v, want infeasible", res.Status)
	}
	if !strings.Contains(strings.Join(res.Reasons, "\n"), "do not intersect") {
		t.Fatalf("reasons missing intersection explanation: %v", res.Reasons)
	}
}

func TestHighestVersionAndPreference(t *testing.T) {
	a := prof("a",
		FeatureDecl{Name: "tls", Versions: VersionRange{1, 3}, Required: true},
		FeatureDecl{Name: "compression", Versions: VersionRange{1, 2}, Priority: 5},
	)
	b := prof("b",
		FeatureDecl{Name: "tls", Versions: VersionRange{2, 3}, Required: true},
		FeatureDecl{Name: "compression", Versions: VersionRange{1, 2}, Priority: 1},
	)
	res := Negotiate(a, b, -1)
	if res.Status != StatusOK {
		t.Fatalf("status = %v (%v)", res.Status, res.Reasons)
	}
	if res.Selection["tls"] != 3 || res.Selection["compression"] != 2 {
		t.Fatalf("selection = %v, want tls=3 compression=2", res.Selection)
	}
	if res.Score.Weight != 6 {
		t.Fatalf("weight = %d, want 6", res.Score.Weight)
	}
}

// TestGlobalBeatsLocal proves per-feature greedy intersection is not
// enough: locally the best version of x is 2, but x@2 blocks y (y needs
// x@1), so the global optimum picks x@1 to keep y's preference weight.
func TestGlobalBeatsLocal(t *testing.T) {
	mk := func() []FeatureDecl {
		return []FeatureDecl{
			{Name: "x", Versions: VersionRange{1, 2}, Priority: 10},
			{Name: "y", Versions: VersionRange{1, 1}, Priority: 9,
				Depends: []Requirement{{Feature: "x", Range: VersionRange{1, 1}}}},
		}
	}
	a, b := prof("a", mk()...), prof("b", mk()...)
	res := Negotiate(a, b, -1)
	if res.Status != StatusOK {
		t.Fatalf("status = %v", res.Status)
	}
	if res.Selection["x"] != 1 || res.Selection["y"] != 1 {
		t.Fatalf("selection = %v, want x=1 y=1", res.Selection)
	}
	if res.Score.Weight != 2*(10+9) {
		t.Fatalf("weight = %d, want %d", res.Score.Weight, 2*19)
	}
}

// TestVersionCondition: a dependency active only for some versions forces a
// lower version of the dependent feature.
func TestVersionCondition(t *testing.T) {
	when := VersionRange{3, 3}
	mk := func() []FeatureDecl {
		return []FeatureDecl{
			{Name: "a", Versions: VersionRange{1, 3}, Required: true,
				Depends: []Requirement{{Feature: "b", Range: VersionRange{2, 2}, When: &when}}},
			{Name: "b", Versions: VersionRange{1, 1}, Required: true},
		}
	}
	res := Negotiate(prof("a", mk()...), prof("b", mk()...), -1)
	if res.Status != StatusOK {
		t.Fatalf("status = %v (%v)", res.Status, res.Reasons)
	}
	if res.Selection["a"] != 2 {
		t.Fatalf("selection = %v, want a=2 (a@3 needs b@2, unavailable)", res.Selection)
	}
}

func TestConflictCoreMinimal(t *testing.T) {
	// x and y are both required but conflict; z is unrelated and must not
	// appear in the core.
	mk := func() []FeatureDecl {
		return []FeatureDecl{
			{Name: "x", Versions: VersionRange{1, 1}, Required: true, Conflicts: []string{"y"}},
			{Name: "y", Versions: VersionRange{1, 1}, Required: true},
			{Name: "z", Versions: VersionRange{1, 2}},
		}
	}
	a, b := prof("a", mk()...), prof("b", mk()...)
	res := Negotiate(a, b, -1)
	if res.Status != StatusInfeasible {
		t.Fatalf("status = %v", res.Status)
	}
	if res.CorePending {
		t.Fatalf("core should not be pending with unlimited budget")
	}
	for _, c := range res.Core {
		if strings.Contains(c, `"z"`) {
			t.Fatalf("unrelated feature z leaked into core: %v", res.Core)
		}
	}
	// Verify the core is a 1-minimal unsatisfiable subset: the core alone
	// is infeasible, and removing any single core atom from it is feasible.
	atoms := buildAtoms(a, b)
	inCore := make([]bool, len(atoms))
	for i, at := range atoms {
		for _, c := range res.Core {
			if at.desc == c {
				inCore[i] = true
			}
		}
	}
	removed := make([]bool, len(atoms))
	for i := range atoms {
		removed[i] = !inCore[i]
	}
	a2, b2 := filterProfiles(a, b, atoms, removed)
	if st := feasible(a2, b2, -1); st != StatusInfeasible {
		t.Fatalf("core alone should be infeasible, got %v", st)
	}
	for k := range atoms {
		if !inCore[k] {
			continue
		}
		removed[k] = true
		a3, b3 := filterProfiles(a, b, atoms, removed)
		if st := feasible(a3, b3, -1); st != StatusOK {
			t.Fatalf("core not minimal: removing %q still infeasible", atoms[k].desc)
		}
		removed[k] = false
	}
}

func TestBudgetExceededIsSeparate(t *testing.T) {
	mk := func() []FeatureDecl {
		return []FeatureDecl{
			{Name: "x", Versions: VersionRange{0, 50}},
			{Name: "y", Versions: VersionRange{0, 50}},
			{Name: "z", Versions: VersionRange{0, 50}},
		}
	}
	res := Negotiate(prof("a", mk()...), prof("b", mk()...), 1)
	if res.Status != StatusBudgetExceeded {
		t.Fatalf("status = %v, want budget-exceeded", res.Status)
	}
	if res.Selection != nil {
		t.Fatalf("budget-exceeded must not return a selection")
	}
	// And with enough budget the same problem solves.
	res2 := Negotiate(prof("a", mk()...), prof("b", mk()...), -1)
	if res2.Status != StatusOK {
		t.Fatalf("status = %v", res2.Status)
	}
}

func TestCertificateVerifyAndTamper(t *testing.T) {
	a := prof("a", FeatureDecl{Name: "tls", Versions: VersionRange{1, 2}, Required: true})
	b := prof("b", FeatureDecl{Name: "tls", Versions: VersionRange{1, 2}, Required: true})
	res := Negotiate(a, b, -1)
	cert := res.Certificate(a, b)
	if err := cert.Verify(a, b); err != nil {
		t.Fatalf("verify: %v", err)
	}
	bad := Certificate{Selection: Selection{"tls": 1}, Summary: cert.Summary}
	if err := bad.Verify(a, b); err == nil {
		t.Fatalf("tampered selection must fail verification")
	}
	// A selection that hashes correctly but violates constraints must fail.
	weird := NewCertificate(a, b, Selection{"tls": 2, "ghost": 1})
	if err := weird.Verify(a, b); err == nil {
		t.Fatalf("infeasible selection must fail verification")
	}
}

func TestCertificateRoundTripBinding(t *testing.T) {
	a := prof("a", FeatureDecl{Name: "tls", Versions: VersionRange{1, 2}, Required: true})
	b := prof("b", FeatureDecl{Name: "tls", Versions: VersionRange{1, 2}, Required: true})
	res := Negotiate(a, b, -1)
	cert := res.Certificate(a, b)
	data := cert.Marshal()
	got, err := UnmarshalCertificate(data, cert.Summary)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Selection["tls"] != cert.Selection["tls"] {
		t.Fatalf("round trip changed selection: %v", got.Selection)
	}
	var other [32]byte
	other[0] = 0xff
	if _, err := UnmarshalCertificate(data, other); err != ErrSummaryMismatch {
		t.Fatalf("expected ErrSummaryMismatch, got %v", err)
	}
}

func TestDowngradeBindsAndKeepsRequired(t *testing.T) {
	mk := func(name string) *Profile {
		return prof(name,
			FeatureDecl{Name: "tls", Versions: VersionRange{1, 3}, Required: true},
			FeatureDecl{Name: "compression", Versions: VersionRange{1, 2}, Priority: 4},
		)
	}
	a, b := mk("a"), mk("b")
	s, res, err := Establish(a, b, -1)
	if err != nil {
		t.Fatalf("establish: %v (%v)", err, res.Reasons)
	}
	oldSummary := s.Summary()
	oldCert := s.Cert

	// Stale downgrade request must be rejected.
	if _, _, err := s.Downgrade("compression", 1, [32]byte{1, 2, 3}); err != ErrStaleBinding {
		t.Fatalf("expected ErrStaleBinding, got %v", err)
	}

	// Proper downgrade of an optional feature succeeds and rebinds.
	cert, _, err := s.Downgrade("compression", 1, oldSummary)
	if err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if cert.Summary == oldSummary {
		t.Fatalf("summary must change after downgrade")
	}
	if cert.Selection["compression"] != 1 {
		t.Fatalf("selection = %v, want compression=1", cert.Selection)
	}
	if _, ok := cert.Selection["tls"]; !ok {
		t.Fatalf("downgrade silently dropped required feature tls")
	}

	// Old certificate can no longer be used to decode the new parameters.
	if _, err := UnmarshalCertificate(oldCert.Marshal(), s.Summary()); err != ErrSummaryMismatch {
		t.Fatalf("old certificate must not decode new session, got %v", err)
	}
	if _, err := UnmarshalCertificate(cert.Marshal(), s.Summary()); err != nil {
		t.Fatalf("new certificate must decode: %v", err)
	}

	// A downgrade that would kill a required feature must fail and leave
	// the session untouched.
	if _, _, err := s.Downgrade("tls", 0, s.Summary()); err == nil {
		t.Fatalf("downgrade below required range must fail")
	}
	if s.Summary() != cert.Summary {
		t.Fatalf("failed downgrade must not change session state")
	}
}

func TestRetryAndRenegotiateBinding(t *testing.T) {
	mk := func(name string, max int) *Profile {
		return prof(name, FeatureDecl{Name: "tls", Versions: VersionRange{1, max}, Required: true})
	}
	s, _, err := Establish(mk("a", 2), mk("b", 2), -1)
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	cert, err := s.Retry(s.Summary())
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if cert.Summary != s.Summary() {
		t.Fatalf("retry with same parameters must reproduce the summary")
	}
	if _, err := s.Retry([32]byte{9}); err != ErrStaleBinding {
		t.Fatalf("stale retry must fail, got %v", err)
	}
	old := s.Cert
	if _, _, err := s.Renegotiate(mk("a", 3), mk("b", 3), s.Summary()); err != nil {
		t.Fatalf("renegotiate: %v", err)
	}
	if s.Summary() == old.Summary {
		t.Fatalf("new parameters must yield a new summary")
	}
	if _, err := UnmarshalCertificate(old.Marshal(), s.Summary()); err != ErrSummaryMismatch {
		t.Fatalf("old result must not decode new parameters, got %v", err)
	}
}

func TestProfileCodecRoundTrip(t *testing.T) {
	when := VersionRange{2, 3}
	p := prof("a",
		FeatureDecl{Name: "x", Versions: VersionRange{1, 4}, Required: true, Priority: -2,
			Depends:   []Requirement{{Feature: "y", Range: VersionRange{1, 2}, When: &when}},
			Conflicts: []string{"z"}},
		FeatureDecl{Name: "y", Versions: VersionRange{0, 2}},
	)
	q, err := UnmarshalProfile(MarshalProfile(p))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	other := prof("b", FeatureDecl{Name: "y", Versions: VersionRange{0, 2}})
	if SummaryOf(p, other, nil) != SummaryOf(q, other, nil) {
		t.Fatalf("profile round trip not canonical")
	}
}
