package netsec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func q0825SampleTree() *WalletTree {
	return &WalletTree{Root: &WalletNode{
		Path: "m",
		Children: []*WalletNode{
			{
				Path: "m/0",
				Children: []*WalletNode{
					{
						Path: "m/0/1",
						Children: []*WalletNode{
							{Path: "m/0/1/2"},
							{Path: "m/0/1/3'"},
						},
					},
					{
						Path: "m/0/4'",
						Children: []*WalletNode{
							{Path: "m/0/4'/5"},
						},
					},
				},
			},
			{
				Path: "m/1",
				Children: []*WalletNode{
					{Path: "m/1/6"},
				},
			},
			{
				Path: "m/7'",
				Children: []*WalletNode{
					{Path: "m/7'/8"},
				},
			},
			{Path: "m/9'"},
		},
	}}
}

func q0825SamplePaths() []string {
	return []string{
		"m",
		"m/0",
		"m/0/1",
		"m/0/1/2",
		"m/0/1/3'",
		"m/0/4'",
		"m/0/4'/5",
		"m/1",
		"m/1/6",
		"m/7'",
		"m/7'/8",
		"m/9'",
	}
}

func q0825ExposedSet(report AuditReport) map[string]bool {
	set := make(map[string]bool, len(report.ExposedPaths))
	for _, path := range report.ExposedPaths {
		set[path] = true
	}
	return set
}

func q0825AssertExposed(t *testing.T, report AuditReport, want ...string) {
	t.Helper()
	got := q0825ExposedSet(report)
	if len(got) != len(want) {
		t.Fatalf("exposed set = %v, want %v", report.ExposedPaths, want)
	}
	for _, path := range want {
		if !got[path] {
			t.Fatalf("exposed set = %v, want %v", report.ExposedPaths, want)
		}
	}
}

func q0825FindExposure(t *testing.T, report AuditReport, path string) Exposure {
	t.Helper()
	for _, exposure := range report.Exposed {
		if exposure.Path == path {
			return exposure
		}
	}
	t.Fatalf("path %s is not exposed; got %v", path, report.ExposedPaths)
	return Exposure{}
}

