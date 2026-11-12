package taskpkg

import (
	"errors"
	"fmt"
	"testing"
)

func v(name string, pairs ...string) Version {
	if len(pairs)%2 != 0 {
		panic("edge pairs must be even")
	}
	edges := make([]Edge, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		edges = append(edges, Edge{From: pairs[i], To: pairs[i+1]})
	}
	return Version{Name: name, Edges: edges}
}

func TestReorderedReceiptLeavesAttackPath(t *testing.T) {
	req := Request{
		Regions: []Region{
			{
				ID:             "entry",
				CurrentVersion: "open",
				TargetVersion:  "closed",
				Versions: []Version{
					v("open", "attacker", "conduit"),
					v("closed"),
				},
				CanConfirm: true,
			},
			{
				ID:             "core",
				CurrentVersion: "closed",
				TargetVersion:  "open",
				Versions: []Version{
					v("closed"),
					v("open", "conduit", "secret"),
				},
				CanConfirm: true,
			},
		},
		FixedEdges:      []Edge{{From: "admin", To: "secret"}},
		AttackEntries:   []string{"attacker"},
		Secrets:         []string{"secret"},
		ManagementPaths: []ManagementPath{{From: "admin", To: "secret"}},
	}

	plan, err := Plan(req)
	if !errors.Is(err, ErrNoSafeOrder) {
		t.Fatalf("Plan error = %v, want ErrNoSafeOrder", err)
	}
	if plan != nil {
		t.Fatalf("Plan returned %+v; no first step may be published", plan)
	}

	// If the controller truly waits for each receipt and cannot lose a region,
	// entry -> core is the unique safe two-step order.
	req.AssumeOrderedReceipts = true
	req.AssumeNoRegionLoss = true
	plan, err = Plan(req)
	if err != nil {
		t.Fatalf("Plan with ordered receipts: %v", err)
	}
	if plan.MinimumSteps != 2 || len(plan.Steps) != 2 {
		t.Fatalf("MinimumSteps = %d, steps = %+v, want 2", plan.MinimumSteps, plan.Steps)
	}
	if plan.Steps[0].Region != "entry" || plan.Steps[1].Region != "core" {
		t.Fatalf("order = %+v, want entry then core", plan.Steps)
	}
	if err := ValidatePlan(req, plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
}

func TestUnconfirmedRegionIsCheckedInBothStates(t *testing.T) {
	req := Request{
		Regions: []Region{{
			ID:             "gateway",
			CurrentVersion: "open",
			TargetVersion:  "closed",
			Versions: []Version{
				v("open", "attacker", "conduit"),
				v("closed"),
			},
			CanConfirm: false,
		}},
		FixedEdges:            []Edge{{From: "conduit", To: "secret"}, {From: "admin", To: "secret"}},
		AttackEntries:         []string{"attacker"},
		Secrets:               []string{"secret"},
		ManagementPaths:       []ManagementPath{{From: "admin", To: "secret"}},
		AssumeOrderedReceipts: true,
		AssumeNoRegionLoss:    true,
	}

	plan, err := Plan(req)
	if !errors.Is(err, ErrNoSafeOrder) {
		t.Fatalf("Plan error = %v, want ErrNoSafeOrder for unconfirmed old state", err)
	}
	if plan != nil {
		t.Fatalf("Plan returned %+v; unconfirmed region must not publish a first step", plan)
	}

	req.Regions[0].CanConfirm = true
	plan, err = Plan(req)
	if err != nil {
		t.Fatalf("Plan with confirmation capability: %v", err)
	}
	if plan.MinimumSteps != 1 || plan.Steps[0].Region != "gateway" {
		t.Fatalf("plan = %+v, want one gateway step", plan)
	}
}

func TestValidatePlanRejectsUnsafeOrder(t *testing.T) {
	req := Request{
		Regions: []Region{
			{
				ID:             "entry",
				CurrentVersion: "open",
				TargetVersion:  "closed",
				Versions:       []Version{v("open", "attacker", "conduit"), v("closed")},
				CanConfirm:     true,
			},
			{
				ID:             "core",
				CurrentVersion: "closed",
				TargetVersion:  "open",
				Versions:       []Version{v("closed"), v("open", "conduit", "secret")},
				CanConfirm:     true,
			},
		},
		FixedEdges:            []Edge{{From: "admin", To: "secret"}},
		AttackEntries:         []string{"attacker"},
		Secrets:               []string{"secret"},
		ManagementPaths:       []ManagementPath{{From: "admin", To: "secret"}},
		AssumeOrderedReceipts: true,
		AssumeNoRegionLoss:    true,
	}
	plan := &UpdatePlan{Steps: []Step{
		{Region: "core", FromVersion: "closed", ToVersion: "open"},
		{Region: "entry", FromVersion: "open", ToVersion: "closed"},
	}}

	err := ValidatePlan(req, plan)
	if !errors.Is(err, ErrNoSafeOrder) {
		t.Fatalf("ValidatePlan error = %v, want ErrNoSafeOrder", err)
	}
}

func TestLostRequiredRegionRejectsPlan(t *testing.T) {
	req := Request{
		Regions: []Region{{
			ID:             "gateway",
			CurrentVersion: "v1",
			TargetVersion:  "v2",
			Versions:       []Version{v("v1"), v("v2")},
			CanConfirm:     true,
		}},
		LostRegions: []string{"gateway"},
	}

	plan, err := Plan(req)
	if !errors.Is(err, ErrNoSafeOrder) {
		t.Fatalf("Plan error = %v, want ErrNoSafeOrder", err)
	}
	if plan != nil {
		t.Fatalf("Plan returned %+v for a lost required region", plan)
	}
}

func TestNoSafeOrderWhenOnlyFinalStateIsSafe(t *testing.T) {
	req := Request{
		Regions: []Region{
			{
				ID:             "path-c",
				CurrentVersion: "open",
				TargetVersion:  "closed",
				Versions:       []Version{v("open", "attacker", "c"), v("closed")},
				CanConfirm:     true,
			},
			{
				ID:             "path-d",
				CurrentVersion: "open",
				TargetVersion:  "closed",
				Versions:       []Version{v("open", "attacker", "d"), v("closed")},
				CanConfirm:     true,
			},
		},
		FixedEdges: []Edge{
			{From: "c", To: "secret"},
			{From: "d", To: "secret"},
			{From: "admin", To: "secret"},
		},
		AttackEntries:         []string{"attacker"},
		Secrets:               []string{"secret"},
		ManagementPaths:       []ManagementPath{{From: "admin", To: "secret"}},
		AssumeOrderedReceipts: true,
		AssumeNoRegionLoss:    true,
	}

	plan, err := Plan(req)
	if !errors.Is(err, ErrNoSafeOrder) {
		t.Fatalf("Plan error = %v, want ErrNoSafeOrder", err)
	}
	if plan != nil {
		t.Fatalf("Plan returned %+v although every first step is unsafe", plan)
	}
}

func TestManagementPathMustBePreserved(t *testing.T) {
	req := Request{
		Regions: []Region{{
			ID:             "control",
			CurrentVersion: "routed",
			TargetVersion:  "broken",
			Versions: []Version{
				v("routed", "admin", "jump"),
				v("broken"),
			},
			CanConfirm: true,
		}},
		FixedEdges:      []Edge{{From: "jump", To: "secret"}},
		Secrets:         []string{"secret"},
		ManagementPaths: []ManagementPath{{From: "admin", To: "secret"}},
	}

	plan, err := Plan(req)
	if !errors.Is(err, ErrNoSafeOrder) {
		t.Fatalf("Plan error = %v, want ErrNoSafeOrder", err)
	}
	if plan != nil {
		t.Fatalf("Plan returned %+v with a broken management path", plan)
	}
}

func TestMutableEdgeLimit(t *testing.T) {
	edges := make([]Edge, 0, 13)
	for i := 0; i < 13; i++ {
		edges = append(edges, Edge{From: "attacker", To: fmt.Sprintf("n%d", i)})
	}
	req := Request{
		Regions: []Region{{
			ID:             "large",
			CurrentVersion: "v1",
			TargetVersion:  "v2",
			Versions: []Version{
				{Name: "v1", Edges: edges},
				v("v2"),
			},
			CanConfirm: true,
		}},
		AttackEntries: []string{"attacker"},
		Secrets:       []string{"secret"},
	}

	plan, err := Plan(req)
	if !errors.Is(err, ErrMutableEdgeLimit) {
		t.Fatalf("Plan error = %v, want ErrMutableEdgeLimit", err)
	}
	if plan != nil {
		t.Fatalf("Plan returned %+v beyond the mutable edge limit", plan)
	}
}

func TestTwelveMutableEdgesAreAllowed(t *testing.T) {
	edges := make([]Edge, 0, 12)
	for i := 0; i < 12; i++ {
		edges = append(edges, Edge{From: "attacker", To: fmt.Sprintf("n%d", i)})
	}
	req := Request{
		Regions: []Region{{
			ID:             "twelve",
			CurrentVersion: "v1",
			TargetVersion:  "v2",
			Versions: []Version{
				{Name: "v1", Edges: edges},
				v("v2"),
			},
			CanConfirm: true,
		}},
		AttackEntries: []string{"attacker"},
		Secrets:       []string{"secret"},
	}

	plan, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.MinimumSteps != 1 {
		t.Fatalf("MinimumSteps = %d, want 1", plan.MinimumSteps)
	}
}
