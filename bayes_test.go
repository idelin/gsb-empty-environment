package bayes

import (
	"math/big"
	"testing"
)

func rats(vals ...*big.Rat) []*big.Rat { return vals }

func eq(t *testing.T, got *big.Rat, num, den int64) {
	t.Helper()
	want := big.NewRat(num, den)
	if got.Cmp(want) != 0 {
		t.Fatalf("got %s, want %s", got.RatString(), want.RatString())
	}
}

// enumeratePosterior is an independent cross-check: it enumerates the joint
// distribution over (hypothesis, evidence-world) atoms as
// prior[h] * prod_g L_g[h] and normalizes, without reusing Model code.
func enumeratePosterior(prior []*big.Rat, groups [][]*big.Rat) []*big.Rat {
	joint := make([]*big.Rat, len(prior))
	total := new(big.Rat)
	for h := range prior {
		joint[h] = new(big.Rat).Set(prior[h])
		for _, g := range groups {
			joint[h].Mul(joint[h], g[h])
		}
		total.Add(total, joint[h])
	}
	for h := range joint {
		joint[h].Quo(joint[h], total)
	}
	return joint
}

// enumerateError is an independent cross-check for expected misjudgment:
// enumerate every (hypothesis, outcome) joint atom, pick the argmax
// hypothesis per outcome, and sum the probability mass of all other
// hypotheses.
func enumerateError(w []*big.Rat, obs Observation) *big.Rat {
	err := new(big.Rat)
	for _, row := range obs.OutcomeProb {
		bestH, best := 0, new(big.Rat)
		for h, p := range row {
			joint := new(big.Rat).Mul(w[h], p)
			if joint.Cmp(best) > 0 {
				best.Set(joint)
				bestH = h
			}
		}
		for h, p := range row {
			if h != bestH {
				err.Add(err, new(big.Rat).Mul(w[h], p))
			}
		}
	}
	return err
}

func TestPosteriorExactJointGroup(t *testing.T) {
	m, err := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	// One group whose joint likelihood (1/2, 1/4) is NOT the product of
	// per-observation likelihoods; it must be used as-is.
	if _, err := m.AddEvidence("g1", rats(Rat(1, 2), Rat(1, 4))); err != nil {
		t.Fatal(err)
	}
	post, err := m.Posterior()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, post[0], 2, 3)
	eq(t, post[1], 1, 3)

	// Cross-check by joint enumeration.
	want := enumeratePosterior(rats(Rat(1, 2), Rat(1, 2)), [][]*big.Rat{rats(Rat(1, 2), Rat(1, 4))})
	for h := range want {
		if post[h].Cmp(want[h]) != 0 {
			t.Fatalf("enumeration mismatch at h=%d: %s vs %s", h, post[h], want[h])
		}
	}
}

func TestPosteriorExactFractions(t *testing.T) {
	// Values with no exact float representation must stay exact.
	m, err := NewModel(rats(Rat(1, 3), Rat(1, 3), Rat(1, 3)))
	if err != nil {
		t.Fatal(err)
	}
	m.AddEvidence("e1", rats(Rat(1, 7), Rat(2, 7), Rat(3, 7)))
	m.AddEvidence("e2", rats(Rat(5, 11), Rat(1, 11), Rat(2, 11)))
	post, err := m.Posterior()
	if err != nil {
		t.Fatal(err)
	}
	want := enumeratePosterior(
		rats(Rat(1, 3), Rat(1, 3), Rat(1, 3)),
		[][]*big.Rat{rats(Rat(1, 7), Rat(2, 7), Rat(3, 7)), rats(Rat(5, 11), Rat(1, 11), Rat(2, 11))},
	)
	for h := range want {
		if post[h].Cmp(want[h]) != 0 {
			t.Fatalf("enumeration mismatch at h=%d: %s vs %s", h, post[h], want[h])
		}
	}
	// Unnormalized weights: (5, 2, 6) / 13.
	eq(t, post[0], 5, 13)
	eq(t, post[1], 2, 13)
	eq(t, post[2], 6, 13)
}

func TestDuplicateEvidenceCountedOnce(t *testing.T) {
	m, err := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	added, err := m.AddEvidence("e", rats(Rat(3, 4), Rat(1, 4)))
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	// Duplicate id with different numbers must be ignored.
	added, err = m.AddEvidence("e", rats(Rat(9, 10), Rat(1, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Fatal("duplicate evidence was applied twice")
	}
	post, err := m.Posterior()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, post[0], 3, 4)
	eq(t, post[1], 1, 4)
}

func TestRetractRestoresPrior(t *testing.T) {
	m, err := NewModel(rats(Rat(1, 3), Rat(2, 3)))
	if err != nil {
		t.Fatal(err)
	}
	m.AddEvidence("a", rats(Rat(1, 5), Rat(4, 5)))
	m.AddEvidence("b", rats(Rat(7, 8), Rat(1, 8)))
	if !m.Retract("a") {
		t.Fatal("retract(a) reported missing")
	}
	post, err := m.Posterior()
	if err != nil {
		t.Fatal(err)
	}
	want := enumeratePosterior(rats(Rat(1, 3), Rat(2, 3)), [][]*big.Rat{rats(Rat(7, 8), Rat(1, 8))})
	for h := range want {
		if post[h].Cmp(want[h]) != 0 {
			t.Fatalf("after retract: h=%d got %s want %s", h, post[h], want[h])
		}
	}
	if !m.Retract("b") {
		t.Fatal("retract(b) reported missing")
	}
	post, err = m.Posterior()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, post[0], 1, 3)
	eq(t, post[1], 2, 3)
	if m.Retract("b") {
		t.Fatal("double retract reported present")
	}
}

