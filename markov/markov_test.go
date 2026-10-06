package markov

import (
	"math/big"
	"strings"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return v
}

func checkResult(t *testing.T, got Result, hitProb, condExp string, status Status) {
	t.Helper()
	if got.Status != status {
		t.Errorf("status = %v, want %v", got.Status, status)
	}
	if got.HitProb.Cmp(rat(t, hitProb)) != 0 {
		t.Errorf("HitProb = %s, want %s", got.HitProb.RatString(), hitProb)
	}
	if condExp == "" {
		if got.ConditionalExpectedSteps != nil {
			t.Errorf("ConditionalExpectedSteps = %s, want nil", got.ConditionalExpectedSteps.RatString())
		}
		return
	}
	if got.ConditionalExpectedSteps == nil {
		t.Errorf("ConditionalExpectedSteps = nil, want %s", condExp)
		return
	}
	if got.ConditionalExpectedSteps.Cmp(rat(t, condExp)) != 0 {
		t.Errorf("ConditionalExpectedSteps = %s, want %s", got.ConditionalExpectedSteps.RatString(), condExp)
	}
}

func mustChain(t *testing.T, lines []string) *Chain {
	t.Helper()
	p, err := ParseMatrix(lines)
	if err != nil {
		t.Fatalf("ParseMatrix: %v", err)
	}
	c, err := NewChain(p)
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}
	return c
}

// Geometric waiting time: state 0 flips a fair coin until moving to the
// absorbing state 1. h = 1, E[T] = 2.
func TestCoinFlip(t *testing.T) {
	c := mustChain(t, []string{
		"1/2 1/2",
		"0 1",
	})
	res, err := c.Analyze([]int{1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "1", "2", CertainAbsorption)
	checkResult(t, res[1], "1", "0", CertainAbsorption)
}

// Closed non-absorbing class {2,3} (period-2 cycle). State 1 can hit the
// target 0, loop on itself, or fall into the closed class forever.
//
//	h_1 = 1/4 + 1/4 h_1            => h_1 = 1/3
//	g_1 = h_1 + 1/4 g_1            => g_1 = 4/9
//	E[T|T<inf] = g_1/h_1 = 4/3
//
// Cross-check: P(T=t, hit) = (1/4)^t, so E[T|hit] = 3 * sum t (1/4)^t = 4/3.
func TestClosedNonAbsorbingClass(t *testing.T) {
	c := mustChain(t, []string{
		"1 0 0 0",
		"1/4 1/4 1/2 0",
		"0 0 0 1",
		"0 0 1 0",
	})
	res, err := c.Analyze([]int{0})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "1", "0", CertainAbsorption)
	checkResult(t, res[1], "1/3", "4/3", InfiniteExpectation)
	checkResult(t, res[2], "0", "", NeverReaches)
	checkResult(t, res[3], "0", "", NeverReaches)
}

// Loop-back chain: 0 -> 1 (1/3), 0 -> 2 (2/3); 1 -> 0; 2 absorbing.
//
//	e_0 = 1 + (1/3) e_1, e_1 = 1 + e_0  =>  e_0 = 2, e_1 = 3
func TestLoopBack(t *testing.T) {
	c := mustChain(t, []string{
		"0 1/3 2/3",
		"1 0 0",
		"0 0 1",
	})
	res, err := c.Analyze([]int{2})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "1", "2", CertainAbsorption)
	checkResult(t, res[1], "1", "3", CertainAbsorption)
	checkResult(t, res[2], "1", "0", CertainAbsorption)
}

// Two absorbing states; every transient path takes exactly one step from
// state 1 and two steps from state 3.
func TestMultipleAbsorbingStates(t *testing.T) {
	c := mustChain(t, []string{
		"1 0 0 0",
		"1/3 0 2/3 0",
		"0 0 1 0",
		"0 1 0 0",
	})
	res, err := c.Analyze([]int{0, 2})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "1", "0", CertainAbsorption)
	checkResult(t, res[1], "1", "1", CertainAbsorption)
	checkResult(t, res[2], "1", "0", CertainAbsorption)
	checkResult(t, res[3], "1", "2", CertainAbsorption)
}

// Target unreachable from state 0 (both states are absorbing singletons).
func TestUnreachableTarget(t *testing.T) {
	c := mustChain(t, []string{
		"1 0",
		"0 1",
	})
	res, err := c.Analyze([]int{1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "0", "", NeverReaches)
	checkResult(t, res[1], "1", "0", CertainAbsorption)
}

// Rational (non-dyadic) probabilities: 0 -> 0 (2/3), 0 -> 1 (1/3).
// E[T] = 1/(1/3) = 3; h = 1.
func TestRationalProbabilities(t *testing.T) {
	c := mustChain(t, []string{
		"2/3 1/3",
		"0 1",
	})
	res, err := c.Analyze([]int{1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "1", "3", CertainAbsorption)
	checkResult(t, res[1], "1", "0", CertainAbsorption)
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"row sum not one", []string{"1/2 1/3", "0 1"}, "sums to"},
		{"negative entry", []string{"-1/2 3/2", "0 1"}, "negative"},
		{"entry above one", []string{"3/2 -1/2", "0 1"}, "exceeds 1"},
		{"not square", []string{"1 0 0", "0 1 0"}, "square"},
		{"bad token", []string{"a b", "0 1"}, "cannot parse"},
	}
	for _, tc := range cases {
		p, perr := ParseMatrix(tc.lines)
		if perr != nil {
			if !strings.Contains(perr.Error(), tc.want) {
				t.Errorf("%s: error %q does not mention %q", tc.name, perr, tc.want)
			}
			continue
		}
		_, err := NewChain(p)
		if err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
	}

	if _, err := NewChain(nil); err == nil {
		t.Error("empty matrix: expected error")
	}
	row := []*big.Rat{nil, big.NewRat(1, 1)}
	if _, err := NewChain([][]*big.Rat{row, {big.NewRat(0, 1), big.NewRat(1, 1)}}); err == nil {
		t.Error("nil entry: expected error")
	}
}

func TestAnalyzeBadAbsorbingSet(t *testing.T) {
	c := mustChain(t, []string{"1 0", "0 1"})
	if _, err := c.Analyze(nil); err == nil {
		t.Error("empty absorbing set: expected error")
	}
	if _, err := c.Analyze([]int{2}); err == nil {
		t.Error("out-of-range absorbing state: expected error")
	}
	if _, err := c.Analyze([]int{-1}); err == nil {
		t.Error("negative absorbing state: expected error")
	}
}

// Exactness check: 1/3-type probabilities must stay exact, not round to
// floats. h_1 = 1/7 for the system h = 1/7 + (6/7)(1/6)h ... construct
// directly: 0 -> 1 w.p. 6/7 (closed class {1,2}), 0 -> 3 w.p. 1/7 (target).
func TestExactRationalArithmetic(t *testing.T) {
	c := mustChain(t, []string{
		"0 6/7 0 1/7",
		"0 0 1 0",
		"0 1 0 0",
		"0 0 0 1",
	})
	res, err := c.Analyze([]int{3})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	checkResult(t, res[0], "1/7", "1", InfiniteExpectation)
	checkResult(t, res[1], "0", "", NeverReaches)
	checkResult(t, res[2], "0", "", NeverReaches)
	checkResult(t, res[3], "1", "0", CertainAbsorption)
	if res[0].HitProb.RatString() != "1/7" {
		t.Errorf("HitProb not exact: %s", res[0].HitProb.RatString())
	}
}
