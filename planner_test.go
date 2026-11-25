package upgradeplan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAliasSignersCountOnePublicKeyVote(t *testing.T) {
	aliases := SignerSet{
		AliasSigner("emergency-cert-a", "shared-emergency-key"),
		AliasSigner("emergency-cert-b", "shared-emergency-key"),
	}
	if got := aliases.DistinctKeyCount(); got != 1 {
		t.Fatalf("DistinctKeyCount() = %d, want 1", got)
	}

	device := []DeviceState{{Package: "app", SDK: 30, Signers: aliases, HasData: true}}

	tooStrict := Release{ID: "two-key-threshold", OldThreshold: 2}
	if got := EligibleIndices(device, tooStrict); len(got) != 0 {
		t.Fatalf("alias set incorrectly satisfied two-key threshold: %v", got)
	}

	oneKey := Release{ID: "one-key-threshold", OldThreshold: 1}
	if got := EligibleIndices(device, oneKey); len(got) != 1 {
		t.Fatalf("alias set should satisfy one-key threshold, got %v", got)
	}

	byAliasID := Release{
		ID:           "named-old-id",
		OldThreshold: 1,
		OldSigners:   SignerSet{{ID: "emergency-cert-a"}},
	}
	if got := OldKeyVoteCount(aliases, byAliasID); got != 1 {
		t.Fatalf("OldKeyVoteCount() by alias ID = %d, want 1 distinct key", got)
	}
	if got := EligibleIndices(device, byAliasID); len(got) != 1 {
		t.Fatalf("named alias authorization should match shared key, got %v", got)
	}

	newAliasRelease := Release{
		ID:           "choose-aliased-new-set",
		OldThreshold: 0,
		NewSigners: SignerSet{
			AliasSigner("new-id-1", "NEW-SHARED"),
			AliasSigner("new-id-2", "NEW-SHARED"),
		},
	}
	outcomes, err := EnumerateOutcomes([]DeviceState{{SDK: 1}}, newAliasRelease)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Fleet[0].Signers.DistinctKeyCount() != 1 {
		t.Fatalf("new aliased set should contain one distinct key vote: %+v", outcomes)
	}
	if got := EligibleIndices([]DeviceState{{Signers: outcomes[0].Fleet[0].Signers}}, Release{OldThreshold: 2}); len(got) != 0 {
		t.Fatalf("new-set aliases incorrectly counted as two threshold votes")
	}
}