func TestContradiction(t *testing.T) {
	m, err := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	m.AddEvidence("all-zero", rats(Rat(0, 1), Rat(0, 1)))
	if _, err := m.Posterior(); err != ErrContradiction {
		t.Fatalf("got %v, want ErrContradiction", err)
	}
	// Mutually exclusive groups also contradict.
	m2, _ := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	m2.AddEvidence("only-h0", rats(Rat(1, 1), Rat(0, 1)))
	m2.AddEvidence("only-h1", rats(Rat(0, 1), Rat(1, 1)))
	if _, err := m2.Posterior(); err != ErrContradiction {
		t.Fatalf("got %v, want ErrContradiction", err)
	}
	// Retracting one group resolves the contradiction.
	m2.Retract("only-h1")
	post, err := m2.Posterior()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, post[0], 1, 1)
	eq(t, post[1], 0, 1)
}

func TestBestObservation(t *testing.T) {
	m, err := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	informative := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(9, 10), Rat(1, 5)),
		rats(Rat(1, 10), Rat(4, 5)),
	}}
	noisy := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(3, 5), Rat(1, 2)),
		rats(Rat(2, 5), Rat(1, 2)),
	}}
	best, minErr, err := m.BestObservation([]Observation{noisy, informative})
	if err != nil {
		t.Fatal(err)
	}
	if best != 1 {
		t.Fatalf("best = %d, want 1", best)
	}
	// 1 - (max(9/20,1/10) + max(1/20,2/5)) = 1 - 17/20 = 3/20.
	eq(t, minErr, 3, 20)
	// Cross-check both candidates by joint enumeration.
	post, _ := m.Posterior()
	for i, c := range []Observation{noisy, informative} {
		got, err := ExpectedError(post, c)
		if err != nil {
			t.Fatal(err)
		}
		if want := enumerateError(post, c); got.Cmp(want) != 0 {
			t.Fatalf("candidate %d: got %s, enumeration wants %s", i, got, want)
		}
	}
}

func TestBestObservationTieBreaksByIndex(t *testing.T) {
	m, err := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	a := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(3, 4), Rat(1, 4)),
		rats(Rat(1, 4), Rat(3, 4)),
	}}
	b := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(1, 4), Rat(3, 4)),
		rats(Rat(3, 4), Rat(1, 4)),
	}}
	best, minErr, err := m.BestObservation([]Observation{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if best != 0 {
		t.Fatalf("tie broke to index %d, want 0", best)
	}
	eq(t, minErr, 1, 4)
}

func TestBestObservationUsesPosterior(t *testing.T) {
	// After evidence, the ranking of candidates can flip.
	m, err := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	m.AddEvidence("e", rats(Rat(9, 10), Rat(1, 10))) // posterior (9/10, 1/10)
	testH0 := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(19, 20), Rat(1, 20)),
		rats(Rat(1, 20), Rat(19, 20)),
	}}
	useless := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(1, 2), Rat(1, 2)),
		rats(Rat(1, 2), Rat(1, 2)),
	}}
	best, minErr, err := m.BestObservation([]Observation{useless, testH0})
	if err != nil {
		t.Fatal(err)
	}
	if best != 1 {
		t.Fatalf("best = %d, want 1", best)
	}
	eq(t, minErr, 1, 20)
	post, _ := m.Posterior()
	if want := enumerateError(post, testH0); minErr.Cmp(want) != 0 {
		t.Fatalf("got %s, enumeration wants %s", minErr, want)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewModel(nil); err == nil {
		t.Fatal("empty prior accepted")
	}
	if _, err := NewModel(rats(Rat(0, 1), Rat(0, 1))); err == nil {
		t.Fatal("zero prior accepted")
	}
	if _, err := NewModel(rats(Rat(-1, 2), Rat(3, 2))); err == nil {
		t.Fatal("negative prior accepted")
	}
	m, _ := NewModel(rats(Rat(1, 2), Rat(1, 2)))
	if _, err := m.AddEvidence("bad-len", rats(Rat(1, 1))); err == nil {
		t.Fatal("wrong-length likelihood accepted")
	}
	if _, err := m.AddEvidence("neg", rats(Rat(-1, 2), Rat(1, 2))); err == nil {
		t.Fatal("negative likelihood accepted")
	}
	badObs := Observation{OutcomeProb: [][]*big.Rat{
		rats(Rat(1, 1), Rat(1, 1)),
		rats(Rat(1, 1), Rat(1, 1)),
	}}
	if _, _, err := m.BestObservation([]Observation{badObs}); err == nil {
		t.Fatal("outcome probabilities not summing to 1 accepted")
	}
}
