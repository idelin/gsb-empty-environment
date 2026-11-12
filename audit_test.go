package netsec

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, s *Store, recs ...Record) {
	t.Helper()
	if err := s.AddRecords(recs...); err != nil {
		t.Fatalf("AddRecords: %v", err)
	}
}

func TestFullAuditAnchorPasses(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}})

	v, err := s.Evaluate("serde", "1.0.0", SafeToDeploy)
	if err != nil || !v.Allowed || v.Reason != ReasonReachable {
		t.Fatalf("full audit should pass, got %+v err=%v", v, err)
	}
}

func TestDeltaChainFromFullAudit(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.0.0", "1.1.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.1.0", "1.2.0", SafeToDeploy}},
	)
	for _, ver := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		allowed, err := s.IsAllowed("serde", ver, SafeToDeploy)
		if err != nil || !allowed {
			t.Errorf("version %s should be reachable, allowed=%v err=%v", ver, allowed, err)
		}
	}
	allowed, err := s.IsAllowed("serde", "1.3.0", SafeToDeploy)
	if err != nil || allowed {
		t.Errorf("1.3.0 has no edge to it, allowed=%v err=%v", allowed, err)
	}
}

func TestBrokenChainRejectsUnauditedStart(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		// Delta chain starts at 2.0.0 which was never fully audited.
		Record{Delta: &DeltaAudit{"serde", "2.0.0", "2.1.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "2.1.0", "2.2.0", SafeToDeploy}},
	)
	v, err := s.Evaluate("serde", "2.2.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonBrokenChain {
		t.Fatalf("unaudited chain start must break the chain, got %+v", v)
	}
}

func TestCycleDoesNotHangAndUnanchoredCycleRejects(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Delta: &DeltaAudit{"serde", "1.0.0", "1.1.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.1.0", "1.0.0", SafeToDeploy}},
	)
	v, err := s.Evaluate("serde", "1.1.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonBrokenChain {
		t.Fatalf("cycle without anchor must reject, got %+v", v)
	}

	// With an anchor, a cycle is traversed safely and its nodes pass.
	mustAdd(t, s, Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}})
	v, err = s.Evaluate("serde", "1.1.0", SafeToDeploy)
	if err != nil || !v.Allowed {
		t.Fatalf("anchored cycle node should pass, got %+v err=%v", v, err)
	}
}

func TestViolationRangeBeatsAuditChain(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.0.0", "1.1.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.1.0", "1.2.0", SafeToDeploy}},
		Record{Viol: &Violation{Crate: "serde", Range: ">=1.1.0 <1.2.0"}},
	)

	// 1.0.0 outside the range still passes.
	if allowed, _ := s.IsAllowed("serde", "1.0.0", SafeToDeploy); !allowed {
		t.Error("1.0.0 outside violation range should pass")
	}
	// 1.1.0 is inside the range despite the valid delta edge into it.
	v, err := s.Evaluate("serde", "1.1.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonViolation || v.Detail != ">=1.1.0 <1.2.0" {
		t.Fatalf("violation must take precedence, got %+v", v)
	}
	// 1.2.0 sits exactly on the exclusive upper bound, so it passes.
	if allowed, _ := s.IsAllowed("serde", "1.2.0", SafeToDeploy); !allowed {
		t.Error("1.2.0 is outside the exclusive upper bound and should pass")
	}
}

func TestViolationRangeBoundaries(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Viol: &Violation{Crate: "serde", Range: ">=1.0.0 <=2.0.0"}},
	)
	for _, ver := range []string{"0.9.9", "1.0.0", "1.5.0", "2.0.0", "2.0.1"} {
		mustAdd(t, s, Record{Full: &FullAudit{"serde", ver, SafeToDeploy}})
	}
	for _, tc := range []struct {
		ver   string
		block bool
	}{
		{"0.9.9", false},
		{"1.0.0", true},
		{"1.5.0", true},
		{"2.0.0", true},
		{"2.0.1", false},
	} {
		v, err := s.Evaluate("serde", tc.ver, SafeToDeploy)
		if err != nil {
			t.Fatal(err)
		}
		if v.Allowed == tc.block {
			t.Errorf("version %s blocked=%v, want %v (reason %s)", tc.ver, !v.Allowed, tc.block, v.Reason)
		}
	}
}