func TestGoldenCountBranchingDecisionTree(t *testing.T) {
	trusted := KeySignerSet("TRUSTED")
	problem := Problem{
		Package: "app",
		InitialFleets: [][]DeviceState{
			{{SDK: 24, Signers: KeySignerSet("OLD"), HasData: true}},
			{{SDK: 30, Signers: KeySignerSet("OLD"), HasData: true}},
		},
		Releases: []Release{
			{
				ID: "r-probe", SDKRange: SDKRange{MinSDK: 28, MaxSDK: 32},
				OldThreshold: 1, NewSigners: KeySignerSet("PROBED"), CarryExistingData: true,
			},
			{
				ID: "r-low", SDKRange: SDKRange{MinSDK: 20, MaxSDK: 27},
				OldSignerKeys: []string{"OLD"}, OldThreshold: 1,
				NewSigners: trusted, CarryExistingData: true,
			},
			{
				ID: "r-high", SDKRange: SDKRange{MinSDK: 28, MaxSDK: 32},
				OldSignerKeys: []string{"PROBED"}, OldThreshold: 1,
				NewSigners: trusted, CarryExistingData: true,
			},
		},
		Target: Target{Trusted: trusted},
	}

	plan, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if plan.WorstRounds != 2 {
		t.Fatalf("WorstRounds = %d, want 2", plan.WorstRounds)
	}

	const wantTree = `worst_rounds=2
broadcast r-probe
  count=0 ->
    broadcast r-low
      count=1 ->
        terminal
  count=1 ->
    broadcast r-high
      count=1 ->
        terminal
`
	if got := plan.TreeText(); got != wantTree {
		t.Fatalf("decision tree mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantTree)
	}

	report, err := VerifyPlan(problem, plan)
	if err != nil {
		t.Fatalf("VerifyPlan() error = %v", err)
	}
	if !report.Accepted || report.TrajectoriesChecked != 2 {
		t.Fatalf("acceptance = %+v, want accepted with 2 trajectories", report)
	}

	session, err := NewSession(plan)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := session.Current()
	if !ok || first.ReleaseID != "r-probe" {
		t.Fatalf("Current() = %+v,%v", first, ok)
	}
	next, err := session.Observe(0)
	if err != nil || next == nil || next.ReleaseID != "r-low" {
		t.Fatalf("Observe(0) = %+v,%v", next, err)
	}
	if _, err := session.Observe(1); err != nil {
		t.Fatalf("Observe(1) terminal error = %v", err)
	}
}

func TestSameCountBranchesKeepDifferentSignaturesTogether(t *testing.T) {
	trusted := KeySignerSet("TRUSTED")
	fleet := []DeviceState{
		{SDK: 30, Signers: KeySignerSet("SIG-A"), HasData: true},
		{SDK: 30, Signers: KeySignerSet("SIG-B"), HasData: true},
	}
	choose := Release{
		ID:           "r-choose",
		SDKRange:     SDKRange{MinSDK: 28, MaxSDK: 32},
		OldThreshold: 0,
		NewSignerSets: []SignerSet{
			KeySignerSet("NEW-U"),
			KeySignerSet("NEW-V"),
		},
		CarryExistingData: true,
	}
	finish := Release{
		ID:                "r-finish",
		SDKRange:          SDKRange{MinSDK: 28, MaxSDK: 32},
		OldThreshold:      1,
		OldSignerKeys:     []string{"NEW-U", "NEW-V"},
		NewSigners:        trusted,
		CarryExistingData: true,
	}
	problem := Problem{
		Devices:  fleet,
		Releases: []Release{choose, finish},
		Target:   Target{Trusted: trusted},
	}

	children := transitionBelief(NewBelief(fleet), choose)
	if len(children) != 1 || children[2] == nil {
		t.Fatalf("broadcast should have only visible count 2, got %#v", children)
	}
	if len(children[2].Worlds) != 3 {
		t.Fatalf("same count should contain UU, UV/VU, VV worlds, got %d", len(children[2].Worlds))
	}

	plan, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	const wantTree = `worst_rounds=2
broadcast r-choose
  count=2 ->
    broadcast r-finish
      count=2 ->
        terminal
`
	if got := plan.TreeText(); got != wantTree {
		t.Fatalf("tree mismatch\n%s", got)
	}

	report, err := VerifyPlan(problem, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accepted {
		t.Fatalf("plan not accepted: %+v", report.Violations)
	}
	if report.TrajectoriesChecked != 3 {
		t.Fatalf("TrajectoriesChecked = %d, want UU/UV/VV canonical paths", report.TrajectoriesChecked)
	}
	if report.AssignmentsChecked < 4 {
		t.Fatalf("AssignmentsChecked = %d, want all four first-round labeled choices", report.AssignmentsChecked)
	}

	reversed := Problem{Devices: []DeviceState{fleet[1], fleet[0]}, Releases: problem.Releases, Target: problem.Target}
	reversedPlan, err := Solve(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TreeText() != reversedPlan.TreeText() {
		t.Fatalf("device order changed decision tree:\n%s\nvs\n%s", plan.TreeText(), reversedPlan.TreeText())
	}
	planJSON, _ := json.Marshal(plan)
	reversedJSON, _ := json.Marshal(reversedPlan)
	if string(planJSON) != string(reversedJSON) {
		t.Fatalf("plan JSON differs for reordered fleet")
	}
}

func TestBeliefKeyIgnoresWorldAndDeviceOrder(t *testing.T) {
	a := DeviceState{SDK: 30, Signers: KeySignerSet("A")}
	b := DeviceState{SDK: 31, Signers: KeySignerSet("B")}
	first := CanonicalBeliefKey([][]DeviceState{{a, b}, {b}})
	second := CanonicalBeliefKey([][]DeviceState{{b}, {b, a}})
	if first != second {
		t.Fatalf("belief keys differ under reordering:\n%s\n%s", first, second)
	}
}

func TestInitialStateUniverseCoversEveryMultiset(t *testing.T) {
	states := []DeviceState{
		{SDK: 24, Signers: KeySignerSet("OLD"), HasData: true},
		{SDK: 30, Signers: KeySignerSet("OLD"), HasData: true},
	}
	fleets := AllMultisetFleets(states, 2)
	if len(fleets) != 3 {
		t.Fatalf("AllMultisetFleets() = %d fleets, want 3", len(fleets))
	}

	trusted := KeySignerSet("T")
	problem := Problem{
		InitialStates: states,
		DeviceCount:   2,
		Releases: []Release{{
			ID: "r-universal", SDKRange: SDKRange{MinSDK: 20, MaxSDK: 32},
			OldThreshold: 1, NewSigners: trusted, CarryExistingData: true,
		}},
		Target: Target{Trusted: trusted},
	}
	plan, err := Solve(problem)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorstRounds != 1 || plan.CalculateWorstRounds() != 1 {
		t.Fatalf("rounds = declared %d/calculated %d, want 1", plan.WorstRounds, plan.CalculateWorstRounds())
	}
	report, err := VerifyPlan(problem, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accepted || report.InitialWorlds != 3 {
		t.Fatalf("report = %+v, want 3 accepted initial multisets", report)
	}
}

func TestAliasThresholdInfeasibleFailureBasis(t *testing.T) {
	problem := Problem{
		Devices: []DeviceState{{
			Signers: SignerSet{AliasSigner("a1", "ONLY-KEY"), AliasSigner("a2", "ONLY-KEY")},
			HasData: true,
		}},
		Releases: []Release{{
			ID: "requires-two-distinct-keys", OldThreshold: 2,
			NewSigners: KeySignerSet("T"), CarryExistingData: true,
		}},
		Target:    Target{Trusted: KeySignerSet("T")},
		MaxRounds: 1,
	}
	_, err := Solve(problem)
	var infeasible *InfeasibleError
	if !errors.As(err, &infeasible) {
		t.Fatalf("Solve() error = %v, want *InfeasibleError", err)
	}
	if len(infeasible.Failure.Candidates) != 1 {
		t.Fatalf("Candidates = %d, want 1", len(infeasible.Failure.Candidates))
	}
	candidate := infeasible.Failure.Candidates[0]
	if candidate.ReleaseID != "requires-two-distinct-keys" || len(candidate.Branches) != 1 {
		t.Fatalf("candidate failure = %+v", candidate)
	}
	if candidate.Branches[0].Count != 0 || candidate.Branches[0].Failure == nil {
		t.Fatalf("expected count-0 failure basis, got %+v", candidate.Branches[0])
	}
}

func TestInfeasibleCoversEveryCandidateBroadcast(t *testing.T) {
	trusted := KeySignerSet("T")
	problem := Problem{
		Devices: []DeviceState{{SDK: 30, Signers: KeySignerSet("OLD"), HasData: true}},
		Releases: []Release{
			{ID: "r-wipe", OldThreshold: 1, NewSigners: KeySignerSet("MID"), CarryExistingData: false},
			{ID: "r-finish-after-wipe", OldSignerKeys: []string{"MID"}, OldThreshold: 1, NewSigners: trusted, CarryExistingData: true},
		},
		Target:    Target{Trusted: trusted},
		MaxRounds: 2,
	}
	_, err := Solve(problem)
	var infeasible *InfeasibleError
	if !errors.As(err, &infeasible) {
		t.Fatalf("Solve() error = %v, want infeasible", err)
	}

	seen := map[string]CandidateFailure{}
	for _, candidate := range infeasible.Failure.Candidates {
		seen[candidate.ReleaseID] = candidate
	}
	if len(seen) != len(problem.Releases) {
		t.Fatalf("failure covers %d candidates, want %d: %+v", len(seen), len(problem.Releases), infeasible.Failure.Candidates)
	}

	wipe := seen["r-wipe"]
	if len(wipe.Branches) != 1 || wipe.Branches[0].Count != 1 {
		t.Fatalf("r-wipe failure branches = %+v", wipe.Branches)
	}
	if wipe.Branches[0].Failure == nil || wipe.Branches[0].Failure.Reason != ReasonDataLost {
		t.Fatalf("r-wipe should have sticky data-loss basis, got %+v", wipe.Branches[0].Failure)
	}

	finish := seen["r-finish-after-wipe"]
	if len(finish.Branches) == 0 || finish.Branches[0].Count != 0 || finish.Branches[0].Failure == nil {
		t.Fatalf("r-finish should fail on count 0, got %+v", finish)
	}
	if !strings.Contains(err.Error(), "r-wipe") || !strings.Contains(err.Error(), "r-finish-after-wipe") {
		t.Fatalf("error text does not name all candidates: %v", err)
	}
}

func TestAcceptanceRejectsDataLossThenLaterUpgrade(t *testing.T) {
	initial := Fleet{{SDK: 30, Signers: KeySignerSet("OLD"), HasData: true, InitialData: true}}
	lost := Fleet{{
		SDK: 30, Signers: KeySignerSet("MID"), HasData: false,
		InitialData: true, DataLost: true,
	}}
	finalTrusted := Fleet{{
		SDK: 30, Signers: KeySignerSet("T"), HasData: false,
		InitialData: true, DataLost: true,
	}}
	trajectory := Trajectory{
		Initial: initial,
		Steps: []TrajectoryStep{
			{Round: 1, Broadcast: Broadcast{ReleaseID: "r-wipe"}, SuccessCount: 1, Before: initial, After: lost},
			{Round: 2, Broadcast: Broadcast{ReleaseID: "r-finish"}, SuccessCount: 1, Before: lost, After: finalTrusted},
		},
		Final: finalTrusted,
	}
	err := AcceptTrajectory(trajectory, Target{Trusted: KeySignerSet("T")})
	if err == nil || !strings.Contains(err.Error(), "round 1") {
		t.Fatalf("AcceptTrajectory() error = %v, want round-1 data loss rejection", err)
	}

	badPlan := &Plan{
		WorstRounds: 2,
		Root: &PlanNode{
			Broadcast: &Broadcast{ReleaseID: "r-wipe"},
			Branches: map[int]*PlanNode{
				1: {
					Broadcast: &Broadcast{ReleaseID: "r-finish"},
					Branches: map[int]*PlanNode{
						1: {Terminal: true},
					},
				},
			},
		},
	}
	problem := Problem{
		Devices: []DeviceState{initial[0]},
		Releases: []Release{
			{ID: "r-wipe", OldThreshold: 1, NewSigners: KeySignerSet("MID"), CarryExistingData: false},
			{ID: "r-finish", OldSignerKeys: []string{"MID"}, OldThreshold: 1, NewSigners: KeySignerSet("T"), CarryExistingData: true},
		},
		Target: Target{Trusted: KeySignerSet("T")},
	}
	report, err := VerifyPlan(problem, badPlan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Accepted || len(report.Violations) == 0 {
		t.Fatalf("bad plan accepted: %+v", report)
	}
	if report.Violations[0].Code != ReasonDataLost {
		t.Fatalf("first violation = %q, want data_lost", report.Violations[0].Code)
	}
	if report.Violations[0].Trajectory == nil || len(report.Violations[0].Trajectory.Steps) != 1 {
		t.Fatalf("data-loss violation should include replay trajectory: %+v", report.Violations[0])
	}
}
