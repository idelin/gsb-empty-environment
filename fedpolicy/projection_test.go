package fedpolicy

import (
	"testing"
	"time"
)

func TestProjectionDecisions(t *testing.T) {
	mapper, policy, labeler, public, candidates := buildFixture()
	report := Project(candidates, mapper, policy, labeler, public)

	wantSafe := map[int]bool{1: true, 5: true, 6: true}
	wantReason := map[int]RejectReason{
		0: RejectOldDeny,
		2: RejectOldDeny,
		3: RejectOldDeny,
		4: RejectNonPublicLabel,
		7: RejectOldDeny,
		8: RejectNoPreimage,
		9: RejectNoPreimage,
	}

	if len(report.Decisions) != len(candidates) {
		t.Fatalf("got %d decisions, want %d", len(report.Decisions), len(candidates))
	}
	for i, decision := range report.Decisions {
		if decision.Safe != wantSafe[i] {
			t.Errorf("candidate %d safe=%v, want %v (%s)", i, decision.Safe, wantSafe[i], decision.Reason)
		}
		if reason, ok := wantReason[i]; ok && decision.Reason != reason {
			t.Errorf("candidate %d reason=%q, want %q", i, decision.Reason, reason)
		}
		if !decision.Safe && decision.Reason != RejectNonPublicLabel && len(decision.Witnesses) == 0 {
			t.Errorf("candidate %d rejected without a witness", i)
		}
	}

	// Every old-deny witness must be a real denied principal whose trace
	// reconstructs the claimed target path.
	for i, decision := range report.Decisions {
		if decision.Reason != RejectOldDeny {
			continue
		}
		w := decision.Witnesses[0]
		if _, effect := policy.MatchedRule(w.Old); effect != Deny && len(w.MissingLabels) == 0 {
			t.Fatalf("candidate %d witness is not actually denied", i)
		}
		if len(w.Trace) != len(w.Old.Path) {
			t.Fatalf("candidate %d trace length %d != path length %d",
				i, len(w.Trace), len(w.Old.Path))
		}
		if !PathEquals(traceOutput(w.Trace), w.Target.Path) {
			t.Fatalf("candidate %d trace does not reconstruct target", i)
		}
	}
}

func TestEmptyPathIsModeled(t *testing.T) {
	mapper, policy, labeler, public, candidates := buildFixture()
	report := Project(candidates, mapper, policy, labeler, public)

	emptyCorp := Name{Issuer: issuerCorp, Tenant: tenantShared}
	if !policy.Allows(emptyCorp) {
		t.Fatal("empty-path corp principal should be allowed by the broad rule")
	}

	decision := report.Decisions[0]
	if decision.Safe {
		t.Fatal("empty-prefix merged candidate cannot be safe")
	}
	if decision.Witnesses[0].Old.Issuer == "" {
		t.Fatal("expected a concrete old principal witness")
	}

	model := Model{
		Authorities: []HeaderKey{
			{Issuer: issuerCorp, Tenant: tenantShared},
			{Issuer: issuerHub, Tenant: tenantHub},
		},
		Alphabet: smallAlphabet,
		MaxDepth: 2,
		Labeler:  labeler,
	}
	var sawEmpty bool
	for _, n := range model.Enumerate() {
		if len(n.Path) == 0 {
			sawEmpty = true
		}
	}
	if !sawEmpty {
		t.Fatal("model enumeration omitted the empty path")
	}
}

