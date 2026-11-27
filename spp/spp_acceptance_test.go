package spp

import (
	"testing"
)

// badGadget builds the canonical 3-AS Bad Gadget (Griffin-Wilfong):
// origin 0, pivots 1,2,3 with unrestricted triangle edges and cyclic
// "route via the next pivot" preferences. No Stable Path Assignment exists.
func badGadget(t *testing.T) *Instance {
	t.Helper()
	g, err := NewASGraph([]int{0, 1, 2, 3}, []Link{
		{A: 0, B: 1, Relation: RelationUnknown},
		{A: 0, B: 2, Relation: RelationUnknown},
		{A: 0, B: 3, Relation: RelationUnknown},
		{A: 1, B: 2, Relation: RelationUnknown},
		{A: 2, B: 3, Relation: RelationUnknown},
		{A: 3, B: 1, Relation: RelationUnknown},
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	inst, err := Build(g, PolicyConfig{
		Origin:       0,
		ExplicitOnly: true,
		ExtraPaths: map[int][][]int{
			1: {{1, 3, 0}, {1, 0}},
			2: {{2, 1, 0}, {2, 0}},
			3: {{3, 2, 0}, {3, 0}},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return inst
}

func TestBadGadgetNoStableState(t *testing.T) {
	inst := badGadget(t)
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Outcome != OutcomeNoStable || len(res.Solutions) != 0 {
		t.Fatalf("expected no stable assignment, got %s (%d solutions)", res.Outcome, len(res.Solutions))
	}
	wheels := inst.DetectWheels(8)
	if len(wheels) == 0 {
		t.Fatal("expected a dispute wheel, got none")
	}
	w := wheels[0]
	if len(w.Pivots) != 3 {
		t.Fatalf("expected 3-pivot wheel, got %d", len(w.Pivots))
	}
	for i, pr := range w.Proof {
		if pr.BetterRank >= pr.WorseRank {
			t.Fatalf("wheel step %d not strictly preferred: %+v", i, pr)
		}
	}
}

func TestBadGadgetMinimumRepair(t *testing.T) {
	inst := badGadget(t)
	plans, err := inst.MinimumRepair(RepairOptions{AllowEdgeDisable: true})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(plans) == 0 || len(plans[0].Actions) != 1 {
		t.Fatalf("expected minimum repair of size 1, got %d plans", len(plans))
	}
	plan := plans[0]
	if plan.Verify.Outcome != OutcomeUnique {
		t.Fatalf("repaired instance not unique-stable: %s", plan.Verify.Outcome)
	}
	if len(plan.RepairedInstance.DetectWheels(8)) != 0 {
		t.Fatal("repaired instance still contains a dispute wheel")
	}
	if err := plan.RepairedInstance.IsStable(plan.Verify.Solutions[0]); err != nil {
		t.Fatalf("repaired SPA failed stability check: %v", err)
	}
}

// disagree4 builds a 4-AS DISAGREE gadget (two pivots 1,2 plus customer 3):
// each pivot prefers the route through the other over its direct route.
func disagree4(t *testing.T) *Instance {
	t.Helper()
	g, err := NewASGraph([]int{0, 1, 2, 3}, []Link{
		{A: 0, B: 1, Relation: RelationUnknown},
		{A: 0, B: 2, Relation: RelationUnknown},
		{A: 1, B: 2, Relation: RelationUnknown},
		{A: 3, B: 1, Relation: RelationUnknown},
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	inst, err := Build(g, PolicyConfig{
		Origin:       0,
		ExplicitOnly: true,
		ExtraPaths: map[int][][]int{
			1: {{1, 2, 0}, {1, 0}},
			2: {{2, 1, 0}, {2, 0}},
			3: {{3, 1, 0}},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return inst
}

func TestDisagree4TwoStableStates(t *testing.T) {
	inst := disagree4(t)
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Outcome != OutcomeMultiple || len(res.Solutions) < 2 {
		t.Fatalf("expected >=2 stable assignments, got %s", res.Outcome)
	}
	// The two SPAs must be mutually exclusive on at least one pivot.
	s0, s1 := res.Solutions[0], res.Solutions[1]
	differ := false
	for as, p := range s0 {
		if !pathEquals(p, s1[as]) {
			differ = true
		}
	}
	if !differ {
		t.Fatal("two reported SPAs are identical, not mutually exclusive")
	}
	for _, sol := range res.Solutions {
		if err := inst.IsStable(sol); err != nil {
			t.Fatalf("reported SPA unstable: %v", err)
		}
	}
	wheels := inst.DetectWheels(8)
	if len(wheels) == 0 || len(wheels[0].Pivots) != 2 {
		t.Fatalf("expected a 2-pivot dispute wheel, got %+v", wheels)
	}
}

// valleyFreeNetwork is a clean Gao-Rexford topology:
// origin 4 at the top; transit providers 1 and 2 are both customers of 4 and
// peer laterally; dual-homed stub customer 3 sits below both providers. Every
// retained path has the up* (peer?) down* shape and every export obeys the
// customer > peer > provider filtering hierarchy.
func valleyFreeNetwork(t *testing.T, strict bool, peerBackup bool) *Instance {
	t.Helper()
	links := []Link{
		// Clean customer->provider hierarchy rooted at origin 4:
		// 1 and 2 are customers of 4; 3 is a customer of 1. Every enumerated
		// simple path is then strictly up* and trivially valley-free.
		{A: 1, B: 4, Relation: RelationCustomerToProvider},
		{A: 2, B: 4, Relation: RelationCustomerToProvider},
		{A: 3, B: 1, Relation: RelationCustomerToProvider},
	}
	if peerBackup {
		// A lateral backup peering plus a second attachment for stub 3. The
		// backup routes stay within Gao-Rexford export rules, while the
		// local-pref overrides below break preference monotonicity.
		links = append(links,
			Link{A: 1, B: 2, Relation: RelationPeer},
			Link{A: 3, B: 2, Relation: RelationCustomerToProvider},
		)
	}
	g, err := NewASGraph([]int{1, 2, 3, 4}, links)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	cfg := PolicyConfig{Origin: 4, StrictGR: strict}
	if peerBackup {
		// The stub customer is given a high local-pref for its backup route
		// via provider 2, and provider 2 is given a high local-pref for the
		// route learned across the peering. The engineered routes remain
		// export-legal but the preference monotonicity breaks, exposing a
		// hidden dispute wheel.
		cfg.TierFor = map[int]map[string]LocalPrefTier{
			3: {"[3 2 4]": PrefCustomer},
			2: {"[2 1 4]": PrefCustomer},
		}
	}
	inst, err := Build(g, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return inst
}

func TestValleyFreeNetworkUniqueStable(t *testing.T) {
	inst := valleyFreeNetwork(t, true, false)
	if !inst.Report.IsClean() {
		t.Fatalf("expected clean GR validation, got %+v", inst.Report.Violations)
	}
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Outcome != OutcomeUnique {
		t.Fatalf("expected unique stable assignment, got %s", res.Outcome)
	}
	analysis := Analyze(inst, SolveOptions{}, 8)
	if analysis.ConvergenceProof == nil {
		t.Fatal("expected a lock-in convergence certificate")
	}
	if len(analysis.Wheels) != 0 {
		t.Fatalf("clean GR network must have no dispute wheel, got %d", len(analysis.Wheels))
	}
}

func TestBackupLinkBreaksValleyFreeAndTriggersWheel(t *testing.T) {
	// Strict mode flags the engineered preferences; relaxed mode retains them
	// and exposes the hidden dispute wheel / multi-stability.
	strict := valleyFreeNetwork(t, true, true)
	if strict.Report.IsClean() {
		t.Fatal("strict GR audit must flag the backup local-pref violation")
	}
	inst := valleyFreeNetwork(t, false, true)
	wheels := inst.DetectWheels(8)
	if len(wheels) == 0 {
		t.Fatal("expected a hidden dispute wheel after backup preference engineering")
	}
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Outcome == OutcomeUnique {
		t.Fatalf("expected non-unique outcome, got %s", res.Outcome)
	}
	plans, err := inst.MinimumRepair(RepairOptions{AllowEdgeDisable: true})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	for _, plan := range plans {
		if plan.Verify.Outcome != OutcomeUnique || !plan.ReachabilityKept {
			t.Fatal("minimum repair did not restore unique reachable stable state")
		}
	}
}

func TestCommercialCycleDetected(t *testing.T) {
	g, err := NewASGraph([]int{1, 2, 3, 4}, []Link{
		{A: 1, B: 2, Relation: RelationCustomerToProvider},
		{A: 2, B: 3, Relation: RelationCustomerToProvider},
		{A: 3, B: 1, Relation: RelationCustomerToProvider}, // 3 claims to be customer of 1
		{A: 4, B: 1, Relation: RelationCustomerToProvider},
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	inst, err := Build(g, PolicyConfig{Origin: 4, StrictGR: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if inst.Report.CommercialCycleFree {
		t.Fatal("expected customer-provider commercial cycle to be detected")
	}
	if len(inst.Report.CommercialCycle) < 3 {
		t.Fatalf("bad commercial cycle report: %v", inst.Report.CommercialCycle)
	}
}

func TestCoreEdgesProtectedFromRepair(t *testing.T) {
	inst := valleyFreeNetwork(t, true, false)
	// Every link in the strict customer-provider tree is core: removing any
	// disconnects at least one AS from the prefix via a valley-free route.
	for _, link := range inst.sortedLinks() {
		if !inst.isCoreEdge(link) {
			t.Fatalf("tree link %d-%d must be treated as core", link.A, link.B)
		}
	}
	// Therefore edge-disable actions must be empty; only preference edits
	// remain available, and on a unique-stable network no repair is needed.
	actions := inst.candidateActions(true)
	for _, a := range actions {
		if _, isEdge := a.(EdgeDisable); isEdge {
			t.Fatalf("core edge action generated: %s", a.Describe())
		}
	}
}