func TestViolationOnPrereleaseBySemverOrder(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0-alpha", SafeToDeploy}},
		Record{Viol: &Violation{Crate: "serde", Range: ">=1.0.0-alpha <1.0.0"}},
	)
	v, err := s.Evaluate("serde", "1.0.0-rc.9", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonViolation {
		t.Fatalf("prerelease inside violation range must be blocked, got %+v", v)
	}

	// Build metadata must not move a version out of the range.
	v, err = s.Evaluate("serde", "1.0.0-alpha+build.77", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonViolation {
		t.Fatalf("build metadata must not evade a violation, got %+v", v)
	}
}

func TestDeployImpliesRunButNotReverse(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
	)
	// A deploy audit satisfies the weaker run criteria.
	if allowed, err := s.IsAllowed("serde", "1.0.0", SafeToRun); err != nil || !allowed {
		t.Fatalf("safe-to-deploy must imply safe-to-run, allowed=%v err=%v", allowed, err)
	}

	// A run-only audit must not satisfy deploy.
	s2 := NewStore()
	mustAdd(t, s2, Record{Full: &FullAudit{"serde", "1.0.0", SafeToRun}})
	v, err := s2.Evaluate("serde", "1.0.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed {
		t.Fatalf("safe-to-run must not imply safe-to-deploy, got %+v", v)
	}
	if allowed, _ := s2.IsAllowed("serde", "1.0.0", SafeToRun); !allowed {
		t.Error("the run-only audit should still satisfy safe-to-run itself")
	}
}

func TestDeltaChainMustKeepSameCriteria(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
		// Only a run-level review for the jump, which does not carry a
		// deploy query even though deploy implies run.
		Record{Delta: &DeltaAudit{"serde", "1.0.0", "1.1.0", SafeToRun}},
	)
	v, err := s.Evaluate("serde", "1.1.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonNoAudit {
		t.Fatalf("weaker run delta must not extend a deploy chain, got %+v", v)
	}

	// The same edge is fine for a run query starting from the deploy
	// anchor, since deploy evidence covers run.
	v, err = s.Evaluate("serde", "1.1.0", SafeToRun)
	if err != nil || !v.Allowed {
		t.Fatalf("run query should traverse the run delta, got %+v err=%v", v, err)
	}
}

func TestViolationCriteriaDirection(t *testing.T) {
	// A run-level violation blocks both run and deploy queries because
	// deploy implies run; a deploy-level violation does not block a run
	// query that never claimed deploy-level evidence.
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
		Record{Viol: &Violation{Crate: "serde", Range: "=1.0.0", Criteria: SafeToRun}},
	)
	for _, criteria := range []string{SafeToRun, SafeToDeploy} {
		v, err := s.Evaluate("serde", "1.0.0", criteria)
		if err != nil {
			t.Fatal(err)
		}
		if v.Allowed || v.Reason != ReasonViolation {
			t.Errorf("run violation must block %s query, got %+v", criteria, v)
		}
	}

	s2 := NewStore()
	mustAdd(t, s2,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToRun}},
		Record{Viol: &Violation{Crate: "serde", Range: "=1.0.0", Criteria: SafeToDeploy}},
	)
	v, err := s2.Evaluate("serde", "1.0.0", SafeToRun)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Allowed {
		t.Fatalf("deploy-level violation must not block a run-only query, got %+v", v)
	}
}

func TestWildcardViolationBlocksAllCriteria(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
		Record{Viol: &Violation{Crate: "serde", Range: "*"}},
	)
	for _, criteria := range []string{SafeToDeploy, SafeToRun} {
		v, err := s.Evaluate("serde", "5.0.0", criteria)
		if err != nil {
			t.Fatal(err)
		}
		if v.Allowed || v.Reason != ReasonViolation {
			t.Errorf("wildcard violation must block %s, got %+v", criteria, v)
		}
	}
}

