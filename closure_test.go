package closure_test

import (
	"errors"
	"fmt"
	"testing"

	closure "prescreen.local/ps-c690d01eea"
)

func testID(name string, version int, digest string) closure.Identity {
	return closure.Identity{Name: name, Version: version, Source: "source-" + name, Digest: digest}
}

func att(id closure.Identity, generation uint64, condition closure.Condition) closure.Attestation {
	return closure.Attestation{ID: id, PolicyGeneration: generation, Condition: condition}
}

func sixFlagKitchenSink(t *testing.T) closure.Request {
	t.Helper()

	root := testID("root", 1, "digest-root")
	aHi := testID("a", 2, "digest-a-hi")
	aLo := testID("a", 1, "digest-a-lo")
	bGood := testID("b", 1, "digest-b-good")
	bBad := testID("b", 2, "digest-b-bad")
	bConflict := testID("b", 3, "digest-b-conflict")
	cOne := testID("c", 1, "digest-c")
	dOne := testID("d", 1, "digest-d-1")
	dTwo := testID("d", 2, "digest-d-2")

	return closure.Request{
		Root:             root,
		VariableOrder:    []string{"f0", "f1", "f2", "f3", "f4", "f5"},
		PolicyGeneration: 7,
		Clauses: []closure.Clause{
			closure.Either(closure.PosLiteral("f0"), closure.PosLiteral("f1")),
			closure.Either(closure.NegLiteral("f2"), closure.PosLiteral("f3")),
			closure.Either(closure.PosLiteral("f4"), closure.NegLiteral("f5")),
		},
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{
				{Name: "a-hi", When: closure.Condition{"f0": true}, Alternatives: []closure.Identity{aHi}},
				{Name: "a-lo", When: closure.Condition{"f0": false}, Alternatives: []closure.Identity{aLo}},
				{Name: "d", When: closure.Condition{"f1": true}, Alternatives: []closure.Identity{dOne, dTwo}},
			}},
			{ID: aHi, Deps: []closure.Dependency{
				{Name: "b", Alternatives: []closure.Identity{bGood, bBad}},
				{Name: "c", When: closure.Condition{"f2": true}, Alternatives: []closure.Identity{cOne}},
				{Name: "a-cycle", Alternatives: []closure.Identity{aHi, aLo}},
			}},
			{ID: aLo, Deps: []closure.Dependency{
				{Name: "b", Alternatives: []closure.Identity{bGood}},
				{Name: "a-cycle", Alternatives: []closure.Identity{aHi, aLo}},
			}},
			{ID: bGood, Deps: []closure.Dependency{
				{Name: "a-cycle", Alternatives: []closure.Identity{aHi, aLo}},
			}},
			{ID: bBad, Deps: []closure.Dependency{
				{Name: "root-cycle", Alternatives: []closure.Identity{root}},
			}},
			{ID: cOne, Deps: []closure.Dependency{
				{Name: "b-conflict", Alternatives: []closure.Identity{bConflict}},
			}},
			{ID: dOne},
			{ID: dTwo},
			{ID: bConflict},
		},
		Attestations: []closure.Attestation{
			att(root, 7, nil),
			att(aHi, 7, nil),
			att(aLo, 7, nil),
			att(bGood, 7, nil),
			att(cOne, 7, nil),
			att(dOne, 7, nil),
			att(dTwo, 7, nil),
			att(bConflict, 7, nil),
		},
		NodeBudget: -1,
	}
}

func configFromBits(order []string, bits int) closure.Assignment {
	config := closure.Assignment{}
	for i, name := range order {
		config[name] = bits&(1<<i) != 0
	}
	return config
}

func bddRegion(report *closure.Report, config closure.Assignment) closure.RegionName {
	pass := report.Graph.Eval(report.Pass, config)
	reject := report.Graph.Eval(report.Reject, config)
	unbuildable := report.Graph.Eval(report.Unbuildable, config)
	switch {
	case pass && !reject && !unbuildable:
		return closure.RegionPass
	case reject && !pass && !unbuildable:
		return closure.RegionReject
	case unbuildable && !pass && !reject:
		return closure.RegionUnbuildable
	case !pass && !reject && !unbuildable && !report.Graph.Eval(report.Allowed, config):
		return closure.RegionDisallowed
	default:
		return closure.RegionName(fmt.Sprintf("overlap/none(pass=%v reject=%v unbuildable=%v allowed=%v)", pass, reject, unbuildable, report.Graph.Eval(report.Allowed, config)))
	}
}

