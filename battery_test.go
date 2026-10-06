package batsim

import (
	"math/big"
	"testing"
)

func testSpec() Spec {
	return Spec{
		Capacity:          Rat(10, 1),
		MaxChargePower:    Rat(3, 1),
		MaxDischargePower: Rat(2, 1),
		ChargeEff:         Rat(1, 2),
		DischargeEff:      Rat(3, 4),
		SelfDischarge:     Rat(1, 10),
	}
}

// checkStepIdentity verifies the three exact per-step identities.
func checkStepIdentity(t *testing.T, rec StepRecord) {
	t.Helper()
	lhs := new(big.Rat).Sub(rec.StoredBefore, rec.SelfLoss)
	lhs.Add(lhs, rec.StoredAdd)
	lhs.Sub(lhs, rec.Drawn)
	if lhs.Cmp(rec.StoredAfter) != 0 {
		t.Fatalf("storage identity violated: %s != %s", lhs, rec.StoredAfter)
	}
	if sum := new(big.Rat).Add(rec.StoredAdd, rec.ChargeLoss); sum.Cmp(rec.GridIn) != 0 {
		t.Fatalf("charge identity violated: %s + %s != %s", rec.StoredAdd, rec.ChargeLoss, rec.GridIn)
	}
	if sum := new(big.Rat).Add(rec.GridOut, rec.DischargeLoss); sum.Cmp(rec.Drawn) != 0 {
		t.Fatalf("discharge identity violated: %s + %s != %s", rec.GridOut, rec.DischargeLoss, rec.Drawn)
	}
}

