package batsim

import (
	"math/big"
	"testing"
)

func fleetSpec() Spec {
	return Spec{
		Capacity:          Rat(20, 1),
		MaxChargePower:    Rat(10, 1),
		MaxDischargePower: Rat(10, 1),
		ChargeEff:         Rat(9, 10),
		DischargeEff:      Rat(9, 10),
		SelfDischarge:     Rat(1, 100),
	}
}

func TestSharedChargeLimit(t *testing.T) {
	b1, _ := NewBattery(fleetSpec(), Rat(0, 1))
	b2, _ := NewBattery(fleetSpec(), Rat(0, 1))
	f, err := NewFleet(Rat(5, 1), b1, b2)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := f.Step([]Command{
		{Charge: Rat(4, 1)},
		{Charge: Rat(4, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// First-come allocation: b1 gets 4, b2 gets the remaining 1.
	if recs[0].GridIn.Cmp(Rat(4, 1)) != 0 {
		t.Fatalf("b1 GridIn = %s, want 4", recs[0].GridIn)
	}
	if recs[1].GridIn.Cmp(Rat(1, 1)) != 0 {
		t.Fatalf("b2 GridIn = %s, want 1", recs[1].GridIn)
	}
	total := new(big.Rat).Add(recs[0].GridIn, recs[1].GridIn)
	if total.Cmp(Rat(5, 1)) != 0 {
		t.Fatalf("shared limit exceeded: total %s", total)
	}
}

func TestBatchAtomicRejection(t *testing.T) {
	b1, _ := NewBattery(fleetSpec(), Rat(1, 1))
	b2, _ := NewBattery(fleetSpec(), Rat(1, 1))
	f, _ := NewFleet(Rat(5, 1), b1, b2)
	before1, before2 := b1.Stored(), b2.Stored()

	_, err := f.Step([]Command{
		{Charge: Rat(2, 1)},
		{Charge: Rat(1, 1), Discharge: Rat(1, 1)}, // invalid
	})
	if err != ErrSimultaneousChargeDischarge {
		t.Fatalf("err = %v, want ErrSimultaneousChargeDischarge", err)
	}
	// The whole batch is rejected: neither battery stepped.
	if b1.Stored().Cmp(before1) != 0 || b2.Stored().Cmp(before2) != 0 {
		t.Fatalf("partial execution on rejected batch")
	}
	if len(f.History()) != 0 {
		t.Fatalf("rejected batch committed to history")
	}
}

func TestExecutedHistoryImmutable(t *testing.T) {
	b1, _ := NewBattery(fleetSpec(), Rat(0, 1))
	f, _ := NewFleet(Rat(5, 1), b1)
	recs, err := f.Step([]Command{{Charge: Rat(3, 1)}})
	if err != nil {
		t.Fatal(err)
	}
	// Mutating returned records must not touch committed history.
	recs[0].GridIn.SetInt64(999)
	hist := f.History()
	if hist[0][0].GridIn.Cmp(Rat(3, 1)) != 0 {
		t.Fatalf("executed history mutated via returned record")
	}
	// Battery log equally immutable.
	brecs := b1.Records()
	brecs[0].StoredAfter.SetInt64(0)
	if b1.Records()[0].StoredAfter.Cmp(Rat(27, 10)) != 0 {
		t.Fatalf("battery log mutated via returned record")
	}
}

func TestFleetConservation(t *testing.T) {
	spec := fleetSpec()
	inits := []*big.Rat{Rat(3, 1), Rat(7, 2), Rat(0, 1)}
	bs := make([]*Battery, 3)
	for i, init := range inits {
		bs[i], _ = NewBattery(spec, init)
	}
	f, _ := NewFleet(Rat(4, 1), bs...)

	seed := int64(7)
	next := func(mod int64) int64 {
		seed = seed*1103515245 + 12345
		v := (seed / 65536) % mod
		if v < 0 {
			v = -v
		}
		return v
	}
	for step := 0; step < 500; step++ {
		cmds := make([]Command, 3)
		for i := range cmds {
			switch next(3) {
			case 0:
				cmds[i] = Command{Charge: Rat(next(9), 2)}
			case 1:
				cmds[i] = Command{Discharge: Rat(next(5), 2)}
			}
		}
		recs, err := f.Step(cmds)
		if err != nil {
			t.Fatal(err)
		}
		// Shared limit respected exactly at every batch.
		sum := Rat(0, 1)
		for _, r := range recs {
			sum.Add(sum, r.GridIn)
		}
		if sum.Cmp(Rat(4, 1)) > 0 {
			t.Fatalf("step %d: shared limit exceeded: %s", step, sum)
		}
	}
	// Per-battery global conservation over the whole run.
	for i, b := range bs {
		if !b.CheckConservation(inits[i]) {
			t.Fatalf("battery %d: conservation violated", i)
		}
	}
}