func TestSixFlagFullEnumerationMatchesBDD(t *testing.T) {
	req := sixFlagKitchenSink(t)
	report, err := closure.Analyze(req)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Status != closure.StatusDecided {
		t.Fatalf("status = %q cause=%v", report.Status, report.Cause)
	}
	if err := report.Graph.Verify(); err != nil {
		t.Fatalf("ROBDD invariant: %v", err)
	}

	const count = 1 << 6
	var counts map[closure.RegionName]int
	counts = map[closure.RegionName]int{}
	for bits := 0; bits < count; bits++ {
		config := configFromBits(req.VariableOrder, bits)
		want, err := closure.ClassifyConfiguration(req, config)
		if err != nil {
			t.Fatalf("ClassifyConfiguration(%v): %v", config, err)
		}
		got := bddRegion(report, config)
		if got != want {
			t.Fatalf("config %v: BDD region %q, enumeration %q", config, got, want)
		}
		counts[got]++
	}
	for _, region := range []closure.RegionName{closure.RegionPass, closure.RegionReject, closure.RegionUnbuildable, closure.RegionDisallowed} {
		if counts[region] == 0 {
			t.Fatalf("region %s was not exercised by kitchen sink", region)
		}
		t.Logf("%s=%d", region, counts[region])
	}
	allowedAssignments := report.Graph.AllAssignments(report.Allowed)
	if want := counts[closure.RegionPass] + counts[closure.RegionReject] + counts[closure.RegionUnbuildable]; len(allowedAssignments) != want {
		t.Fatalf("AllAssignments(Allowed)=%d, want %d", len(allowedAssignments), want)
	}
}

func TestAllRejectionEvidenceVerifies(t *testing.T) {
	req := sixFlagKitchenSink(t)
	report, err := closure.Analyze(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Evidence) == 0 {
		t.Fatal("expected rejection evidence")
	}
	for i, evidence := range report.Evidence {
		if err := closure.VerifyEvidence(req, evidence); err != nil {
			t.Fatalf("evidence %d (%s -> %s): %v", i, evidence.Root, evidence.Unproven, err)
		}
		if !report.Graph.Eval(evidence.Support, evidence.Config) {
			t.Fatalf("evidence %d config does not satisfy its support BDD", i)
		}
		if !report.Graph.Eval(report.Reject, evidence.Config) {
			t.Fatalf("evidence %d config is not in reject region", i)
		}
	}
}

func TestMutuallyExclusiveBranchesAreNotConflict(t *testing.T) {
	root := testID("root", 1, "r")
	yes := testID("a", 2, "a-yes")
	no := testID("a", 1, "a-no")
	req := closure.Request{
		Root:          root,
		VariableOrder: []string{"f"},
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{
				{Name: "a-when-true", When: closure.Condition{"f": true}, Alternatives: []closure.Identity{yes}},
				{Name: "a-when-false", When: closure.Condition{"f": false}, Alternatives: []closure.Identity{no}},
			}},
			{ID: yes},
			{ID: no},
		},
		Attestations: []closure.Attestation{att(root, 0, nil), att(yes, 0, nil), att(no, 0, nil)},
		NodeBudget:   -1,
	}
	report, err := closure.Analyze(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []bool{false, true} {
		config := closure.Assignment{"f": f}
		if got := bddRegion(report, config); got != closure.RegionPass {
			t.Fatalf("f=%v got %s, want pass", f, got)
		}
		want, err := closure.ClassifyConfiguration(req, config)
		if err != nil {
			t.Fatal(err)
		}
		if want != closure.RegionPass {
			t.Fatalf("oracle f=%v = %s", f, want)
		}
	}
}

