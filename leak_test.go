package netsec

import (
	"strings"
	"testing"
)

func leakPolicySet(t *testing.T) *PolicySet {
	// export: routes tagged SECRET(0) are rejected while they still
	// carry the secret; the scrub sub-policy strips the tag for
	// internal prefixes, after which accept-rest leaks them.
	ps := &PolicySet{Policies: map[string]*Policy{
		"export": {
			Name: "export",
			Rules: []Rule{
				{Name: "delegate-scrub", Do: CallPolicy{Name: "scrub"}},
				{
					Name: "guard-secret",
					When: TagCondition{Op: TagAll, Tags: secretSet(0)},
					Do:   Reject{},
				},
				{Name: "accept-rest", Do: Accept{}},
			},
		},
		"scrub": {
			Name: "scrub",
			Rules: []Rule{
				{
					Name: "strip-internal",
					When: PrefixRange{
						Network: MustParsePrefix("10.0.0.0/8"),
						MinLen:  16,
						MaxLen:  24,
					},
					Do: DelTags{Tags: secretSet(0)},
				},
			},
		},
	}}
	if err := ps.Validate(); err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestCheckLeakFindsMinimalWitness(t *testing.T) {
	ps := leakPolicySet(t)
	report := ps.CheckLeak(LeakSpec{
		Entry:      "export",
		SecretTag:  0,
		Neighbors:  []Neighbor{{Name: "peer-X"}},
		MaxPathLen: 8,
	})
	if report.Safe || report.Leak == nil {
		t.Fatalf("expected a leak, stats=%+v", report.Stats)
	}
	w := report.Leak
	if w.Neighbor.Name != "peer-X" {
		t.Fatalf("wrong neighbor: %v", w.Neighbor)
	}
	if !w.Route.Tags.Contains(0) {
		t.Fatal("witness must start with the secret tag")
	}
	if w.ExportedRoute.Tags.Contains(0) {
		t.Fatal("exported route should have had the secret stripped")
	}
	if w.SecretRetained {
		t.Fatal("secret should not be retained on this witness")
	}
	if w.Route.Prefix.String() != "10.0.0.0/16" {
		t.Fatalf("expected minimal prefix 10.0.0.0/16, got %s", w.Route.Prefix)
	}
	if w.Route.PathLen != 0 {
		t.Fatalf("expected minimal path length 0, got %d", w.Route.PathLen)
	}
	if !strings.Contains(RenderTrace(w.Trace), "rule strip-internal MATCH") {
		t.Fatalf("trace must show the stripping rule:\n%s", RenderTrace(w.Trace))
	}
	if report.Stats.Evaluations == 0 {
		t.Fatal("stats did not count evaluations")
	}
}

func TestCheckLeakSafeWhenGuardHolds(t *testing.T) {
	ps := &PolicySet{Policies: map[string]*Policy{
		"export": {
			Name: "export",
			Rules: []Rule{
				{Name: "deny-secret", When: TagCondition{Op: TagAll, Tags: secretSet(0)}, Do: Reject{}},
				{Name: "accept", Do: Accept{}},
			},
		},
	}}
	if err := ps.Validate(); err != nil {
		t.Fatal(err)
	}
	report := ps.CheckLeak(LeakSpec{
		Entry:      "export",
		SecretTag:  0,
		Neighbors:  []Neighbor{{Name: "peer-Y"}},
		MaxPathLen: 4,
	})
	if !report.Safe || report.Leak != nil {
		t.Fatalf("policy should be safe over the finite domain: %+v", report.Errors)
	}
}

func TestRequireSecretRetained(t *testing.T) {
	ps := leakPolicySet(t)
	report := ps.CheckLeak(LeakSpec{
		Entry:                 "export",
		SecretTag:             0,
		Neighbors:             []Neighbor{{Name: "peer-Z"}},
		RequireSecretRetained: true,
		MaxPathLen:            4,
	})
	if !report.Safe {
		t.Fatalf("expected no retained-secret leak, got %+v", report.Leak)
	}
}

func TestFullTagDomainEnumeration(t *testing.T) {
	ps := &PolicySet{Policies: map[string]*Policy{
		"export": {
			Name: "export",
			Rules: []Rule{
				{Name: "deny-secret", When: TagCondition{Op: TagAll, Tags: secretSet(0)}, Do: Reject{}},
				{Name: "accept", Do: Accept{}},
			},
		},
	}}
	if err := ps.Validate(); err != nil {
		t.Fatal(err)
	}
	report := ps.CheckLeak(LeakSpec{
		Entry:         "export",
		SecretTag:     0,
		Neighbors:     []Neighbor{{Name: "peer"}},
		FullTagDomain: true,
		MaxPathLen:    1,
	})
	if report.Stats.TagSets != 1<<MaxTags {
		t.Fatalf("full domain should enumerate %d sets, got %d", 1<<MaxTags, report.Stats.TagSets)
	}
	if !report.Safe {
		t.Fatal("guard rejects every secret-carrying set")
	}
}

func TestFiniteDomainCutPoints(t *testing.T) {
	ranges := []PrefixRange{
		{Network: MustParsePrefix("10.0.0.0/8"), MinLen: 16, MaxLen: 24},
	}
	prefixes, family := enumeratePrefixes(ranges)
	if family != "ipv4" || len(prefixes) < 4 {
		t.Fatalf("unexpected prefix domain: family=%s n=%d", family, len(prefixes))
	}
	pathLens := enumeratePathLens([]PathLenRange{{Min: 2, Max: 5}}, 8)
	// Atomic classes: [0,1], [2,5], [6,8]; one representative each.
	wantPath := []int{0, 2, 6}
	if len(pathLens) != len(wantPath) {
		t.Fatalf("path lens = %v, want %v", pathLens, wantPath)
	}
	for i := range wantPath {
		if pathLens[i] != wantPath[i] {
			t.Fatalf("path lens = %v, want %v", pathLens, wantPath)
		}
	}

	// Every representative must be a concrete, parseable CIDR.
	for _, p := range prefixes {
		if !p.Valid() {
			t.Fatalf("invalid representative prefix in domain")
		}
	}
}

func TestMultipleNeighborsMinimalPick(t *testing.T) {
	ps := leakPolicySet(t)
	report := ps.CheckLeak(LeakSpec{
		Entry:     "export",
		SecretTag: 0,
		Neighbors: []Neighbor{
			{Name: "peer-A"},
			{Name: "peer-B"},
		},
		MaxPathLen: 3,
	})
	if report.Safe || report.Leak == nil {
		t.Fatalf("expected leak across neighbors: %+v", report.Errors)
	}
	if report.Leak.Neighbor.Name != "peer-A" {
		t.Fatalf("minimal witness should keep deterministic neighbor order, got %q", report.Leak.Neighbor.Name)
	}
}
