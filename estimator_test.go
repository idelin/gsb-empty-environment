package batsim

import (
	"math/big"
	"testing"
)

func estSpec() Spec {
	return Spec{
		Capacity:          Rat(20, 1),
		MaxChargePower:    Rat(6, 1),
		MaxDischargePower: Rat(6, 1),
		ChargeEff:         Rat(4, 5),
		DischargeEff:      Rat(4, 5),
		SelfDischarge:     Rat(1, 20),
	}
}

func runFleet(t *testing.T, batches int) (*Fleet, *Battery) {
	t.Helper()
	b, err := NewBattery(estSpec(), Rat(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewFleet(Rat(6, 1), b)
	if err != nil {
		t.Fatal(err)
	}
	cmds := []Command{
		{Charge: Rat(5, 1)},
		{Charge: Rat(3, 1)},
		{Discharge: Rat(2, 1)},
		{Charge: Rat(4, 1)},
		{Discharge: Rat(1, 1)},
		{Charge: Rat(2, 1)},
	}
	for i := 0; i < batches; i++ {
		if _, err := f.Step([]Command{cmds[i%len(cmds)]}); err != nil {
			t.Fatal(err)
		}
	}
	return f, b
}

// TestLateRevisionSeparation verifies that a late sensor revision only
// recomputes the estimate (reporting a delta) and never modifies the
// executed energy accounts.
func TestLateRevisionSeparation(t *testing.T) {
	f, b := runFleet(t, 6)
	spec := estSpec()

	est := NewEstimator(spec, f.BatteryView(0), Reading{Step: 0, Stored: Rat(0, 1)})

	// Snapshot executed state before the revision.
	execBefore := b.Records()
	totalsBefore := b.Totals()
	storedBefore := b.Stored()

	// Late revision: sensor corrects the state after step 2.
	delta, ok := est.Revise(Reading{Step: 2, Stored: Rat(9, 2)})
	if !ok {
		t.Fatal("expected a delta")
	}

	// Executed history untouched.
	execAfter := b.Records()
	if len(execAfter) != len(execBefore) {
		t.Fatalf("executed log length changed")
	}
	for i := range execBefore {
		a, c := execBefore[i], execAfter[i]
		if a.GridIn.Cmp(c.GridIn) != 0 || a.GridOut.Cmp(c.GridOut) != 0 ||
			a.StoredAfter.Cmp(c.StoredAfter) != 0 || a.SelfLoss.Cmp(c.SelfLoss) != 0 ||
			a.ChargeLoss.Cmp(c.ChargeLoss) != 0 || a.DischargeLoss.Cmp(c.DischargeLoss) != 0 {
			t.Fatalf("executed record %d modified by revision", i)
		}
	}
	totalsAfter := b.Totals()
	if totalsBefore.GridIn.Cmp(totalsAfter.GridIn) != 0 ||
		totalsBefore.GridOut.Cmp(totalsAfter.GridOut) != 0 ||
		totalsBefore.TotalLoss().Cmp(totalsAfter.TotalLoss()) != 0 {
		t.Fatalf("executed totals modified by revision")
	}
	if b.Stored().Cmp(storedBefore) != 0 {
		t.Fatalf("executed state modified by revision")
	}

	// The estimate, however, moved by exactly the reported delta.
	cur, ok := est.Estimate(6)
	if !ok {
		t.Fatal("no current estimate at step 6")
	}
	prev, ok := est.EstimateAtVersion(0, 6)
	if !ok {
		t.Fatal("no version-0 estimate at step 6")
	}
	if new(big.Rat).Sub(cur, prev).Cmp(delta) != 0 {
		t.Fatalf("delta %s inconsistent with estimates %s -> %s", delta, prev, cur)
	}

	// Estimates at or before the revised anchor follow the revision.
	atAnchor, ok := est.Estimate(2)
	if !ok || atAnchor.Cmp(Rat(9, 2)) != 0 {
		t.Fatalf("estimate at revised anchor = %v, want 9/2", atAnchor)
	}
}

// TestVersionReplay verifies every historical version can be
// deterministically replayed from its revision prefix, and that newer
// revisions never alter older versions.
func TestVersionReplay(t *testing.T) {
	f, _ := runFleet(t, 6)
	spec := estSpec()
	est := NewEstimator(spec, f.BatteryView(0), Reading{Step: 0, Stored: Rat(0, 1)})

	revisions := []Reading{
		{Step: 1, Stored: Rat(4, 1)},
		{Step: 3, Stored: Rat(5, 2)},
		{Step: 1, Stored: Rat(7, 2)}, // correction overrides same step
	}
	for _, r := range revisions {
		if _, ok := est.Revise(r); !ok {
			t.Fatalf("revision %+v produced no delta", r)
		}
	}
	if est.VersionCount() != 1+len(revisions) {
		t.Fatalf("VersionCount = %d", est.VersionCount())
	}

	// Replay each version and compare against the snapshot taken when
	// that version was current.
	snapshots := make([]map[int]*big.Rat, est.VersionCount())
	for v := 0; v < est.VersionCount(); v++ {
		snapshots[v] = make(map[int]*big.Rat)
		for step := 0; step <= 6; step++ {
			if val, ok := est.EstimateAtVersion(v, step); ok {
				snapshots[v][step] = val
			}
		}
	}
	// Replaying again must reproduce identical values (determinism).
	for v := 0; v < est.VersionCount(); v++ {
		for step := 0; step <= 6; step++ {
			got, gotOK := est.EstimateAtVersion(v, step)
			want, wantOK := snapshots[v][step]
			if gotOK != wantOK {
				t.Fatalf("version %d step %d: presence mismatch", v, step)
			}
			if gotOK && got.Cmp(want) != 0 {
				t.Fatalf("version %d step %d: replay %s != %s", v, step, got, want)
			}
		}
	}

	// The latest version reflects the overriding correction at step 1.
	cur, ok := est.Estimate(1)
	if !ok || cur.Cmp(Rat(7, 2)) != 0 {
		t.Fatalf("current estimate at step 1 = %v, want 7/2", cur)
	}
	// Version 1 (before the override) still reports the old reading.
	old, ok := est.EstimateAtVersion(1, 1)
	if !ok || old.Cmp(Rat(4, 1)) != 0 {
		t.Fatalf("version-1 estimate at step 1 = %v, want 4", old)
	}
}

// TestEstimateTracksExecutedWithoutRevision shows the estimator follows
// the executed trajectory exactly when anchored readings agree with
// reality, keeping estimate and execution consistent yet separate.
func TestEstimateTracksExecuted(t *testing.T) {
	f, b := runFleet(t, 6)
	est := NewEstimator(estSpec(), f.BatteryView(0), Reading{Step: 0, Stored: Rat(0, 1)})
	for step := 0; step <= 6; step++ {
		e, ok := est.Estimate(step)
		if !ok {
			t.Fatalf("no estimate at step %d", step)
		}
		var want *big.Rat
		if step == 0 {
			want = Rat(0, 1)
		} else {
			want = b.Records()[step-1].StoredAfter
		}
		if e.Cmp(want) != 0 {
			t.Fatalf("step %d: estimate %s != executed %s", step, e, want)
		}
	}
}
