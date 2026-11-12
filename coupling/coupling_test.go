package coupling

import (
	"math/big"
	"testing"
)

func rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

func rats(ws ...*big.Rat) []*big.Rat { return ws }

func allEdges(nL, nR int) [][2]int {
	var edges [][2]int
	for i := 0; i < nL; i++ {
		for j := 0; j < nR; j++ {
			edges = append(edges, [2]int{i, j})
		}
	}
	return edges
}

func mustSolve(t *testing.T, p *Problem) *Result {
	t.Helper()
	r, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := Verify(p, r); err != nil {
		t.Fatalf("Verify rejected solver output: %v", err)
	}
	return r
}

// bruteForceMinCut enumerates every finite cut of the flow network and
// returns the minimum cut capacity. It is an independent reference for
// the max-flow min-cut value on tiny problems.
func bruteForceMinCut(p *Problem) *big.Rat {
	nL, nR := len(p.Left), len(p.Right)
	best := new(big.Rat).Add(p.TotalLeft(), p.TotalRight())
	for maskL := 0; maskL < 1<<nL; maskL++ {
		for maskR := 0; maskR < 1<<nR; maskR++ {
			finite := true
			for _, e := range p.Edges {
				if maskL>>e[0]&1 == 1 && maskR>>e[1]&1 == 0 {
					finite = false
					break
				}
			}
			if !finite {
				continue
			}
			cap := new(big.Rat)
			for i := 0; i < nL; i++ {
				if maskL>>i&1 == 0 {
					cap.Add(cap, p.Left[i])
				}
			}
			for j := 0; j < nR; j++ {
				if maskR>>j&1 == 1 {
					cap.Add(cap, p.Right[j])
				}
			}
			if cap.Cmp(best) < 0 {
				best = cap
			}
		}
	}
	return best
}

// checkConservation asserts exact marginal conservation: every left
// point's outflow plus its leftover equals its prescribed weight, the
// leftovers sum to the reported unpaired masses, and the paired mass
// equals the independent brute-force min-cut value.
func checkConservation(t *testing.T, p *Problem, r *Result) {
	t.Helper()
	nL, nR := len(p.Left), len(p.Right)
	leftOut := make([]*big.Rat, nL)
	rightIn := make([]*big.Rat, nR)
	for i := range leftOut {
		leftOut[i] = new(big.Rat)
	}
	for j := range rightIn {
		rightIn[j] = new(big.Rat)
	}
	paired := new(big.Rat)
	for _, ef := range r.Flows {
		leftOut[ef.Left].Add(leftOut[ef.Left], ef.Amount)
		rightIn[ef.Right].Add(rightIn[ef.Right], ef.Amount)
		paired.Add(paired, ef.Amount)
	}
	if paired.Cmp(r.Paired) != 0 {
		t.Fatalf("paired mass %s != flow sum %s", r.Paired, paired)
	}
	unpairedL := new(big.Rat)
	unpairedR := new(big.Rat)
	for i := 0; i < nL; i++ {
		leftover := new(big.Rat).Sub(p.Left[i], leftOut[i])
		if leftover.Sign() < 0 {
			t.Fatalf("left point %d: outflow %s exceeds weight %s", i, leftOut[i], p.Left[i])
		}
		unpairedL.Add(unpairedL, leftover)
	}
	for j := 0; j < nR; j++ {
		leftover := new(big.Rat).Sub(p.Right[j], rightIn[j])
		if leftover.Sign() < 0 {
			t.Fatalf("right point %d: inflow %s exceeds weight %s", j, rightIn[j], p.Right[j])
		}
		unpairedR.Add(unpairedR, leftover)
	}
	if unpairedL.Cmp(r.UnpairedLeft) != 0 {
		t.Fatalf("unpaired left %s != conserved leftover %s", r.UnpairedLeft, unpairedL)
	}
	if unpairedR.Cmp(r.UnpairedRight) != 0 {
		t.Fatalf("unpaired right %s != conserved leftover %s", r.UnpairedRight, unpairedR)
	}
	if opt := bruteForceMinCut(p); opt.Cmp(r.Paired) != 0 {
		t.Fatalf("paired mass %s != brute-force min cut %s", r.Paired, opt)
	}
}

