package batsim

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func mustBattery(t *testing.T, cap, maxCh, maxDis, effCh, effDis, self, init string) *Battery {
	t.Helper()
	b, err := NewBattery(rat(cap), rat(maxCh), rat(maxDis), rat(effCh), rat(effDis), rat(self), rat(init))
	if err != nil {
		t.Fatalf("NewBattery: %v", err)
	}
	return b
}

func eqRat(t *testing.T, got *big.Rat, want, ctx string) {
	t.Helper()
	if got.Cmp(rat(want)) != 0 {
		t.Fatalf("%s: got %s, want %s", ctx, got.RatString(), want)
	}
}

func checkConservation(t *testing.T, b *Battery, ctx string) {
	t.Helper()
	if e := b.ConservationError(); e.Sign() != 0 {
		t.Fatalf("%s: conservation residual %s", ctx, e.RatString())
	}
}

func TestRejectSimultaneousChargeDischarge(t *testing.T) {
	b := mustBattery(t, "100", "50", "50", "1/2", "1/2", "1/10", "40")
	_, err := b.Step(Command{ChargeW: rat("5"), DischargeW: rat("5")}, rat("1"))
	if err != ErrSimultaneousChargeDischarge {
		t.Fatalf("want ErrSimultaneousChargeDischarge, got %v", err)
	}
	// Rejected command must not mutate anything, including self-discharge.
	eqRat(t, b.Stored, "40", "stored after rejected step")
	if b.TotalSelfDischargeLoss.Sign() != 0 {
		t.Fatalf("self-discharge booked on rejected step: %s", b.TotalSelfDischargeLoss)
	}
	checkConservation(t, b, "after rejection")
}

func TestSelfDischargeAppliesBeforeCommand(t *testing.T) {
	// Full battery, rate 1/10, then charge: self-discharge frees headroom
	// that the charge command must be allowed to use.
	b := mustBattery(t, "100", "50", "50", "1", "1", "1/10", "100")
	res, err := b.Step(Command{ChargeW: rat("5")}, rat("1"))
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	eqRat(t, res.SelfDischargeLoss, "10", "self-discharge loss")
	eqRat(t, res.GridIn, "5", "grid in")
	eqRat(t, b.Stored, "95", "stored")
	if res.Truncated {
		t.Fatalf("charge into freed headroom must not truncate")
	}
	checkConservation(t, b, "self-discharge-first")
}

func TestChargeTruncatedByCapacity(t *testing.T) {
	b := mustBattery(t, "100", "50", "50", "1/2", "1/2", "0", "95")
	res, err := b.Step(Command{ChargeW: rat("20")}, rat("1"))
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("expected truncation at capacity")
	}
	// Headroom 5 with eff 1/2 => gridIn 10, storedAdd 5, convLoss 5.
	eqRat(t, res.GridIn, "10", "grid in")
	eqRat(t, res.ConversionLoss, "5", "conversion loss")
	eqRat(t, b.Stored, "100", "stored")
	eqRat(t, b.TotalConversionLoss, "5", "booked conversion loss")
	checkConservation(t, b, "capacity truncation")
}

func TestChargeTruncatedByPower(t *testing.T) {
	b := mustBattery(t, "1000", "10", "50", "1", "1", "0", "0")
	res, _ := b.Step(Command{ChargeW: rat("100")}, rat("1"))
	if !res.Truncated {
		t.Fatalf("expected truncation at power limit")
	}
	eqRat(t, res.GridIn, "10", "grid in")
	eqRat(t, b.Stored, "10", "stored")
	checkConservation(t, b, "power truncation")
}

func TestDischargeTruncatedByPower(t *testing.T) {
	b := mustBattery(t, "1000", "50", "10", "1", "1", "0", "500")
	res, _ := b.Step(Command{DischargeW: rat("100")}, rat("1"))
	if !res.Truncated {
		t.Fatalf("expected truncation at power limit")
	}
	eqRat(t, res.GridOut, "10", "grid out")
	eqRat(t, b.Stored, "490", "stored")
	checkConservation(t, b, "discharge power truncation")
}

