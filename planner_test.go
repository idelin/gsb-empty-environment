package netsec

import (
	"errors"
	"testing"
)

func TestSharedKeyCapacitySharing(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "signing", Capacity: 3},
			{ID: "r2", KeyPurpose: "signing", Capacity: 3},
			{ID: "r3", KeyPurpose: "signing", Capacity: 5},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"signing": 6}},
			{ID: "d2", Purposes: map[string]int{"signing": 5}},
		},
		Cost: [][]int{
			{1, 2},
			{1, 2},
			{1, 2},
		},
	}

	plan, cs, err := Solve(in)
	if err != nil {
		t.Fatalf("unexpected error: %v (conflict=%+v)", err, cs)
	}
	if plan.EnabledCount != 2 {
		t.Fatalf("expected both domains enabled, got %d: %+v", plan.EnabledCount, plan)
	}
	// r1+r2 consume the full 6-unit pool, so r3 must land on d2.
	if plan.Assignment[2] != 1 {
		t.Fatalf("r3 must use d2, assignment=%v", plan.Assignment)
	}
	if plan.Assignment[0] != 0 || plan.Assignment[1] != 0 {
		t.Fatalf("r1 and r2 should share d1, assignment=%v", plan.Assignment)
	}
	if plan.TotalCost != 1+1+2 {
		t.Fatalf("unexpected total cost: %d", plan.TotalCost)
	}
}

func TestSharedKeyPurposeInfeasible(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "hsm-aes", Capacity: 2},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"hsm-rsa": 10}},
		},
		Cost: [][]int{{5}},
	}

	_, cs, err := Solve(in)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
	if len(cs.RequestSubset) != 1 || cs.RequestSubset[0] != "r1" {
		t.Fatalf("expected minimal subset [r1], got %v", cs.RequestSubset)
	}
	if len(cs.Reasons) != 1 || cs.Reasons[0].Kind != "unsupported_purpose" {
		t.Fatalf("expected unsupported_purpose reason, got %+v", cs.Reasons)
	}
}

func TestApprovalRoleSeparation(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", TenantID: "t1", Sensitive: true, ApprovalRole: "initiator", KeyPurpose: "k", Capacity: 1},
			{ID: "r2", TenantID: "t1", Sensitive: true, ApprovalRole: "approver", KeyPurpose: "k", Capacity: 1},
			{ID: "r3", TenantID: "t1", Sensitive: true, ApprovalRole: "auditor", KeyPurpose: "k", Capacity: 1},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"k": 10}},
			{ID: "d2", Purposes: map[string]int{"k": 10}},
		},
		Cost: [][]int{{1, 2}, {1, 2}, {1, 2}},
	}

	_, cs, err := Solve(in)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
	if len(cs.RequestSubset) != 3 {
		t.Fatalf("expected all three requests in minimal subset, got %v", cs.RequestSubset)
	}
	if len(cs.PairConflicts) != 3 {
		t.Fatalf("expected three approval_role pair conflicts, got %+v", cs.PairConflicts)
	}
	for _, pc := range cs.PairConflicts {
		if pc.Source != "approval_role" {
			t.Fatalf("expected approval_role source, got %q", pc.Source)
		}
	}

	in.Domains = append(in.Domains, Domain{ID: "d3", Purposes: map[string]int{"k": 10}})
	for i := range in.Cost {
		in.Cost[i] = append(in.Cost[i], 3)
	}
	plan, _, err := Solve(in)
	if err != nil {
		t.Fatalf("expected feasible with three domains, got %v", err)
	}
	if plan.EnabledCount != 3 {
		t.Fatalf("expected three enabled domains, got %d", plan.EnabledCount)
	}
}

func TestExplicitMustSeparate(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "k", Capacity: 1},
			{ID: "r2", KeyPurpose: "k", Capacity: 1},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"k": 10}},
		},
		MustSeparate: []Pair{{A: "r1", B: "r2"}},
		Cost:         [][]int{{1}, {1}},
	}

	_, cs, err := Solve(in)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
	if len(cs.PairConflicts) != 1 || cs.PairConflicts[0].Source != "explicit" {
		t.Fatalf("expected one explicit pair conflict, got %+v", cs.PairConflicts)
	}
}