func TestCustomImplicationTransitiveAndCycle(t *testing.T) {
	s := NewStore()
	if err := s.AddImplication("crypto-safe", SafeToDeploy); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, Record{Full: &FullAudit{"vault", "3.0.0", "crypto-safe"}})

	// crypto-safe -> safe-to-deploy -> safe-to-run, transitively.
	for _, criteria := range []string{"crypto-safe", SafeToDeploy, SafeToRun} {
		if allowed, err := s.IsAllowed("vault", "3.0.0", criteria); err != nil || !allowed {
			t.Errorf("transitive implication failed for %s", criteria)
		}
	}

	// Reverse edge would make deploy imply crypto-safe, a cycle.
	err := s.AddImplication(SafeToDeploy, "crypto-safe")
	if !errors.Is(err, ErrImplicationCycle) {
		t.Fatalf("expected ErrImplicationCycle, got %v", err)
	}
}

func TestUnknownAndInvalidInputs(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}})

	v, err := s.Evaluate("unknown-crate", "1.0.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonUnknownCrate {
		t.Fatalf("unknown crate verdict wrong, got %+v", v)
	}

	if _, err := s.Evaluate("serde", "not-a-version", SafeToDeploy); err == nil {
		t.Error("invalid target version should error")
	}
	if _, err := s.Evaluate("serde", "1.0.0", "mystery-criteria"); !errors.Is(err, ErrUnknownCriteria) {
		t.Errorf("unknown criteria should wrap ErrUnknownCriteria, got %v", err)
	}
	if err := s.AddFullAudit(FullAudit{"serde", "1.0.0", AnyCriteria}); !errors.Is(err, ErrReservedCriteria) {
		t.Errorf("wildcard audit should be rejected, got %v", err)
	}
}

func TestBuildMetadataSharesGraphIdentity(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0+build.1", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.0.0+build.1", "1.1.0+build.2", SafeToDeploy}},
	)
	v, err := s.Evaluate("serde", "1.1.0+build.99", SafeToDeploy)
	if err != nil || !v.Allowed {
		t.Fatalf("build metadata variants must share identity, got %+v err=%v", v, err)
	}
}

func TestSelfLoopAndUnmatchedAnchor(t *testing.T) {
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToRun}},
		// A self-loop must terminate and never manufacture deploy trust.
		Record{Delta: &DeltaAudit{"serde", "1.0.0", "1.0.0", SafeToRun}},
	)
	v, err := s.Evaluate("serde", "1.0.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed {
		t.Fatalf("run-only anchor plus self-loop must not pass deploy, got %+v", v)
	}

	// Run query on the same anchored node still passes.
	if allowed, err := s.IsAllowed("serde", "1.0.0", SafeToRun); err != nil || !allowed {
		t.Fatalf("run query on run anchor should pass, allowed=%v err=%v", allowed, err)
	}
}

func TestMalformedRecordsRejected(t *testing.T) {
	s := NewStore()
	if err := s.AddFullAudit(FullAudit{"serde", "nope", SafeToDeploy}); err == nil {
		t.Error("full audit with invalid version should error")
	}
	if err := s.AddDeltaAudit(DeltaAudit{"serde", "1.0.0", "1.0", SafeToDeploy}); err == nil {
		t.Error("delta audit with invalid 'to' version should error")
	}
	if err := s.AddViolation(Violation{Crate: "serde", Range: ">=1.0.0"}); err != nil {
		t.Errorf("valid violation range rejected: %v", err)
	}
	if err := s.AddViolation(Violation{Crate: "serde", Range: "not-a-range"}); err == nil {
		t.Error("invalid violation range should error")
	}
	if err := s.AddImplication(SafeToRun, ""); err == nil {
		t.Error("empty criteria in implication should error")
	}
	if err := s.AddRecords(Record{}); err == nil {
		t.Error("empty record should error")
	}
}

func TestDeltaIntoViolatedVersionBlocksChain(t *testing.T) {
	// Attacker publishes 1.2.0 inside a violation range and supplies a
	// delta audit 1.1.0 -> 1.2.0; the violation must still block it.
	s := NewStore()
	mustAdd(t, s,
		Record{Full: &FullAudit{"serde", "1.0.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.0.0", "1.1.0", SafeToDeploy}},
		Record{Delta: &DeltaAudit{"serde", "1.1.0", "1.2.0", SafeToDeploy}},
		Record{Viol: &Violation{Crate: "serde", Range: "=1.2.0"}},
	)
	v, err := s.Evaluate("serde", "1.2.0", SafeToDeploy)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Reason != ReasonViolation {
		t.Fatalf("attacker version behind delta edge must be blocked, got %+v", v)
	}
}
