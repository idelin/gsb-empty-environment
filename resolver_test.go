package netsec

import "testing"

func mustRange(t *testing.T, s string) VersionRange {
	t.Helper()
	r, err := ParseVersionRange(s)
	if err != nil {
		t.Fatalf("ParseVersionRange(%q): %v", s, err)
	}
	return r
}

func TestAuditChainReachability(t *testing.T) {
	full := []FullAudit{{Version: "1.0.0", Criteria: CriteriaSafeRun}}
	delta := []DeltaAudit{
		{From: "1.0.0", To: "1.1.0", Criteria: CriteriaSafeRun},
		{From: "1.1.0", To: "1.2.0", Criteria: CriteriaSafeRun},
		{From: "1.2.0", To: "1.2.1", Criteria: CriteriaSafeRun},
		// Broken chain: 1.9.0 is never reached, so 2.0.0 stays unreachable.
		{From: "1.9.0", To: "2.0.0", Criteria: CriteriaSafeRun},
		// Different criteria never joins the run chain.
		{From: "1.2.0", To: "1.3.0", Criteria: CriteriaSafeDeploy},
	}
	r, err := NewResolver(full, delta, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	allowed := []string{"1.0.0", "1.1.0", "1.2.0", "1.2.1"}
	denied := []string{"1.9.0", "2.0.0", "1.3.0", "1.2.2", "0.9.0"}
	for _, v := range allowed {
		ok, err := r.Allowed(v, CriteriaSafeRun)
		if err != nil || !ok {
			t.Errorf("Allowed(%q, safe-to-run) = %v, %v; want true", v, ok, err)
		}
	}
	for _, v := range denied {
		ok, err := r.Allowed(v, CriteriaSafeRun)
		if err != nil || ok {
			t.Errorf("Allowed(%q, safe-to-run) = %v, %v; want false", v, ok, err)
		}
	}

	// The deploy-only edge cannot satisfy a run gate, and no deploy root
	// exists, so every deploy gate request is denied.
	ok, err := r.Allowed("1.3.0", CriteriaSafeDeploy)
	if err != nil || ok {
		t.Errorf("Allowed(1.3.0, safe-to-deploy) = %v, %v; want false", ok, err)
	}
}

func TestBrokenChainFromUnattestedStart(t *testing.T) {
	full := []FullAudit{{Version: "1.0.0", Criteria: CriteriaSafeRun}}
	delta := []DeltaAudit{
		// Attacker links an untrusted start to an evil version.
		{From: "0.9.0", To: "9.9.9", Criteria: CriteriaSafeRun},
		{From: "9.9.9", To: "9.9.10", Criteria: CriteriaSafeRun},
	}
	r, err := NewResolver(full, delta, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	for _, v := range []string{"0.9.0", "9.9.9", "9.9.10"} {
		ok, err := r.Allowed(v, CriteriaSafeRun)
		if err != nil || ok {
			t.Errorf("Allowed(%q) = %v, %v; want false (broken chain)", v, ok, err)
		}
	}
	ok, err := r.Allowed("1.0.0", CriteriaSafeRun)
	if err != nil || !ok {
		t.Errorf("full audit root must pass: %v, %v", ok, err)
	}
}

func TestCycleDoesNotBlockResolution(t *testing.T) {
	full := []FullAudit{{Version: "1.0.0", Criteria: CriteriaSafeRun}}
	delta := []DeltaAudit{
		{From: "1.0.0", To: "1.1.0", Criteria: CriteriaSafeRun},
		{From: "1.1.0", To: "1.2.0", Criteria: CriteriaSafeRun},
		// Cycle back into earlier nodes, including a self loop.
		{From: "1.2.0", To: "1.1.0", Criteria: CriteriaSafeRun},
		{From: "1.2.0", To: "1.2.0", Criteria: CriteriaSafeRun},
		// A node reachable only by going around the cycle.
		{From: "1.1.0", To: "1.1.1", Criteria: CriteriaSafeRun},
	}
	r, err := NewResolver(full, delta, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	for _, v := range []string{"1.0.0", "1.1.0", "1.1.1", "1.2.0"} {
		ok, err := r.Allowed(v, CriteriaSafeRun)
		if err != nil || !ok {
			t.Errorf("Allowed(%q) = %v, %v; want true despite cycle", v, ok, err)
		}
	}
	ok, err := r.Allowed("1.3.0", CriteriaSafeRun)
	if err != nil || ok {
		t.Errorf("Allowed(1.3.0) = %v, %v; want false", ok, err)
	}
}

func TestViolationOverridesAudit(t *testing.T) {
	full := []FullAudit{{Version: "1.0.0", Criteria: CriteriaSafeRun}}
	delta := []DeltaAudit{
		{From: "1.0.0", To: "1.1.0", Criteria: CriteriaSafeRun},
		{From: "1.1.0", To: "1.2.0", Criteria: CriteriaSafeRun},
	}
	violations := []Violation{{Range: mustRange(t, "[1.1.0,1.2.0)")}}
	r, err := NewResolver(full, delta, violations, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// 1.1.0 is audited and reachable, but the violation denies it.
	for _, v := range []string{"1.1.0", "1.1.5"} {
		ok, err := r.Allowed(v, CriteriaSafeRun)
		if err != nil || ok {
			t.Errorf("Allowed(%q) = %v, %v; want false (violation wins)", v, ok, err)
		}
		violated, err := r.Violated(v)
		if err != nil || !violated {
			t.Errorf("Violated(%q) = %v, %v; want true", v, violated, err)
		}
	}
	// Exclusive upper bound: 1.2.0 is outside the interval and audited.
	ok, err := r.Allowed("1.2.0", CriteriaSafeRun)
	if err != nil || !ok {
		t.Errorf("Allowed(1.2.0) = %v, %v; want true (exclusive upper bound)", ok, err)
	}
	ok, err = r.Allowed("1.0.0", CriteriaSafeRun)
	if err != nil || !ok {
		t.Errorf("Allowed(1.0.0) = %v, %v; want true", ok, err)
	}

	// A full audit on an explicitly violated version is still denied.
	badRoot, err := NewResolver(
		[]FullAudit{{Version: "2.0.0", Criteria: CriteriaSafeRun}},
		nil,
		[]Violation{{Range: mustRange(t, "[2.0.0,2.0.0]")}},
		nil,
	)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	ok, err = badRoot.Allowed("2.0.0", CriteriaSafeRun)
	if err != nil || ok {
		t.Errorf("violated full-audit root must be denied: %v, %v", ok, err)
	}
}

func TestCriteriaImplication(t *testing.T) {
	full := []FullAudit{
		{Version: "1.0.0", Criteria: CriteriaSafeDeploy},
		{Version: "2.0.0", Criteria: CriteriaSafeRun},
	}
	delta := []DeltaAudit{
		{From: "1.0.0", To: "1.1.0", Criteria: CriteriaSafeDeploy},
		{From: "2.0.0", To: "2.1.0", Criteria: CriteriaSafeRun},
	}
	r, err := NewResolver(full, delta, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Deploy chain satisfies both deploy and run gates.
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if ok, err := r.Allowed(v, CriteriaSafeDeploy); err != nil || !ok {
			t.Errorf("Allowed(%q, deploy) = %v, %v; want true", v, ok, err)
		}
		if ok, err := r.Allowed(v, CriteriaSafeRun); err != nil || !ok {
			t.Errorf("Allowed(%q, run) = %v, %v; want true (deploy implies run)", v, ok, err)
		}
	}
	// Run-only chain never satisfies a deploy gate (reverse does not hold).
	for _, v := range []string{"2.0.0", "2.1.0"} {
		if ok, err := r.Allowed(v, CriteriaSafeRun); err != nil || !ok {
			t.Errorf("Allowed(%q, run) = %v, %v; want true", v, ok, err)
		}
		if ok, err := r.Allowed(v, CriteriaSafeDeploy); err != nil || ok {
			t.Errorf("Allowed(%q, deploy) = %v, %v; want false (run does not imply deploy)", v, ok, err)
		}
	}
}

func TestCustomCriterionImplication(t *testing.T) {
	custom := func(a, b string) bool {
		if a == b {
			return true
		}
		// A strict audit satisfies both built-in gates.
		if a == "strict-review" && (b == CriteriaSafeRun || b == CriteriaSafeDeploy) {
			return true
		}
		return false
	}
	r, err := NewResolver(
		[]FullAudit{{Version: "3.0.0", Criteria: "strict-review"}},
		nil, nil, custom,
	)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if ok, _ := r.Allowed("3.0.0", CriteriaSafeDeploy); !ok {
		t.Error("custom implication should let strict-review satisfy deploy")
	}
	// Unknown criteria with no implication path stays denied.
	if ok, _ := r.Allowed("3.0.0", "unknown-gate"); ok {
		t.Error("unrelated gate criteria must not pass")
	}
}

func TestPrereleaseAndBuildMetadataInChain(t *testing.T) {
	// Build metadata differs but canonical versions match, so the chain
	// connects and the prerelease ordering gates range membership.
	full := []FullAudit{{Version: "1.0.0-rc.1+ci.42", Criteria: CriteriaSafeRun}}
	delta := []DeltaAudit{
		{From: "1.0.0-rc.1", To: "1.0.0-rc.2+ci.99", Criteria: CriteriaSafeRun},
		{From: "1.0.0-rc.2+ci.99", To: "1.0.0", Criteria: CriteriaSafeRun},
	}
	violations := []Violation{{Range: mustRange(t, "[1.0.0-rc.2,1.0.0-rc.2]")}}
	r, err := NewResolver(full, delta, violations, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	if ok, err := r.Allowed("1.0.0-rc.1+anything", CriteriaSafeRun); err != nil || !ok {
		t.Errorf("rc.1 should pass despite build metadata: %v, %v", ok, err)
	}
	if ok, err := r.Allowed("1.0.0-rc.2", CriteriaSafeRun); err != nil || ok {
		t.Errorf("rc.2 should be denied by prerelease violation: %v, %v", ok, err)
	}
	if ok, err := r.Allowed("1.0.0", CriteriaSafeRun); err != nil || !ok {
		t.Errorf("release 1.0.0 should pass: %v, %v", ok, err)
	}
}

func TestResolverRejectsBadRecords(t *testing.T) {
	cases := []struct {
		name       string
		full       []FullAudit
		delta      []DeltaAudit
		violations []Violation
	}{
		{"bad full version", []FullAudit{{Version: "nope", Criteria: CriteriaSafeRun}}, nil, nil},
		{"empty full criteria", []FullAudit{{Version: "1.0.0", Criteria: ""}}, nil, nil},
		{"bad delta from", nil, []DeltaAudit{{From: "1.0", To: "1.1.0", Criteria: CriteriaSafeRun}}, nil},
		{"bad delta to", nil, []DeltaAudit{{From: "1.0.0", To: "1.1", Criteria: CriteriaSafeRun}}, nil},
		{"empty delta criteria", nil, []DeltaAudit{{From: "1.0.0", To: "1.1.0", Criteria: ""}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewResolver(tc.full, tc.delta, tc.violations, nil); err == nil {
				t.Fatal("expected constructor error")
			}
		})
	}

	r, err := NewResolver(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("empty resolver: %v", err)
	}
	if ok, err := r.Allowed("1.0.0", CriteriaSafeRun); err != nil || ok {
		t.Errorf("empty resolver must deny everything: %v, %v", ok, err)
	}
	if _, err := r.Allowed("not-a-version", CriteriaSafeRun); err == nil {
		t.Error("malformed query version must return error")
	}
	if _, err := r.Allowed("1.0.0", ""); err == nil {
		t.Error("empty gate criteria must return error")
	}
}