func TestManyToOneAndExceptionCoverage(t *testing.T) {
	mapper, policy, labeler, public, candidates := buildFixture()
	report := Project(candidates, mapper, policy, labeler, public)

	// Hub /b is rejected because corp [b] (allowed) and retail [a b]
	// (retail /b deny) collapse onto the same hub path [b].
	decision := report.Decisions[2]
	if decision.Safe {
		t.Fatal("many-to-one /b candidate must be rejected")
	}
	foundRetailDeny := false
	foundManyToOne := false
	for _, w := range decision.Witnesses {
		if w.Old.Header().Issuer == issuerRetail && PathEquals(w.Target.Path, []string{"b"}) {
			foundRetailDeny = true
			// The witness's trace must explain exactly how the retail name
			// reaches hub [b], proving this is a shared (many-to-one) target.
			if PathEquals(traceOutput(w.Trace), []string{"b"}) {
				foundManyToOne = true
			}
		}
	}
	if !foundRetailDeny {
		t.Fatal("missing a retail deny preimage collapsing onto hub [b]")
	}
	if !foundManyToOne {
		t.Fatal("deny witness trace does not reconstruct the shared hub [b] target")
	}

	if report.Policy.RuleCount() != report.MinimumRuleCount {
		t.Fatal("reported policy size must equal the proven minimum")
	}
	var hasException bool
	for _, rule := range report.Policy.Rules {
		if PathEquals(rule.Prefix.Path, []string{"a", "b"}) && len(rule.Require) == 0 {
			hasException = true
		}
	}
	if !hasException {
		t.Fatal("the /a/b exception rule was lost in minimization")
	}
}

func TestSameTenantDifferentIssuers(t *testing.T) {
	mapper, _, _, public, candidates := buildFixture()
	report := Project(candidates, mapper, OldPolicy{Default: Deny}, PrefixLabeler{}, public)

	// Re-run with the real fixture policy for an importable policy check.
	mapper2, policy2, labeler2, public2, candidates2 := buildFixture()
	report = Project(candidates2, mapper2, policy2, labeler2, public2)

	partnerName := Name{
		Issuer: issuerPartner, Tenant: tenantShared,
		Path: []string{"b"}, Labels: []Label{"retail-wide"},
	}
	if !report.Policy.Allows(partnerName) {
		t.Fatal("partner /b identity should be allowed")
	}
	if report.Policy.Allows(Name{Issuer: issuerPartner, Tenant: "other-tenant", Path: []string{"b"}}) {
		t.Fatal("a different tenant under the same issuer must not match")
	}

	// Same tenant name string and same path, different issuer: stay denied.
	cross := Name{Issuer: "hub.wrong.example", Tenant: tenantShared, Path: []string{"b"}}
	if report.Policy.Allows(cross) {
		t.Fatal("issuer confusion: a same-named foreign tenant was admitted")
	}
}

func TestCompleteModelSoundnessAndCoverage(t *testing.T) {
	mapper, policy, labeler, public, candidates := buildFixture()
	report := Project(candidates, mapper, policy, labeler, public)

	authorities := []HeaderKey{
		{Issuer: issuerCorp, Tenant: tenantShared},
		{Issuer: issuerRetail, Tenant: tenantShared},
		{Issuer: issuerHub, Tenant: tenantHub},
		{Issuer: issuerPartner, Tenant: tenantShared},
	}
	model := Model{
		Authorities: authorities,
		Alphabet:    smallAlphabet,
		MaxDepth:    3,
		Labeler:     labeler,
	}

	if violation, ok := VerifySoundness(report, mapper, policy, model); !ok {
		t.Fatalf("soundness violation at %v", violation.Path)
	}
	if missing, ok := VerifyCoverage(report, model); !ok {
		t.Fatalf("coverage gap at %v", missing.Path)
	}
	if bad, ok := VerifyWitnesses(report, policy); !ok {
		t.Fatalf("invalid witness for %v", bad.Target.Path)
	}
}