func TestPowerTruncation(t *testing.T) {
	b, err := NewBattery(testSpec(), Rat(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.Step(Command{Charge: Rat(100, 1)})
	if err != nil {
		t.Fatal(err)
	}
	// Grid intake truncated to the 3 kW power limit; stored gets 3 * 1/2.
	if rec.GridIn.Cmp(Rat(3, 1)) != 0 {
		t.Fatalf("GridIn = %s, want 3", rec.GridIn)
	}
	if rec.StoredAdd.Cmp(Rat(3, 2)) != 0 {
		t.Fatalf("StoredAdd = %s, want 3/2", rec.StoredAdd)
	}
	if rec.ChargeLoss.Cmp(Rat(3, 2)) != 0 {
		t.Fatalf("ChargeLoss = %s, want 3/2", rec.ChargeLoss)
	}
	checkStepIdentity(t, rec)
}

func TestCapacityTruncation(t *testing.T) {
	spec := testSpec()
	spec.ChargeEff = Rat(1, 1)
	spec.SelfDischarge = Rat(0, 1)
	b, err := NewBattery(spec, Rat(19, 2)) // 9.5 of 10
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.Step(Command{Charge: Rat(100, 1)})
	if err != nil {
		t.Fatal(err)
	}
	// Only 1/2 of headroom: grid intake truncated to exactly 1/2.
	if rec.GridIn.Cmp(Rat(1, 2)) != 0 {
		t.Fatalf("GridIn = %s, want 1/2", rec.GridIn)
	}
	if b.Stored().Cmp(Rat(10, 1)) != 0 {
		t.Fatalf("stored = %s, want 10", b.Stored())
	}
	checkStepIdentity(t, rec)
}

func TestSelfDischargeBeforeCommand(t *testing.T) {
	spec := testSpec()
	spec.ChargeEff = Rat(1, 1)
	spec.MaxChargePower = Rat(100, 1)
	b, err := NewBattery(spec, Rat(10, 1)) // full
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.Step(Command{Charge: Rat(100, 1)})
	if err != nil {
		t.Fatal(err)
	}
	// Self-discharge of 1/10 frees exactly 1 unit before charging.
	if rec.SelfLoss.Cmp(Rat(1, 1)) != 0 {
		t.Fatalf("SelfLoss = %s, want 1", rec.SelfLoss)
	}
	if rec.GridIn.Cmp(Rat(1, 1)) != 0 {
		t.Fatalf("GridIn = %s, want 1 (self-discharge applied first)", rec.GridIn)
	}
	if b.Stored().Cmp(Rat(10, 1)) != 0 {
		t.Fatalf("stored = %s, want 10", b.Stored())
	}
	checkStepIdentity(t, rec)
}

func TestEmptyAndFullBattery(t *testing.T) {
	spec := testSpec()
	b, err := NewBattery(spec, Rat(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	// Discharge from empty: nothing happens, no losses, no negatives.
	rec, err := b.Step(Command{Discharge: Rat(5, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if rec.GridOut.Sign() != 0 || rec.Drawn.Sign() != 0 || rec.DischargeLoss.Sign() != 0 {
		t.Fatalf("empty discharge produced energy: %+v", rec)
	}
	if b.Stored().Sign() != 0 {
		t.Fatalf("stored = %s, want 0", b.Stored())
	}
	checkStepIdentity(t, rec)

	// Partial truncation near empty: stored 1/2, eff 3/4 -> at most 3/8 out.
	b2, err := NewBattery(spec, Rat(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	rec2, err := b2.Step(Command{Discharge: Rat(5, 1)})
	if err != nil {
		t.Fatal(err)
	}
	// After self-discharge: 1/2 * 9/10 = 9/20 available; gridOut = 9/20 * 3/4.
	want := Rat(27, 80)
	if rec2.GridOut.Cmp(want) != 0 {
		t.Fatalf("GridOut = %s, want %s", rec2.GridOut, want)
	}
	if b2.Stored().Sign() != 0 {
		t.Fatalf("stored = %s, want 0", b2.Stored())
	}
	checkStepIdentity(t, rec2)

	// Charge into a full battery: nothing accepted.
	b3, err := NewBattery(spec, spec.Capacity)
	if err != nil {
		t.Fatal(err)
	}
	specNoSelf := testSpec()
	specNoSelf.SelfDischarge = Rat(0, 1)
	b3, err = NewBattery(specNoSelf, specNoSelf.Capacity)
	if err != nil {
		t.Fatal(err)
	}
	rec3, err := b3.Step(Command{Charge: Rat(5, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if rec3.GridIn.Sign() != 0 || rec3.StoredAdd.Sign() != 0 {
		t.Fatalf("full battery accepted charge: %+v", rec3)
	}
	checkStepIdentity(t, rec3)
}

func TestSimultaneousChargeDischargeRejected(t *testing.T) {
	b, err := NewBattery(testSpec(), Rat(5, 1))
	if err != nil {
		t.Fatal(err)
	}
	before := b.Stored()
	_, err = b.Step(Command{Charge: Rat(1, 1), Discharge: Rat(1, 1)})
	if err != ErrSimultaneousChargeDischarge {
		t.Fatalf("err = %v, want ErrSimultaneousChargeDischarge", err)
	}
	// Rejection is atomic: not even self-discharge was applied.
	if b.Stored().Cmp(before) != 0 {
		t.Fatalf("state changed on rejected command: %s -> %s", before, b.Stored())
	}
	if len(b.Records()) != 0 {
		t.Fatalf("rejected command was logged")
	}
}

// TestLongSequenceConservation runs a long deterministic command
// sequence and verifies exact global conservation plus replay recovery:
// re-simulating the executed records from the initial state reproduces
// the final stored energy exactly.
func TestLongSequenceConservation(t *testing.T) {
	spec := testSpec()
	initial := Rat(5, 1)
	b, err := NewBattery(spec, initial)
	if err != nil {
		t.Fatal(err)
	}

	seed := int64(42)
	next := func(mod int64) int64 {
		seed = seed*1103515245 + 12345
		v := (seed / 65536) % mod
		if v < 0 {
			v = -v
		}
		return v
	}

	const steps = 2000
	for i := 0; i < steps; i++ {
		var cmd Command
		switch next(3) {
		case 0:
			cmd = Command{Charge: Rat(next(8), 3)}
		case 1:
			cmd = Command{Discharge: Rat(next(6), 2)}
		default:
			cmd = Command{}
		}
		rec, err := b.Step(cmd)
		if err != nil {
			t.Fatal(err)
		}
		checkStepIdentity(t, rec)
		if b.Stored().Sign() < 0 || b.Stored().Cmp(spec.Capacity) > 0 {
			t.Fatalf("step %d: stored %s out of bounds", i, b.Stored())
		}
	}

	if !b.CheckConservation(initial) {
		t.Fatalf("global conservation violated after %d steps", steps)
	}

	// Replay recovery: rebuild the trajectory purely from the log.
	cur := clone(initial)
	for _, rec := range b.Records() {
		if rec.StoredBefore.Cmp(cur) != 0 {
			t.Fatalf("replay mismatch at step %d: %s != %s", rec.Step, rec.StoredBefore, cur)
		}
		cur = clone(rec.StoredAfter)
	}
	if cur.Cmp(b.Stored()) != 0 {
		t.Fatalf("replay final %s != stored %s", cur, b.Stored())
	}

	// Totals equal the sum over records (losses accounted separately).
	sum := zeroTotals()
	for _, rec := range b.Records() {
		sum.GridIn.Add(sum.GridIn, rec.GridIn)
		sum.GridOut.Add(sum.GridOut, rec.GridOut)
		sum.SelfLoss.Add(sum.SelfLoss, rec.SelfLoss)
		sum.ChargeLoss.Add(sum.ChargeLoss, rec.ChargeLoss)
		sum.DischargeLoss.Add(sum.DischargeLoss, rec.DischargeLoss)
	}
	tot := b.Totals()
	if sum.GridIn.Cmp(tot.GridIn) != 0 || sum.GridOut.Cmp(tot.GridOut) != 0 ||
		sum.SelfLoss.Cmp(tot.SelfLoss) != 0 || sum.ChargeLoss.Cmp(tot.ChargeLoss) != 0 ||
		sum.DischargeLoss.Cmp(tot.DischargeLoss) != 0 {
		t.Fatalf("totals do not match record sums")
	}
}
