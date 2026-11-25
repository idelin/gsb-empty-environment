package upgradeplan

import (
	"fmt"
	"testing"
)

func TestMaxBoundChainSmoke(t *testing.T) {
	devices := make([]DeviceState, MaxDevices)
	for i := range devices {
		devices[i] = DeviceState{SDK: 30, Signers: KeySignerSet("K0"), HasData: true}
	}

	releases := make([]Release, 0, MaxReleases)
	for stage := 1; stage <= MaxRounds; stage++ {
		oldKeys := []string{fmt.Sprintf("K%da", stage-1), fmt.Sprintf("K%db", stage-1)}
		if stage == 1 {
			oldKeys = []string{"K0"}
		}
		releases = append(releases, Release{
			ID:            fmt.Sprintf("r%02d", stage),
			OldThreshold:  1,
			OldSignerKeys: oldKeys,
			NewSignerSets: []SignerSet{
				KeySignerSet(fmt.Sprintf("K%da", stage)),
				KeySignerSet(fmt.Sprintf("K%db", stage)),
			},
			CarryExistingData: true,
		})
	}
	for i := 0; i < MaxReleases-MaxRounds; i++ {
		releases = append(releases, Release{
			ID:            fmt.Sprintf("z-junk-%d", i),
			OldThreshold:  1,
			OldSignerKeys: []string{"NEVER"},
			NewSigners:    KeySignerSet("X"),
		})
	}

	problem := Problem{
		Devices:  devices,
		Releases: releases,
		Target: Target{TrustedSets: []SignerSet{
			KeySignerSet("K8a"),
			KeySignerSet("K8b"),
		}},
		MaxRounds: MaxRounds,
	}
	plan, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if plan.WorstRounds != MaxRounds || plan.Root.Broadcast == nil || plan.Root.Broadcast.ReleaseID != "r01" {
		t.Fatalf("unexpected max-bound plan: rounds=%d root=%+v", plan.WorstRounds, plan.Root.Broadcast)
	}
	report, err := VerifyPlan(problem, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Accepted {
		t.Fatalf("max-bound plan rejected: %+v", report.Violations)
	}
}

func TestRejectsInputsBeyondStatedLimits(t *testing.T) {
	tooManyDevices := Problem{Devices: make([]DeviceState, MaxDevices+1)}
	if err := tooManyDevices.ValidateLimits(); err == nil {
		t.Fatal("expected device limit error")
	}
	tooManyReleases := Problem{Releases: make([]Release, MaxReleases+1)}
	if err := tooManyReleases.ValidateLimits(); err == nil {
		t.Fatal("expected release limit error")
	}
	tooManyRounds := Problem{MaxRounds: MaxRounds + 1}
	if err := tooManyRounds.ValidateLimits(); err == nil {
		t.Fatal("expected round limit error")
	}
}
