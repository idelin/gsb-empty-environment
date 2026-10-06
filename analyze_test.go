package markov

import (
	"math/big"
	"reflect"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func checkRat(t *testing.T, name string, got, want *big.Rat) {
	t.Helper()
	if got == nil || got.Cmp(want) != 0 {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// substituteCheck plugs the returned h and g back into the defining
// linear equations on every non-absorbing state:
//
//	h_i = sum_j p_ij h_j
//	g_i = h_i + sum_j p_ij g_j   (g = E[T * 1{hit}])
//
// and verifies conditional = g_i / h_i wherever h_i > 0.
func substituteCheck(t *testing.T, c *Chain, absorbing []int, an *Analysis) {
	t.Helper()
	inA := make([]bool, c.N)
	for _, s := range absorbing {
		inA[s] = true
	}
	h := make([]*big.Rat, c.N)
	g := make([]*big.Rat, c.N)
	for i := 0; i < c.N; i++ {
		h[i] = an.Results[i].HitProbability
		g[i] = new(big.Rat)
		if an.Results[i].ConditionalExpectedSteps != nil {
			g[i].Mul(an.Results[i].ConditionalExpectedSteps, h[i])
		}
	}
	for i := 0; i < c.N; i++ {
		if inA[i] {
			checkRat(t, "h on absorbing state", h[i], rat("1"))
			checkRat(t, "conditional steps on absorbing state",
				an.Results[i].ConditionalExpectedSteps, rat("0"))
			continue
		}
		rhsH := new(big.Rat)
		rhsG := new(big.Rat).Set(h[i])
		for j := 0; j < c.N; j++ {
			rhsH.Add(rhsH, new(big.Rat).Mul(c.P[i][j], h[j]))
			rhsG.Add(rhsG, new(big.Rat).Mul(c.P[i][j], g[j]))
		}
		if h[i].Cmp(rhsH) != 0 {
			t.Errorf("state %d: h=%s does not satisfy h=Ph (rhs %s)", i, h[i], rhsH)
		}
		if g[i].Cmp(rhsG) != 0 {
			t.Errorf("state %d: g=%s does not satisfy g=h+Pg (rhs %s)", i, g[i], rhsG)
		}
	}
}

// TestAllAbsorbed: two transient states, absorption certain.
//
//	0 -> 0 (1/2), 0 -> 1 (1/2)
//	1 -> 0 (1/2), 1 -> 2 (1/2)
//	2 absorbing
//
// Hand computation: h = (1,1,1); E_0 = 1 + E_0/2 + E_1/2,
// E_1 = 1 + E_0/2  =>  E_0 = 6, E_1 = 4.
func TestAllAbsorbed(t *testing.T) {
	c, err := ParseChain([][]string{
		{"1/2", "1/2", "0"},
		{"1/2", "0", "1/2"},
		{"0", "0", "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	an, err := c.Analyze([]int{2})
	if err != nil {
		t.Fatal(err)
	}
	wantH := []string{"1", "1", "1"}
	wantCond := []string{"6", "4", "0"}
	for i := range wantH {
		checkRat(t, "hit prob", an.Results[i].HitProbability, rat(wantH[i]))
		checkRat(t, "conditional steps", an.Results[i].ConditionalExpectedSteps, rat(wantCond[i]))
		v, inf := an.Results[i].UnconditionalExpectedSteps()
		if inf {
			t.Errorf("state %d: unconditional expectation should be finite", i)
		}
		checkRat(t, "unconditional steps", v, rat(wantCond[i]))
	}
	if len(an.ClosedClasses) != 0 {
		t.Errorf("expected no closed non-absorbing classes, got %v", an.ClosedClasses)
	}
	substituteCheck(t, c, []int{2}, an)
}

// TestClosedClass: a closed class {1,2} disjoint from the absorbing set.
//
//	0 -> 1 (1/2), 0 -> 3 (1/2)
//	1 -> 2 (1), 2 -> 1 (1)   closed, never reaches 3
//	3 absorbing
//
// Hand computation: h_0 = 1/2, h_1 = h_2 = 0. From 0, conditioned on
// absorption the chain moves to 3 immediately, so E = 1.
func TestClosedClass(t *testing.T) {
	c, err := ParseChain([][]string{
		{"0", "1/2", "0", "1/2"},
		{"0", "0", "1", "0"},
		{"0", "1", "0", "0"},
		{"0", "0", "0", "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	an, err := c.Analyze([]int{3})
	if err != nil {
		t.Fatal(err)
	}
	checkRat(t, "h_0", an.Results[0].HitProbability, rat("1/2"))
	checkRat(t, "cond E_0", an.Results[0].ConditionalExpectedSteps, rat("1"))
	for _, i := range []int{1, 2} {
		checkRat(t, "h on trapped state", an.Results[i].HitProbability, rat("0"))
		if an.Results[i].ConditionalExpectedSteps != nil {
			t.Errorf("state %d: conditional expectation must be nil (never reaches)", i)
		}
	}
	if !reflect.DeepEqual(an.ClosedClasses, [][]int{{1, 2}}) {
		t.Errorf("closed classes: got %v, want [[1 2]]", an.ClosedClasses)
	}
	if _, inf := an.Results[0].UnconditionalExpectedSteps(); !inf {
		t.Error("state 0: unconditional expectation must be infinite (h < 1)")
	}
	if _, inf := an.Results[1].UnconditionalExpectedSteps(); !inf {
		t.Error("state 1: unconditional expectation must be infinite (h = 0)")
	}
	substituteCheck(t, c, []int{3}, an)
}

// TestPartialHit: 0 < h < 1 with a non-trivial conditional expectation.
//
//	0 -> 1 (1/3), 0 -> 2 (1/3), 0 -> 3 (1/3)
//	1 -> 0 (1)
//	2 -> 2 (1)   closed singleton, never reaches 3
//	3 absorbing
//
// Hand computation: h_0 = h_0/3 + 1/3 => h_0 = 1/2 = h_1.
// g_0 = 1/2 + g_1/3, g_1 = 1/2 + g_0 => g_0 = 1, g_1 = 3/2.
// Conditional: E_0 = 1/(1/2) = 2, E_1 = (3/2)/(1/2) = 3.
func TestPartialHit(t *testing.T) {
	c, err := ParseChain([][]string{
		{"0", "1/3", "1/3", "1/3"},
		{"1", "0", "0", "0"},
		{"0", "0", "1", "0"},
		{"0", "0", "0", "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	an, err := c.Analyze([]int{3})
	if err != nil {
		t.Fatal(err)
	}
	checkRat(t, "h_0", an.Results[0].HitProbability, rat("1/2"))
	checkRat(t, "h_1", an.Results[1].HitProbability, rat("1/2"))
	checkRat(t, "h_2", an.Results[2].HitProbability, rat("0"))
	checkRat(t, "cond E_0", an.Results[0].ConditionalExpectedSteps, rat("2"))
	checkRat(t, "cond E_1", an.Results[1].ConditionalExpectedSteps, rat("3"))
	if an.Results[2].ConditionalExpectedSteps != nil {
		t.Error("state 2: conditional expectation must be nil (never reaches)")
	}
	if !reflect.DeepEqual(an.ClosedClasses, [][]int{{2}}) {
		t.Errorf("closed classes: got %v, want [[2]]", an.ClosedClasses)
	}
	substituteCheck(t, c, []int{3}, an)
}

// TestNonAbsorbingTarget: the target state is not absorbing in the
// chain; the target set is still treated as absorbing for the hitting
// questions, so starts inside it have T = 0.
//
//	0 -> 1 (1/4), 0 -> 2 (3/4)
//	1 -> 0 (1/2), 1 -> 2 (1/2)
//	2 -> 0 (1)
//
// Target {2}: the whole chain is one closed class containing the
// target, so h = (1,1,1). E_0 = 1 + E_1/4, E_1 = 1 + E_0/2 give
// E_0 = 10/7, E_1 = 12/7, and E_2 = 0 (already in the target).
func TestNonAbsorbingTarget(t *testing.T) {
	c, err := ParseChain([][]string{
		{"0", "1/4", "3/4"},
		{"1/2", "0", "1/2"},
		{"1", "0", "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	an, err := c.Analyze([]int{2})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		checkRat(t, "hit prob", an.Results[i].HitProbability, rat("1"))
	}
	checkRat(t, "cond E_0", an.Results[0].ConditionalExpectedSteps, rat("10/7"))
	checkRat(t, "cond E_1", an.Results[1].ConditionalExpectedSteps, rat("12/7"))
	checkRat(t, "cond E_2", an.Results[2].ConditionalExpectedSteps, rat("0"))
	if len(an.ClosedClasses) != 0 {
		t.Errorf("expected no closed non-absorbing classes, got %v", an.ClosedClasses)
	}
	substituteCheck(t, c, []int{2}, an)
}

func TestValidation(t *testing.T) {
	if _, err := ParseChain([][]string{{"1/2", "1/2"}, {"1/2", "1/2"}, {"0", "1"}}); err == nil {
		t.Error("expected error for non-square matrix")
	}
	if _, err := ParseChain([][]string{{"1/2", "1/2"}, {"1/2", "3/5"}}); err == nil {
		t.Error("expected error for row not summing to 1")
	}
	if _, err := ParseChain([][]string{{"3/2", "-1/2"}, {"0", "1"}}); err == nil {
		t.Error("expected error for entry outside [0,1]")
	}
	if _, err := ParseChain([][]string{{"a", "1"}, {"0", "1"}}); err == nil {
		t.Error("expected error for unparseable entry")
	}
	c, err := ParseChain([][]string{{"1/2", "1/2"}, {"0", "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Analyze(nil); err == nil {
		t.Error("expected error for empty absorbing set")
	}
	if _, err := c.Analyze([]int{2}); err == nil {
		t.Error("expected error for out-of-range absorbing state")
	}
	if _, err := c.Analyze([]int{1, 1}); err == nil {
		t.Error("expected error for duplicate absorbing state")
	}
}
