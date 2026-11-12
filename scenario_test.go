package netsec

import "testing"

// TestSensitiveLeftWritableIsDeadEnd models the headline hazard from the
// specification: enabling a sensitive region and then locking it freezes a
// writable area forever, so no safe sequence can reach the sealed target.
func TestSensitiveLeftWritableIsDeadEnd(t *testing.T) {
	regions := []RegionSpec{
		{
			Name:         "secure-boot",
			StartEnable:  true,
			StartLock:    true,
			TargetEnable: false,
			TargetLock:   true,
		},
	}

	m, err := BuildModel(regions, false)
	if err != nil {
		t.Fatal(err)
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusUnreachable {
		t.Fatalf("status=%s want unreachable", p.Status)
	}

	report := VerifyDeadEnds(m, DefaultVerifyStates)
	if report.Err != nil || !report.Consistent {
		t.Fatalf("verify failed: %+v", report)
	}
	// The locked-open start can never reach the locked-shut goal, while the
	// unlocked states still can, so the dead-end set is non-trivial.
	if report.Dead == 0 || report.GoalReachable {
		t.Fatalf("locked-open start must be a dead end: %+v", report)
	}
}

// TestRecoverableBeforeLock proves the planner clears an open enable before
// locking, whereas doing it in the opposite order would be unrecoverable.
func TestRecoverableBeforeLock(t *testing.T) {
	regions := []RegionSpec{
		{StartEnable: true, TargetEnable: false, TargetLock: true},
	}
	m, err := BuildModel(regions, false)
	if err != nil {
		t.Fatal(err)
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved || p.Depth != 1 {
		t.Fatalf("status=%s depth=%d", p.Status, p.Depth)
	}
	final := Simulate(m, p.Writes)
	if !m.AtGoal(final) || !m.Safe(final) {
		t.Fatalf("final=%b goal=%b", final, m.Target)
	}

	report := VerifyDeadEnds(m, DefaultVerifyStates)
	if !report.Consistent || !report.GoalReachable {
		t.Fatalf("unexpected report: %+v", report)
	}
}

// TestForbiddenOpenButUnlockedRecovers covers a start configuration that
// already opens the sensitive enable but has not locked it yet. The planner
// must refuse such a start because the never-open invariant covers every
// visited state, including the initial one.
func TestForbiddenOpenButUnlockedRecovers(t *testing.T) {
	m := Model{
		Regions:       1,
		Start:         0b01,
		Target:        0b10,
		TargetMask:    0b11,
		ForbiddenMask: 0b01,
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusUnreachable || p.Err == nil {
		t.Fatalf("status=%s err=%v, want explicit unsafe-start rejection", p.Status, p.Err)
	}

	// Once the host has cleared the enable bit outside the planner, the same
	// target becomes reachable in a single safe write.
	m.Start = 0b00
	p = PlanModel(m, Budget{})
	if p.Status != StatusSolved || p.Depth != 1 {
		t.Fatalf("status=%s depth=%d", p.Status, p.Depth)
	}
	if final := Simulate(m, p.Writes); !m.AtGoal(final) || !m.Safe(final) {
		t.Fatalf("final state %b is not a safe goal", final)
	}
}