func TestNegatedFlagTransitiveUnprovenStaysInCorrectBranch(t *testing.T) {
	root := testID("root", 1, "r")
	mid := testID("mid", 1, "mid")
	bad := testID("transitive", 1, "bad")
	other := testID("other", 1, "other")
	req := closure.Request{
		Root:          root,
		VariableOrder: []string{"f"},
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{
				{Name: "mid", When: closure.Condition{"f": true}, Alternatives: []closure.Identity{mid}},
				{Name: "other", When: closure.Condition{"f": false}, Alternatives: []closure.Identity{other}},
			}},
			{ID: mid, Deps: []closure.Dependency{
				{Name: "transitive", Alternatives: []closure.Identity{bad}},
			}},
			{ID: bad},
			{ID: other},
		},
		Attestations: []closure.Attestation{
			att(root, 0, nil),
			att(mid, 0, nil),
			att(other, 0, nil),
			// bad intentionally absent.
		},
		NodeBudget: -1,
	}
	report, err := closure.Analyze(req)
	if err != nil {
		t.Fatal(err)
	}

	falseConfig := closure.Assignment{"f": false}
	trueConfig := closure.Assignment{"f": true}
	if got := bddRegion(report, falseConfig); got != closure.RegionPass {
		t.Fatalf("f=false got %s, want pass; transitive bad must not fold into negated branch", got)
	}
	if got := bddRegion(report, trueConfig); got != closure.RegionReject {
		t.Fatalf("f=true got %s, want reject", got)
	}
	wantFalse, _ := closure.ClassifyConfiguration(req, falseConfig)
	wantTrue, _ := closure.ClassifyConfiguration(req, trueConfig)
	if wantFalse != closure.RegionPass || wantTrue != closure.RegionReject {
		t.Fatalf("oracle = %s/%s", wantFalse, wantTrue)
	}

	var found bool
	for _, evidence := range report.Evidence {
		if evidence.Unproven == bad {
			found = true
			if err := closure.VerifyEvidence(req, evidence); err != nil {
				t.Fatalf("bad evidence: %v", err)
			}
			if len(evidence.IdentityChain) != 3 || evidence.IdentityChain[1] != mid || evidence.IdentityChain[2] != bad {
				t.Fatalf("chain = %v, want root->mid->bad", evidence.IdentityChain)
			}
		}
	}
	if !found {
		t.Fatal("no evidence for transitive unproven package")
	}
}

func TestSameNameDifferentSourceDoesNotShareProof(t *testing.T) {
	root := testID("root", 1, "r")
	trusted := closure.Identity{Name: "lib", Version: 1, Source: "trusted", Digest: "same-or-different-digest"}
	evil := closure.Identity{Name: "lib", Version: 1, Source: "evil", Digest: "same-or-different-digest"}
	req := closure.Request{
		Root:          root,
		VariableOrder: []string{"f"},
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{{Name: "lib", Alternatives: []closure.Identity{trusted, evil}}}},
			{ID: trusted},
			{ID: evil},
		},
		Attestations: []closure.Attestation{att(root, 0, nil), att(trusted, 0, nil)},
		NodeBudget:   -1,
	}
	report, err := closure.Analyze(req)
	if err != nil {
		t.Fatal(err)
	}
	config := closure.Assignment{"f": false}
	if got := bddRegion(report, config); got != closure.RegionReject {
		t.Fatalf("got %s, want reject because evil source is an alternative resolution", got)
	}
	if len(report.Evidence) == 0 {
		t.Fatal("expected evil-source evidence")
	}
	var evilEvidence *closure.Evidence
	for i := range report.Evidence {
		if report.Evidence[i].Unproven == evil {
			evilEvidence = &report.Evidence[i]
		}
	}
	if evilEvidence == nil {
		t.Fatal("missing evidence for evil source")
	}
	if err := closure.VerifyEvidence(req, *evilEvidence); err != nil {
		t.Fatal(err)
	}
}