func TestMinimumRuleCountIsProven(t *testing.T) {
	mapper, policy, labeler, public, candidates := buildFixture()
	report := Project(candidates, mapper, policy, labeler, public)

	// Safe candidates: C2, C6, C7. C7 is contained in C6 (same prefix, C6
	// demands no labels), so the minimum equivalent policy has two rules.
	if report.MinimumRuleCount != 2 {
		t.Fatalf("minimum rule count = %d, want 2", report.MinimumRuleCount)
	}
	if got := report.Policy.RuleCount(); got != 2 {
		t.Fatalf("policy has %d rules, want 2", got)
	}

	// Brute-force cross-check: no subset smaller than the reported minimum
	// covers every safe candidate.
	var safe []*Decision
	for _, decision := range report.Decisions {
		if decision.Safe {
			safe = append(safe, decision)
		}
	}
	for size := 0; size < report.MinimumRuleCount; size++ {
		forEachCombination(len(safe), size, func(idx []int) bool {
			subset := make([]*Decision, size)
			for k, v := range idx {
				subset[k] = safe[v]
			}
			if equivalentUnion(safe, subset) {
				t.Fatalf("subset of size %d already equivalent; minimum wrong", size)
			}
			return true
		})
	}
}

func TestUnboundedPathsTerminateAndStaySound(t *testing.T) {
	mapper, policy, labeler, public, candidates := buildFixture()

	// A cyclic transducer can be driven with an arbitrarily long path; both
	// transduction and the finite product projection must terminate.
	done := make(chan struct{})
	go func() {
		Project(candidates, mapper, policy, labeler, public)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("projection did not terminate on cyclic transducer")
	}

	longPath := repeatPath("a", 50)
	ch := mapper.ChannelForOld(HeaderKey{Issuer: issuerCorp, Tenant: tenantShared})
	images := ch.Transducer.Images(longPath)
	if len(images) != 1 || !PathEquals(images[0].Out, longPath) {
		t.Fatal("identity transducer must round-trip an unbounded-length path")
	}

	// The long corp path [a,a,...] sits under the /a deny exception with no
	// deeper allow rule, so it must remain old-denied and never projected.
	longName := Name{Issuer: issuerCorp, Tenant: tenantShared, Path: longPath}
	longName.Labels = labeler.Labels(longName)
	if policy.Allows(longName) {
		t.Fatal("long /a path must be denied by the last-match deny")
	}
	report := Project(candidates, mapper, policy, labeler, public)
	if report.Policy.Allows(Name{
		Issuer: issuerHub, Tenant: tenantHub, Path: longPath, Labels: longName.Labels,
	}) {
		t.Fatal("projection admitted a long-path identity that the old host denies")
	}
}

func TestGapWitnessHasMappingTrajectory(t *testing.T) {
	mapper, _, _, _, candidates := buildFixture()

	// C9 is partner /a, which the a->b partner transducer cannot emit (its
	// output language is b*).
	ch := mapper.ChannelsForNew(HeaderKey{Issuer: issuerPartner, Tenant: tenantShared})[0]
	if ch.Transducer.HasPreimage([]string{"a"}) {
		t.Fatal("partner transducer unexpectedly has an /a preimage")
	}
	if !ch.Transducer.HasPreimage([]string{"b", "b"}) {
		t.Fatal("partner transducer should accept the b* word [b b]")
	}

	decision := analyzeCandidate(candidates[8], mapper, OldPolicy{Default: Deny},
		PrefixLabeler{}, newLabelSet(nil))
	if decision.Reason != RejectNoPreimage {
		t.Fatalf("reason = %q, want no-preimage", decision.Reason)
	}
	w := decision.Witnesses[0]
	if !PathEquals(w.Target.Path, []string{"a"}) {
		t.Fatalf("gap target = %v, want [a]", w.Target.Path)
	}
	if w.Old.Issuer != issuerRetail {
		t.Fatalf("gap boundary old issuer = %q, want retail", w.Old.Issuer)
	}
	// The boundary is the retail empty-path principal, whose trajectory is an
	// empty (nil) trace; traceOutput on it must still reproduce the empty word.
	if len(traceOutput(w.Trace)) != 0 {
		t.Fatal("empty-path boundary trace must reconstruct the empty word")
	}
}

func repeatPath(symbol string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = symbol
	}
	return out
}
