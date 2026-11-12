package netsec

import (
	"math/big"
	"testing"
)

func closeWindow(e *Engine, windowRounds int) {
	for i := 0; i < windowRounds; i++ {
		e.Advance()
	}
}

func TestHappyPathAggregation(t *testing.T) {
	const window = 2
	e, err := NewEngine(window)
	if err != nil {
		t.Fatal(err)
	}
	dealers := []string{"d1", "d2", "d3"}
	receivers := []string{"r1", "r2", "r3"}
	materials, _ := mustRegisterRun(t, e, dealers, receivers, 2)

	closeWindow(e, window)
	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(res.Qualified) != 3 || len(res.Disqualified) != 0 {
		t.Fatalf("unexpected qualification: %+v", res)
	}

	var wantX, wantY *big.Int
	for _, did := range dealers {
		c0 := materials[did].commit.Constant()
		if wantX == nil {
			wantX, wantY = c0.X, c0.Y
		} else {
			wantX, wantY = Curve.Add(wantX, wantY, c0.X, c0.Y)
		}
	}
	if res.AggregatedPublicKey.X.Cmp(wantX) != 0 || res.AggregatedPublicKey.Y.Cmp(wantY) != 0 {
		t.Fatal("aggregated public key mismatch")
	}

	for _, rid := range receivers {
		wantShare := new(big.Int)
		for _, did := range dealers {
			wantShare.Add(wantShare, materials[did].shares[rid])
		}
		wantShare.Mod(wantShare, Curve.Params().N)
		if res.AggregatedShares[rid].Cmp(wantShare) != 0 {
			t.Fatalf("aggregated share mismatch for %s", rid)
		}
		// g^(aggregated share at x) must equal the combined commitments
		// evaluated at that receiver's index x.
		x := big.NewInt(int64(1 + indexOf(receivers, rid)))
		var ex, ey *big.Int
		for _, did := range dealers {
			ev := evalCommitment(materials[did].commit, x)
			if ex == nil {
				ex, ey = ev.X, ev.Y
			} else {
				ex, ey = Curve.Add(ex, ey, ev.X, ev.Y)
			}
		}
		gx, gy := Curve.ScalarBaseMult(scalarBytes(res.AggregatedShares[rid]))
		if gx.Cmp(ex) != 0 || gy.Cmp(ey) != 0 {
			t.Fatalf("g^share does not match combined commitments for %s", rid)
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestTamperedShareComplaintDisqualifies(t *testing.T) {
	const window = 2
	e, _ := NewEngine(window)
	dealers := []string{"good", "bad"}
	receivers := []string{"r1", "r2"}
	materials, _ := mustRegisterRun(t, e, dealers, receivers, 2)

	badHash := materials["bad"].commit.Hash()
	tampered := new(big.Int).Add(materials["bad"].shares["r1"], big.NewInt(1))
	tampered.Mod(tampered, Curve.Params().N)
	if err := e.FileComplaint("bad", "r1", tampered, badHash); err != nil {
		t.Fatalf("file complaint: %v", err)
	}
	closeWindow(e, window)
	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if res.Disqualified["bad"] != ReasonUnresolvedComplaint {
		t.Fatalf("bad dealer not disqualified: %+v", res.Disqualified)
	}
	if _, ok := res.Disqualified["good"]; ok {
		t.Fatal("good dealer wrongly disqualified")
	}
	if _, ok := res.AggregatedShares["r1"]; !ok {
		t.Fatal("missing aggregated share")
	}
	if res.AggregatedShares["r1"].Cmp(materials["good"].shares["r1"]) != 0 {
		t.Fatal("disqualified dealer share leaked into aggregate")
	}
}

func TestFalseAccusation(t *testing.T) {
	const window = 2
	e, _ := NewEngine(window)
	dealers := []string{"d1"}
	receivers := []string{"r1", "r2"}
	materials, _ := mustRegisterRun(t, e, dealers, receivers, 2)

	hash := materials["d1"].commit.Hash()
	bogus := new(big.Int).Add(materials["d1"].shares["r1"], big.NewInt(1))
	bogus.Mod(bogus, Curve.Params().N)
	if err := e.FileComplaint("d1", "r1", bogus, hash); err != nil {
		t.Fatalf("file complaint: %v", err)
	}
	if err := e.DiscloseShare("d1", "r1", materials["d1"].shares["r1"], hash); err != nil {
		t.Fatalf("disclose: %v", err)
	}

	closeWindow(e, window)
	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(res.Disqualified) != 0 {
		t.Fatalf("dealer disqualified despite valid disclosure: %+v", res.Disqualified)
	}
	if len(res.FalseAccusers) != 1 || res.FalseAccusers[0] != "r1" {
		t.Fatalf("false accuser not recorded: %+v", res.FalseAccusers)
	}
}

func TestInvalidDisclosureDisqualifies(t *testing.T) {
	const window = 2
	e, _ := NewEngine(window)
	dealers := []string{"good", "d1"}
	receivers := []string{"r1"}
	materials, _ := mustRegisterRun(t, e, dealers, receivers, 2)

	hash := materials["d1"].commit.Hash()
	bogus := new(big.Int).Add(materials["d1"].shares["r1"], big.NewInt(1))
	bogus.Mod(bogus, Curve.Params().N)
	if err := e.FileComplaint("d1", "r1", bogus, hash); err != nil {
		t.Fatalf("file complaint: %v", err)
	}
	lie := new(big.Int).Add(bogus, big.NewInt(2))
	lie.Mod(lie, Curve.Params().N)
	if err := e.DiscloseShare("d1", "r1", lie, hash); err != nil {
		t.Fatalf("disclose: %v", err)
	}

	closeWindow(e, window)
	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if res.Disqualified["d1"] != ReasonInvalidDisclosure {
		t.Fatalf("want invalid disclosure disqualification, got %+v", res.Disqualified)
	}
	if _, bad := res.Disqualified["good"]; bad {
		t.Fatal("honest dealer wrongly disqualified")
	}
	if len(res.Qualified) != 1 || res.Qualified[0] != "good" {
		t.Fatalf("unexpected qualified set: %+v", res.Qualified)
	}
}