func TestNodeBudgetWith24FlagsOnlyTwoRelevant(t *testing.T) {
	flags := make([]string, 24)
	for i := range flags {
		flags[i] = fmt.Sprintf("b%02d", i)
	}
	root := testID("root", 1, "root")
	yes := testID("dep", 2, "dep-yes")
	no := testID("dep", 1, "dep-no")
	base := closure.Request{
		Root:          root,
		VariableOrder: flags,
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{
				{Name: "dep-yes", When: closure.Condition{"b00": true}, Alternatives: []closure.Identity{yes}},
				{Name: "dep-no", When: closure.Condition{"b01": false}, Alternatives: []closure.Identity{no}},
			}},
			{ID: yes},
			{ID: no},
		},
		Attestations:     []closure.Attestation{att(root, 3, nil), att(yes, 3, nil)},
		PolicyGeneration: 3,
	}

	enough := base
	enough.NodeBudget = 32
	report, err := closure.Analyze(enough)
	if err != nil {
		t.Fatalf("enough budget returned Go error: %v", err)
	}
	if report.Status != closure.StatusDecided {
		t.Fatalf("status=%s cause=%v", report.Status, report.Cause)
	}
	if report.NodeCount > 32 {
		t.Fatalf("node count %d exceeds budget", report.NodeCount)
	}
	if report.NodeCount > 16 {
		t.Fatalf("24-order graph should stay tiny when only two flags matter, got %d nodes", report.NodeCount)
	}
	t.Logf("decided 24-flag report uses %d shared nodes", report.NodeCount)

	poor := base
	poor.NodeBudget = 1
	undecided, err := closure.Analyze(poor)
	if err != nil {
		t.Fatalf("budget exhaustion should be an explicit undecided report, not Go error: %v", err)
	}
	if undecided.Status != closure.StatusUndecided || !undecided.Undecided {
		t.Fatalf("status=%q undecided=%v", undecided.Status, undecided.Undecided)
	}
	if !errors.Is(undecided.Cause, closure.ErrBudgetExceeded) {
		t.Fatalf("cause %v is not ErrBudgetExceeded", undecided.Cause)
	}
}

func TestRootDigestOnlyZeroFlagsAndGenerationMismatch(t *testing.T) {
	root := testID("root", 9, "target-root-digest")
	dep := testID("dep", 1, "dep-digest")
	req := closure.Request{
		RootName:         "root",
		TargetDigest:     "target-root-digest",
		PolicyGeneration: 5,
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{{Alternatives: []closure.Identity{dep}}}},
			{ID: dep},
		},
		Attestations: []closure.Attestation{
			att(root, 5, nil),
			att(dep, 4, nil), // old policy generation
		},
		NodeBudget: -1,
	}
	report, err := closure.Analyze(req)
	if err != nil {
		t.Fatal(err)
	}
	empty := closure.Assignment{}
	if got := report.Region(empty); got != closure.RegionReject {
		t.Fatalf("region=%s, want reject", got)
	}
	if want, err := closure.ClassifyConfiguration(req, empty); err != nil || want != closure.RegionReject {
		t.Fatalf("oracle=(%s,%v), want reject", want, err)
	}
	if len(report.Evidence) != 1 {
		t.Fatalf("evidence count=%d, want one dep witness", len(report.Evidence))
	}
	if report.Evidence[0].ReasonCode != "generation_mismatch" {
		t.Fatalf("reason=%s", report.Evidence[0].ReasonCode)
	}
	if err := closure.VerifyEvidence(req, report.Evidence[0]); err != nil {
		t.Fatal(err)
	}

	req.Attestations[1] = att(dep, 5, nil)
	pass, err := closure.Analyze(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := pass.Region(empty); got != closure.RegionPass {
		t.Fatalf("after current-generation attestation region=%s, want pass", got)
	}
}

func TestPinnedRootIsTrustAnchorByDefault(t *testing.T) {
	root := testID("root", 1, "pinned-root")
	lib := testID("lib", 1, "lib")
	request := closure.Request{
		Root:          root,
		VariableOrder: []string{},
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{{Alternatives: []closure.Identity{lib}}}},
			{ID: lib},
		},
		Attestations: []closure.Attestation{att(lib, 0, nil)},
		NodeBudget:   -1,
	}

	report, err := closure.Analyze(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Region(closure.Assignment{}); got != closure.RegionPass {
		t.Fatalf("default root trust: region=%s, want pass", got)
	}

	request.RequireRootProof = true
	strict, err := closure.Analyze(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := strict.Region(closure.Assignment{}); got != closure.RegionReject {
		t.Fatalf("strict root proof: region=%s, want reject", got)
	}
	if len(strict.Evidence) == 0 || strict.Evidence[0].Unproven != root {
		t.Fatalf("expected root evidence, got %+v", strict.Evidence)
	}
	if err := closure.VerifyEvidence(request, strict.Evidence[0]); err != nil {
		t.Fatal(err)
	}
}