func TestMinimumDomainsThenCost(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "k", Capacity: 1},
			{ID: "r2", KeyPurpose: "k", Capacity: 1},
			{ID: "r3", KeyPurpose: "k", Capacity: 1},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"k": 10}},
			{ID: "d2", Purposes: map[string]int{"k": 10}},
		},
		// d1 is cheapest per request, but r3 is expensive on both. The
		// primary objective still forces a single enabled domain.
		Cost: [][]int{{1, 2}, {1, 2}, {100, 200}},
	}

	plan, _, err := Solve(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.EnabledCount != 1 {
		t.Fatalf("expected a single enabled domain, got %d", plan.EnabledCount)
	}
	expectedDomain := 0
	for _, d := range plan.Assignment {
		if d != expectedDomain {
			t.Fatalf("all requests must share one domain, got %v", plan.Assignment)
		}
	}
	if plan.TotalCost != 1+1+100 {
		t.Fatalf("expected cost 102, got %d", plan.TotalCost)
	}
}

func TestSingleRequestExceedsEveryDomain(t *testing.T) {
	in := Input{
		Requests: []Request{{ID: "r1", KeyPurpose: "k", Capacity: 9}},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"k": 4}},
			{ID: "d2", Purposes: map[string]int{"k": 4}},
		},
		Cost: [][]int{{1, 1}},
	}

	_, cs, err := Solve(in)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
	found := false
	for _, reason := range cs.Reasons {
		if reason.Kind == "single_request_capacity" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected single_request_capacity reason, got %+v", cs.Reasons)
	}
}

func TestValidationErrors(t *testing.T) {
	base := Input{
		Requests: []Request{{ID: "r1", KeyPurpose: "k", Capacity: 1}},
		Domains:  []Domain{{ID: "d1", Purposes: map[string]int{"k": 1}}},
		Cost:     [][]int{{1}},
	}

	cases := []struct {
		name   string
		mutate func(*Input)
	}{
		{"zero capacity", func(in *Input) { in.Requests[0].Capacity = 0 }},
		{"empty request id", func(in *Input) { in.Requests[0].ID = "" }},
		{"empty purpose", func(in *Input) { in.Requests[0].KeyPurpose = "" }},
		{"duplicate request", func(in *Input) {
			in.Requests = append(in.Requests, Request{ID: "r1", KeyPurpose: "k", Capacity: 1})
			in.Cost = append(in.Cost, []int{1})
		}},
		{"too many requests", func(in *Input) {
			for i := len(in.Requests); i <= MaxRequests; i++ {
				in.Requests = append(in.Requests,
					Request{ID: "r" + string(rune('a'+i)), KeyPurpose: "k", Capacity: 1})
				in.Cost = append(in.Cost, []int{1})
			}
		}},
		{"too many domains", func(in *Input) {
			for d := len(in.Domains); d <= MaxDomains; d++ {
				in.Domains = append(in.Domains,
					Domain{ID: "dx", Purposes: map[string]int{"k": 1}})
				for i := range in.Cost {
					in.Cost[i] = append(in.Cost[i], 1)
				}
			}
		}},
		{"empty domain id", func(in *Input) { in.Domains[0].ID = "" }},
		{"duplicate domain", func(in *Input) {
			in.Domains = append(in.Domains, Domain{ID: "d1", Purposes: map[string]int{"k": 1}})
			for i := range in.Cost {
				in.Cost[i] = append(in.Cost[i], 1)
			}
		}},
		{"negative capacity", func(in *Input) { in.Domains[0].Purposes["k"] = -1 }},
		{"bad cost width", func(in *Input) { in.Cost = [][]int{{1, 2}} }},
		{"negative cost", func(in *Input) { in.Cost[0][0] = -1 }},
		{"unknown separate request", func(in *Input) {
			in.MustSeparate = []Pair{{A: "r1", B: "ghost"}}
		}},
		{"self separation", func(in *Input) {
			in.MustSeparate = []Pair{{A: "r1", B: "r1"}}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.Requests = append([]Request(nil), base.Requests...)
			in.Domains = append([]Domain(nil), base.Domains...)
			in.Cost = append([][]int(nil), base.Cost...)
			tc.mutate(&in)
			if _, _, err := Solve(in); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}

	var empty Input
	if _, _, err := Solve(empty); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestPurposeCapacityConflictReason(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "k", Capacity: 4},
			{ID: "r2", KeyPurpose: "k", Capacity: 4},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"k": 5}},
			{ID: "d2", Purposes: map[string]int{"k": 1}},
		},
		Cost: [][]int{{1, 1}, {1, 1}},
	}

	_, cs, err := Solve(in)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
	found := false
	for _, reason := range cs.Reasons {
		if reason.Kind == "purpose_capacity" &&
			reason.Required == 8 && reason.Available == 6 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected purpose_capacity reason, got %+v", cs.Reasons)
	}
}
