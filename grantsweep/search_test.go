package grantsweep

import "testing"

func TestReferenceHasNoFinding(t *testing.T) {
	finding, evaluated, err := Search(Policy{}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if finding != nil {
		t.Fatalf("reference model must never allow an unauthorized call:\n%s", RenderTrace(finding))
	}
	t.Logf("reference states explored: %d", evaluated)
}

func TestEveryWeakPolicyHasShortestTrace(t *testing.T) {
	expected := []string{
		"IgnoreSubset",
		"IgnoreExtraContext",
		"IgnoreCaller",
		"IgnoreOperation",
		"IgnoreKeyVersion",
		"GrantIDLookup",
		"CrossClientPending",
	}
	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			policy := policyByName(t, name)
			finding, _, err := Search(policy, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			if finding == nil {
				t.Fatalf("expected differential trace for %s", name)
			}
			last := finding.Trace[len(finding.Trace)-1]
			if !last.Allowed {
				t.Fatalf("terminal step must be an unauthorized ALLOW:\n%s", RenderTrace(finding))
			}
			t.Logf("\n%s", RenderTrace(finding))
		})
	}
}

func TestSearchAllPolicies(t *testing.T) {
	findings, stats, err := SearchAllPolicies(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	// NonAtomicRetire is a scheduling property covered by SearchRetireRace.
	wantFindings := len(AllPolicies()) - 2
	if len(findings) != wantFindings {
		t.Fatalf("weak policies=%d, findings=%d", wantFindings, len(findings))
	}
	for name, finding := range findings {
		if finding.Policy.Name() != name {
			t.Fatalf("finding policy mismatch: %s", name)
		}
	}
	t.Logf("search stats: %v", stats)
}

func policyByName(t *testing.T, name string) Policy {
	t.Helper()
	for _, p := range AllPolicies() {
		if p.Name() == name {
			return p
		}
	}
	t.Fatalf("unknown policy %q", name)
	return Policy{}
}

func TestRetireRaceSchedule(t *testing.T) {
	finding, interleavings := SearchRetireRace(Policy{NonAtomicRetire: true})
	if finding == nil {
		t.Fatalf("expected a non-atomic retire race across %d interleavings", interleavings)
	}
	t.Logf("\n%s", RenderSchedule(finding))

	// The reference atomic design must show no race under the same schedules.
	refFinding, _ := SearchRetireRace(Policy{})
	if refFinding != nil {
		t.Fatalf("atomic retire must not race:\n%s", RenderSchedule(refFinding))
	}
}

func TestCacheKeyDifferential(t *testing.T) {
	collisions := SearchCacheKeyCollisions()
	if len(collisions) == 0 {
		t.Fatal("expected GrantID-only key collisions")
	}
	for _, c := range collisions {
		if CacheKey(c.A) == CacheKey(c.B) {
			t.Fatalf("strong key must distinguish entries: %+v", c)
		}
		if WeakGrantIDKey(c.A) != WeakGrantIDKey(c.B) {
			t.Fatalf("weak key should collide: %+v", c)
		}
		t.Logf("collision on %q", c.Key)
	}
}