func TestQ0825ExtendedPublicKeyPlusNonHardenedChildPrivateKey(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m"},
			{Kind: LeafPriv, Path: "m/0/1/2"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Recovering m/0/1, then m/0, then m from the non-hardened path makes the
	// root xprv knowable, so every descendant (including hardened children)
	// is exposed.
	q0825AssertExposed(t, report, q0825SamplePaths()...)
	if report.Safe {
		t.Fatal("report unexpectedly marked safe")
	}

	root := q0825FindExposure(t, report, "m")
	if root.PrivateKeyPath != "m" || root.PathSegments.String() != "m" {
		t.Fatalf("bad root exposure fields: %+v", root)
	}
	if len(root.Chain) == 0 || !strings.Contains(root.Reason, "recover xprv m") {
		t.Fatalf("missing recovery chain for root: %+v", root)
	}
	if !strings.Contains(root.Reason, "export xpub m") {
		t.Fatalf("chain does not identify the exported xpub: %s", root.Reason)
	}
}

func TestQ0825HardenedChildPrivateKeyCannotRecoverParent(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m"},
			{Kind: LeafPriv, Path: "m/0/4'/5"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// m -> m/0 is non-hardened, but m/0 -> m/0/4' is hardened. The parent
	// xpub cannot derive the hardened child, and the child private key cannot
	// reverse the hardened edge.
	q0825AssertExposed(t, report, "m/0/4'/5")
	if q0825ExposedSet(report)["m"] || q0825ExposedSet(report)["m/0"] || q0825ExposedSet(report)["m/0/4'"] {
		t.Fatalf("hardened edge leaked upward: %v", report.ExposedPaths)
	}
}

func TestQ0825ExplicitExtendedPublicKeyOnHardenedChild(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m"},
			{Kind: XPub, Path: "m/7'"},
			{Kind: LeafPriv, Path: "m/7'/8"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// m/7' has its own xpub and a non-hardened child private key, so its xprv
	// is recovered. m -> m/7' is hardened, therefore the root stays safe.
	q0825AssertExposed(t, report, "m/7'", "m/7'/8")
	if q0825ExposedSet(report)["m"] {
		t.Fatalf("hardened child recovered the root: %v", report.ExposedPaths)
	}
}

func TestQ0825HardenedChildExtendedPrivateKeyCannotRecoverParent(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m"},
			{Kind: XPrv, Path: "m/0/4'"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}
	q0825AssertExposed(t, report, "m/0/4'", "m/0/4'/5")
	if q0825ExposedSet(report)["m/0"] || q0825ExposedSet(report)["m"] {
		t.Fatalf("hardened xprv leaked upward: %v", report.ExposedPaths)
	}
}

func TestQ0825UpwardPropagationLevelByLevel(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m"},
			{Kind: LeafPriv, Path: "m/0/1/2"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}
	q0825AssertExposed(t, report, q0825SamplePaths()...)

	root := q0825FindExposure(t, report, "m")
	recoveries := 0
	for _, step := range root.Steps {
		if step.Action == ActionRecoverXPrv {
			recoveries++
		}
	}
	if recoveries < 3 {
		t.Fatalf("expected recovery at m/0/1, m/0, and m; steps: %+v", root.Steps)
	}
}

func TestQ0825OnlyExtendedPublicKeysExposeNothing(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m"},
			{Kind: XPub, Path: "m/7'"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Safe || len(report.ExposedPaths) != 0 {
		t.Fatalf("xpub-only audit exposed keys: %v", report.ExposedPaths)
	}
}

func TestQ0825ExtendedPrivateKeyExposesEntireDescendantSubtree(t *testing.T) {
	report, err := Audit(AuditRequest{
		Materials: []Export{
			{Kind: XPrv, Path: "m/0/1"},
		},
		Tree: q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}
	q0825AssertExposed(t, report, "m/0/1", "m/0/1/2", "m/0/1/3'")
	if q0825ExposedSet(report)["m/0"] || q0825ExposedSet(report)["m/1"] {
		t.Fatalf("xprv leaked outside its subtree: %v", report.ExposedPaths)
	}
}

func TestQ0825IncrementalExportRejectedAtomically(t *testing.T) {
	session, err := NewAuditSession(
		[]Export{{Kind: XPub, Path: "m"}},
		q0825SampleTree(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Report().Safe {
		t.Fatalf("initial state should be safe: %v", session.Report().ExposedPaths)
	}

	_, err = session.Apply([]Export{{Kind: LeafPriv, Path: "m/0/1/2"}})
	if !errors.Is(err, ErrPreviouslySafeExposed) {
		t.Fatalf("expected ErrPreviouslySafeExposed, got %v", err)
	}
	var unsafe *UnsafeExportError
	if !errors.As(err, &unsafe) {
		t.Fatalf("expected *UnsafeExportError, got %T", err)
	}
	if len(unsafe.NewPaths) == 0 || unsafe.NewPaths[0] != "m" {
		t.Fatalf("unexpected newly exposed paths: %v", unsafe.NewPaths)
	}

	// The rejected batch must not be partially committed.
	if got := session.Materials(); len(got) != 1 || got[0].Kind != XPub {
		t.Fatalf("rejected batch mutated session: %+v", got)
	}
	if !session.Report().Safe {
		t.Fatalf("rejected batch mutated report: %v", session.Report().ExposedPaths)
	}
}

func TestQ0825IncrementalBatchRejectedAsAWhole(t *testing.T) {
	session, err := NewAuditSession(
		[]Export{{Kind: XPub, Path: "m"}},
		q0825SampleTree(),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Neither item alone exposes a private key: the xpub is a public-only
	// capability, and the private leaf is at an already public path. Together
	// they recover the parent and the entire root subtree.
	_, err = session.Apply([]Export{
		{Kind: XPub, Path: "m/0"},
		{Kind: LeafPriv, Path: "m/0/1/2"},
	})
	if !errors.Is(err, ErrPreviouslySafeExposed) {
		t.Fatalf("expected whole-batch rejection, got %v", err)
	}
	if got := session.Materials(); len(got) != 1 || got[0].Path != "m" {
		t.Fatalf("batch was partially committed: %+v", got)
	}

	// A public-only addition remains allowed after the rejection.
	if _, err := session.Apply([]Export{{Kind: XPub, Path: "m/0"}}); err != nil {
		t.Fatalf("safe xpub addition was rejected: %v", err)
	}
}

func TestQ0825NewExportAtAlreadyExposedPathIsAllowed(t *testing.T) {
	session, err := NewAuditSession(
		[]Export{{Kind: XPrv, Path: "m/0/1"}},
		q0825SampleTree(),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := session.Report().ExposedPaths

	_, err = session.Apply([]Export{{Kind: LeafPriv, Path: "m/0/1/2"}})
	if err != nil {
		t.Fatalf("already exposed path was rejected: %v", err)
	}
	if got := session.Report().ExposedPaths; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("exposed set changed: got %v want %v", got, want)
	}
}

func TestQ0825ApplyExportsFromReport(t *testing.T) {
	base, err := Audit(AuditRequest{
		Materials: []Export{{Kind: XPub, Path: "m"}},
		Tree:      q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplyExports(base, []Export{{Kind: LeafPriv, Path: "m/0/1/2"}})
	if !errors.Is(err, ErrPreviouslySafeExposed) {
		t.Fatalf("expected rejection, got %v", err)
	}
	if len(report.ExposedPaths) != 0 {
		t.Fatalf("rejection did not preserve the baseline: %v", report.ExposedPaths)
	}
}

func TestQ0825AuditRequestCarriesBaseline(t *testing.T) {
	baseline, err := Audit(AuditRequest{
		Materials: []Export{{Kind: XPub, Path: "m"}},
		Tree:      q0825SampleTree(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Audit(AuditRequest{
		Materials: []Export{{Kind: LeafPriv, Path: "m/0/1/2"}},
		Baseline:  &baseline,
	})
	if !errors.Is(err, ErrPreviouslySafeExposed) {
		t.Fatalf("request baseline was not enforced: %v", err)
	}
}

func TestQ0825JSONRoundTrip(t *testing.T) {
	request := AuditRequest{
		Materials: []Export{
			{Kind: XPub, Path: "m", Key: "xpub6..."},
			{Kind: LeafPriv, Path: "m/0/1/2", Key: "L1..."},
		},
		Tree: q0825SampleTree(),
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuditRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	report, err := Audit(decoded)
	if err != nil {
		t.Fatal(err)
	}
	q0825AssertExposed(t, report, q0825SamplePaths()...)

	encodedReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decodedReport AuditReport
	if err := json.Unmarshal(encodedReport, &decodedReport); err != nil {
		t.Fatal(err)
	}
	if decodedReport.Safe || len(decodedReport.Exposed) != len(q0825SamplePaths()) {
		t.Fatalf("report round-trip lost exposure data: %+v", decodedReport.ExposedPaths)
	}
}

func TestQ0825BareMaterialsListReconstructsEnvelope(t *testing.T) {
	// No tree is supplied; the auditor can still see the envelope implied by
	// the two exported paths.
	report, err := Audit([]Export{
		{Kind: XPub, Path: "m"},
		{Kind: LeafPriv, Path: "m/0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	q0825AssertExposed(t, report, "m", "m/0")
}

func TestQ0825BareMaterialSessionExpandsEnvelope(t *testing.T) {
	session, err := NewAuditSession([]Export{{Kind: XPub, Path: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Apply([]Export{{Kind: LeafPriv, Path: "m/0"}})
	if !errors.Is(err, ErrPreviouslySafeExposed) {
		t.Fatalf("expected rejection after envelope expansion, got %v", err)
	}
	if !session.Report().Safe {
		t.Fatalf("session was mutated: %v", session.Report().ExposedPaths)
	}
}

func TestQ0825PathParsingAndOrdering(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "m"},
		{"m", "m"},
		{"m/0", "m/0"},
		{"m/0h/1", "m/0'/1"},
		{"0'/1", "m/0'/1"},
		{"m/2147483648", "m/0'"},
	}
	for _, testCase := range cases {
		path, err := ParsePath(testCase.input)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", testCase.input, err)
		}
		if path.String() != testCase.want {
			t.Fatalf("ParsePath(%q) = %s, want %s", testCase.input, path.String(), testCase.want)
		}
	}
	if ComparePaths(MustParsePath("m"), MustParsePath("m/0")) >= 0 {
		t.Fatal("parent should sort before child")
	}
	if ComparePaths(MustParsePath("m/0"), MustParsePath("m/0'")) >= 0 {
		t.Fatal("non-hardened sibling should sort before hardened sibling")
	}
}
