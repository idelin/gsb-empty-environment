package batsim

import (
	"testing"
)

func mustFleet(t *testing.T, n int, sharedLimit string) *Fleet {
	t.Helper()
	var bs []*Battery
	for i := 0; i < n; i++ {
		bs = append(bs, mustBattery(t, "100", "1000", "1000", "1", "1", "0", "10"))
	}
	return NewFleet(bs, rat(sharedLimit))
}

func TestSharedChargeLimitAllocation(t *testing.T) {
	f := mustFleet(t, 3, "10")
	cmds := []Command{
		{ChargeW: rat("6")},
		{ChargeW: rat("6")},
		{ChargeW: rat("6")},
	}
	res := f.ExecuteBatch(cmds, rat("1"))
	want := []string{"6", "4", "0"}
	for i, w := range want {
		eqRat(t, res[i].AllocatedChargeW, w, "allocated charge")
		eqRat(t, res[i].Step.GridIn, w, "grid in")
	}
	eqRat(t, f.Batteries[0].Stored, "16", "battery 0 stored")
	eqRat(t, f.Batteries[1].Stored, "14", "battery 1 stored")
	eqRat(t, f.Batteries[2].Stored, "10", "battery 2 stored")
	for i, b := range f.Batteries {
		checkConservation(t, b, "fleet battery "+string(rune('0'+i)))
	}
}

func TestDischargeDoesNotConsumeSharedBudget(t *testing.T) {
	f := mustFleet(t, 2, "5")
	res := f.ExecuteBatch([]Command{
		{DischargeW: rat("8")},
		{ChargeW: rat("7")},
	}, rat("1"))
	eqRat(t, res[0].AllocatedChargeW, "0", "discharge allocation")
	eqRat(t, res[0].Step.GridOut, "8", "discharge out")
	eqRat(t, res[1].AllocatedChargeW, "5", "charge clipped to shared limit")
	eqRat(t, res[1].Step.GridIn, "5", "grid in")
}

func TestBatchAtomicAllocationAndNoRollback(t *testing.T) {
	f := mustFleet(t, 3, "10")
	// Middle command is invalid (simultaneous charge+discharge): it must
	// be rejected, must receive no budget, and must not roll back or
	// block the other commands in the batch.
	cmds := []Command{
		{ChargeW: rat("6")},
		{ChargeW: rat("3"), DischargeW: rat("3")},
		{ChargeW: rat("6")},
	}
	res := f.ExecuteBatch(cmds, rat("1"))
	if res[1].Err != ErrSimultaneousChargeDischarge {
		t.Fatalf("cmd 1: want rejection, got %v", res[1].Err)
	}
	if res[0].Err != nil || res[2].Err != nil {
		t.Fatalf("valid commands must execute: %v / %v", res[0].Err, res[2].Err)
	}
	eqRat(t, res[0].AllocatedChargeW, "6", "cmd 0 allocation")
	eqRat(t, res[1].AllocatedChargeW, "0", "rejected cmd gets no budget")
	eqRat(t, res[2].AllocatedChargeW, "4", "cmd 2 gets remaining budget")
	// Executed parts persist: batteries 0 and 2 charged, battery 1 untouched.
	eqRat(t, f.Batteries[0].Stored, "16", "battery 0 stored")
	eqRat(t, f.Batteries[1].Stored, "10", "battery 1 stored")
	eqRat(t, f.Batteries[2].Stored, "14", "battery 2 stored")

	// The action log is append-only and records the rejection.
	if len(f.ActionLog) != 3 {
		t.Fatalf("action log len = %d, want 3", len(f.ActionLog))
	}
	if f.ActionLog[1].Err != ErrSimultaneousChargeDischarge {
		t.Fatalf("action log must record the rejection")
	}
	eqRat(t, f.ActionLog[0].Result.GridIn, "6", "logged grid in")
	eqRat(t, f.ActionLog[2].Result.GridIn, "4", "logged grid in")
}

func TestActionLogReplayRecoversFleetState(t *testing.T) {
	f := mustFleet(t, 2, "7")
	rng := &lcg{state: 7}
	for batch := 0; batch < 50; batch++ {
		cmds := make([]Command, 2)
		for i := range cmds {
			switch rng.next() % 3 {
			case 0:
				cmds[i] = Command{ChargeW: rng.rat(100)}
			case 1:
				cmds[i] = Command{DischargeW: rng.rat(100)}
			}
		}
		f.ExecuteBatch(cmds, rat("1/2"))
	}
	// Replay the immutable action log onto a fresh fleet.
	replay := mustFleet(t, 2, "7")
	batches := make(map[int][]Command)
	maxBatch := -1
	for _, a := range f.ActionLog {
		if a.Err != nil {
			continue
		}
		if a.Batch > maxBatch {
			maxBatch = a.Batch
		}
		batches[a.Batch] = append(batches[a.Batch], a.Command)
	}
	for b := 0; b <= maxBatch; b++ {
		replay.ExecuteBatch(batches[b], rat("1/2"))
	}
	for i := range f.Batteries {
		if replay.Batteries[i].Stored.Cmp(f.Batteries[i].Stored) != 0 {
			t.Fatalf("battery %d replay mismatch: %s != %s",
				i, replay.Batteries[i].Stored, f.Batteries[i].Stored)
		}
		checkConservation(t, replay.Batteries[i], "replayed fleet battery")
	}
}
