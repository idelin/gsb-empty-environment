package upgradeplan

import (
	"strings"
	"testing"
)

func TestSameReleaseCanBeReplayedAfterEnablingStep(t *testing.T) {
	trusted := KeySignerSet("T")
	problem := Problem{
		Devices: []DeviceState{
			{SDK: 30, Signers: KeySignerSet("A"), HasData: true},
			{SDK: 30, Signers: KeySignerSet("B"), HasData: true},
		},
		Releases: []Release{
			{ID: "r-wave", OldSignerKeys: []string{"A"}, OldThreshold: 1, NewSigners: trusted, CarryExistingData: true},
			{ID: "r-convert-b", OldSignerKeys: []string{"B"}, OldThreshold: 1, NewSigners: KeySignerSet("A"), CarryExistingData: true},
		},
		Target: Target{Trusted: trusted},
	}

	replayedPlan := &Plan{
		WorstRounds: 3,
		Root: &PlanNode{
			Broadcast: &Broadcast{ReleaseID: "r-wave"},
			Branches: map[int]*PlanNode{
				1: {
					Broadcast: &Broadcast{ReleaseID: "r-convert-b"},
					Branches: map[int]*PlanNode{
						1: {
							Broadcast: &Broadcast{ReleaseID: "r-wave"},
							Branches: map[int]*PlanNode{
								1: {Terminal: true},
							},
						},
					},
				},
			},
		},
	}
	report, err := VerifyPlan(problem, replayedPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accepted {
		t.Fatalf("replayed release plan rejected: %+v", report.Violations)
	}

	shortest, err := Solve(problem)
	if err != nil {
		t.Fatal(err)
	}
	if shortest.WorstRounds != 2 {
		t.Fatalf("shortest rounds = %d, want 2", shortest.WorstRounds)
	}
}

func TestReplayDerivesAndRejectsDataLossAfterLaterUpgrade(t *testing.T) {
	trusted := KeySignerSet("T")
	problem := Problem{
		Releases: []Release{
			{ID: "r-wipe", OldThreshold: 1, NewSigners: KeySignerSet("MID"), CarryExistingData: false},
			{ID: "r-finish", OldSignerKeys: []string{"MID"}, OldThreshold: 1, NewSigners: trusted, CarryExistingData: true},
		},
		Target: Target{Trusted: trusted},
	}
	initial := []DeviceState{{SDK: 30, Signers: KeySignerSet("OLD"), HasData: true}}
	trace, err := ReplayTrajectory(problem, initial, []ReplayStep{
		{ReleaseID: "r-wipe"},
		{ReleaseID: "r-finish"},
	})
	if err == nil || !strings.Contains(err.Error(), "round 1") {
		t.Fatalf("ReplayTrajectory() error = %v, want round-1 data loss", err)
	}
	if trace == nil || len(trace.Steps) != 2 {
		t.Fatalf("trace = %+v, want both wipe and later finish steps", trace)
	}
	if !trace.Steps[0].After[0].DataLost || !trace.Steps[1].After[0].DataLost {
		t.Fatalf("sticky data-loss marker was not propagated: %+v", trace.Steps)
	}
	if err := AcceptFinalState(trace.Final, problem.Target); err == nil {
		t.Fatal("AcceptFinalState() accepted trusted signers with lost data")
	}
}