func TestDischargeFromNearlyEmpty(t *testing.T) {
	b := mustBattery(t, "100", "50", "50", "1", "1/2", "0", "3")
	res, _ := b.Step(Command{DischargeW: rat("100")}, rat("1"))
	if !res.Truncated {
		t.Fatalf("expected truncation on nearly empty battery")
	}
	// storedRemove would be 200, clipped to 3 => out = 3 * 1/2.
	eqRat(t, res.GridOut, "3/2", "grid out")
	eqRat(t, res.ConversionLoss, "3/2", "conversion loss")
	eqRat(t, b.Stored, "0", "stored")
	checkConservation(t, b, "empty truncation")
}

func TestEmptyBatteryDischargeIsNoop(t *testing.T) {
	b := mustBattery(t, "100", "50", "50", "1", "1", "0", "0")
	res, _ := b.Step(Command{DischargeW: rat("10")}, rat("1"))
	if !res.Truncated {
		t.Fatalf("expected truncation on empty battery")
	}
	eqRat(t, res.GridOut, "0", "grid out")
	eqRat(t, b.Stored, "0", "stored")
	checkConservation(t, b, "empty noop")
}

func TestFullBatteryChargeIsNoop(t *testing.T) {
	b := mustBattery(t, "100", "50", "50", "1/2", "1", "0", "100")
	res, _ := b.Step(Command{ChargeW: rat("10")}, rat("1"))
	if !res.Truncated {
		t.Fatalf("expected truncation on full battery")
	}
	eqRat(t, res.GridIn, "0", "grid in")
	eqRat(t, b.Stored, "100", "stored")
	checkConservation(t, b, "full noop")
}

// lcg is a deterministic pseudo-random source for reproducible sequences.
type lcg struct{ state uint64 }

func (l *lcg) next() uint64 {
	l.state = l.state*6364136223846793005 + 1442695040888963407
	return l.state >> 16
}

func (l *lcg) rat(max int64) *big.Rat {
	return big.NewRat(int64(l.next()%uint64(max))+1, 7)
}

func TestLongSequenceConservationAndReplay(t *testing.T) {
	b := mustBattery(t, "100", "30", "25", "3/4", "5/6", "1/100", "50")
	type record struct {
		cmd Command
		dt  *big.Rat
	}
	var log []record
	rng := &lcg{state: 42}
	const steps = 2000
	for i := 0; i < steps; i++ {
		var cmd Command
		switch rng.next() % 3 {
		case 0:
			cmd = Command{ChargeW: rng.rat(200)}
		case 1:
			cmd = Command{DischargeW: rng.rat(200)}
		default:
			cmd = Command{}
		}
		dt := rng.rat(10)
		if _, err := b.Step(cmd, dt); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		log = append(log, record{cmd, dt})
		if e := b.ConservationError(); e.Sign() != 0 {
			t.Fatalf("step %d: conservation residual %s", i, e.RatString())
		}
		if b.Stored.Sign() < 0 || b.Stored.Cmp(b.Capacity) > 0 {
			t.Fatalf("step %d: stored %s out of bounds", i, b.Stored)
		}
	}

	// Recovery: replaying the recorded log from the initial state must
	// reproduce every counter exactly.
	replay := mustBattery(t, "100", "30", "25", "3/4", "5/6", "1/100", "50")
	for i, r := range log {
		if _, err := replay.Step(r.cmd, r.dt); err != nil {
			t.Fatalf("replay step %d: %v", i, err)
		}
	}
	for name, pair := range map[string][2]*big.Rat{
		"stored":          {replay.Stored, b.Stored},
		"grid in":         {replay.TotalGridIn, b.TotalGridIn},
		"grid out":        {replay.TotalGridOut, b.TotalGridOut},
		"self-discharge":  {replay.TotalSelfDischargeLoss, b.TotalSelfDischargeLoss},
		"conversion loss": {replay.TotalConversionLoss, b.TotalConversionLoss},
	} {
		if pair[0].Cmp(pair[1]) != 0 {
			t.Fatalf("replay mismatch %s: %s != %s", name, pair[0], pair[1])
		}
	}
}