func TestNoForcedNormalization(t *testing.T) {
	// Both sides are sub-distributions of total mass 1/2. A solver that
	// secretly normalized would report Paired == 1.
	p, err := NewProblem(
		rats(rat(1, 4), rat(1, 4)),
		rats(rat(1, 4), rat(1, 4)),
		allEdges(2, 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	r := mustSolve(t, p)
	if r.Paired.Cmp(rat(1, 2)) != 0 {
		t.Fatalf("Paired = %s, want 1/2 (weights must not be normalized)", r.Paired)
	}
	if r.UnpairedLeft.Sign() != 0 || r.UnpairedRight.Sign() != 0 {
		t.Fatalf("unpaired = (%s, %s), want (0, 0)", r.UnpairedLeft, r.UnpairedRight)
	}
}

func TestUnequalTotalMass(t *testing.T) {
	p, err := NewProblem(
		rats(rat(1, 2), rat(1, 2)),
		rats(rat(1, 4), rat(1, 4)),
		allEdges(2, 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	r := mustSolve(t, p)
	if r.Paired.Cmp(rat(1, 2)) != 0 {
		t.Fatalf("Paired = %s, want 1/2", r.Paired)
	}
	if r.UnpairedLeft.Cmp(rat(1, 2)) != 0 || r.UnpairedRight.Sign() != 0 {
		t.Fatalf("unpaired = (%s, %s), want (1/2, 0)", r.UnpairedLeft, r.UnpairedRight)
	}
}

func TestHallDeficiencyCut(t *testing.T) {
	// Both left points may only pair with right point 0, so at most 1/2
	// can be paired; the cut must exhibit the bottleneck.
	p, err := NewProblem(
		rats(rat(1, 2), rat(1, 2)),
		rats(rat(1, 2), rat(1, 2)),
		[][2]int{{0, 0}, {1, 0}},
	)
	if err != nil {
		t.Fatal(err)
	}
	r := mustSolve(t, p)
	if r.Paired.Cmp(rat(1, 2)) != 0 {
		t.Fatalf("Paired = %s, want 1/2", r.Paired)
	}
	if !r.Cut.LeftInS[0] || !r.Cut.LeftInS[1] || !r.Cut.RightInS[0] || r.Cut.RightInS[1] {
		t.Fatalf("unexpected cut witness %+v", r.Cut)
	}
}

func TestIdentityCoupling(t *testing.T) {
	p, err := NewProblem(
		rats(rat(1, 3), rat(1, 3), rat(1, 3)),
		rats(rat(1, 3), rat(1, 3), rat(1, 3)),
		[][2]int{{0, 0}, {1, 1}, {2, 2}},
	)
	if err != nil {
		t.Fatal(err)
	}
	r := mustSolve(t, p)
	if !p.IsComplete(r) {
		t.Fatalf("expected complete coupling, paired = %s", r.Paired)
	}
	for _, ef := range r.Flows {
		if ef.Amount.Cmp(rat(1, 3)) != 0 {
			t.Fatalf("edge (%d,%d) flow %s, want 1/3", ef.Left, ef.Right, ef.Amount)
		}
	}
}

func TestNoEdges(t *testing.T) {
	p, err := NewProblem(rats(rat(1, 1)), rats(rat(1, 1)), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := mustSolve(t, p)
	if r.Paired.Sign() != 0 {
		t.Fatalf("Paired = %s, want 0", r.Paired)
	}
	if r.UnpairedLeft.Cmp(rat(1, 1)) != 0 || r.UnpairedRight.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("unpaired = (%s, %s), want (1, 1)", r.UnpairedLeft, r.UnpairedRight)
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := NewProblem(rats(rat(-1, 2)), rats(rat(1, 2)), nil); err == nil {
		t.Fatal("negative weight accepted")
	}
	if _, err := NewProblem(rats(rat(1, 2)), rats(rat(1, 2)), [][2]int{{0, 0}, {0, 0}}); err == nil {
		t.Fatal("duplicate edge accepted")
	}
	if _, err := NewProblem(rats(rat(1, 2)), rats(rat(1, 2)), [][2]int{{0, 1}}); err == nil {
		t.Fatal("out-of-range edge accepted")
	}
	if _, err := NewProblem(nil, rats(rat(1, 2)), nil); err == nil {
		t.Fatal("empty support accepted")
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	p, err := NewProblem(
		rats(rat(1, 2), rat(1, 2)),
		rats(rat(1, 2), rat(1, 2)),
		[][2]int{{0, 0}, {1, 0}},
	)
	if err != nil {
		t.Fatal(err)
	}
	r := mustSolve(t, p)

	// Inflate one edge flow beyond the marginals.
	bad := *r
	bad.Flows = append([]EdgeFlow(nil), r.Flows...)
	bad.Flows[0].Amount = rat(3, 4)
	if err := Verify(p, &bad); err == nil {
		t.Fatal("Verify accepted inflated flow")
	}

	// Misreport the paired mass.
	bad = *r
	bad.Paired = rat(1, 1)
	if err := Verify(p, &bad); err == nil {
		t.Fatal("Verify accepted wrong paired mass")
	}

	// Replace the cut with one that does not certify the flow value.
	bad = *r
	bad.Cut = Cut{LeftInS: []bool{false, false}, RightInS: []bool{false, false}}
	if err := Verify(p, &bad); err == nil {
		t.Fatal("Verify accepted non-certifying cut")
	}
}

// TestExhaustiveTwoByTwo enumerates every pair of distributions over
// two points with weights in {0, 1/2, 1} and every subset of the four
// possible edges, checking marginal conservation and optimality against
// a brute-force min-cut reference.
func TestExhaustiveTwoByTwo(t *testing.T) {
	weights := []*big.Rat{rat(0, 1), rat(1, 2), rat(1, 1)}
	exhaustive(t, 2, 2, weights)
}

// TestExhaustiveThreeByThree enumerates every pair of distributions
// over three points with weights in {0, 1/2} and every subset of the
// nine possible edges.
func TestExhaustiveThreeByThree(t *testing.T) {
	weights := []*big.Rat{rat(0, 1), rat(1, 2)}
	exhaustive(t, 3, 3, weights)
}

func exhaustive(t *testing.T, nL, nR int, weights []*big.Rat) {
	t.Helper()
	nW := len(weights)
	nEdges := nL * nR
	total := 0
	for dl := 0; ; dl++ {
		left, ok := decodeDist(dl, nL, nW, weights)
		if !ok {
			break
		}
		for dr := 0; ; dr++ {
			right, ok := decodeDist(dr, nR, nW, weights)
			if !ok {
				break
			}
			for mask := 0; mask < 1<<nEdges; mask++ {
				var edges [][2]int
				for k := 0; k < nEdges; k++ {
					if mask>>k&1 == 1 {
						edges = append(edges, [2]int{k / nR, k % nR})
					}
				}
				p, err := NewProblem(left, right, edges)
				if err != nil {
					t.Fatalf("NewProblem(%v, %v, %v): %v", left, right, edges, err)
				}
				r, err := Solve(p)
				if err != nil {
					t.Fatalf("Solve(%v, %v, %v): %v", left, right, edges, err)
				}
				if err := Verify(p, r); err != nil {
					t.Fatalf("Verify(%v, %v, %v): %v", left, right, edges, err)
				}
				checkConservation(t, p, r)
				total++
			}
		}
	}
	t.Logf("verified %d problems", total)
}

// decodeDist maps code in [0, nW^n) to a distribution of n weights.
func decodeDist(code, n, nW int, weights []*big.Rat) ([]*big.Rat, bool) {
	if code >= pow(nW, n) {
		return nil, false
	}
	d := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		d[i] = weights[code%nW]
		code /= nW
	}
	return d, true
}

func pow(base, exp int) int {
	n := 1
	for i := 0; i < exp; i++ {
		n *= base
	}
	return n
}
