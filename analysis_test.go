package main

import (
	"fmt"
	"math/big"
	"strconv"
	"testing"
)

func mustChain(t *testing.T, n int, absorbing []int, edges [][3]string) *Chain {
	t.Helper()
	abs := make([]bool, n)
	for _, s := range absorbing {
		abs[s] = true
	}
	c := NewChain(n, abs)
	for _, e := range edges {
		var i, j int
		if _, err := fmt.Sscanf(e[0], "%d", &i); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Sscanf(e[1], "%d", &j); err != nil {
			t.Fatal(err)
		}
		p, ok := new(big.Rat).SetString(e[2])
		if !ok {
			t.Fatalf("bad rational %q", e[2])
		}
		c.P[i][j] = p
	}
	return c
}

func allChecksPass(t *testing.T, a *Analysis) {
	t.Helper()
	for _, chk := range a.Verification {
		if !chk.OK {
			t.Errorf("verification %q failed: %s", chk.Name, chk.Detail)
		}
	}
}

func findClass(a *Analysis, state int) int {
	for _, cl := range a.ClosedClasses {
		for _, s := range cl.States {
			if s == state {
				return cl.ID
			}
		}
	}
	return -1
}

func TestAlreadyAbsorbedInitialState(t *testing.T) {
	// State 0 is absorbing; starting there means absorption is immediate.
	c := mustChain(t, 2, []int{0}, [][3]string{
		{"1", "0", "1"},
	})
	a := Analyze(c)
	allChecksPass(t, a)

	if got := a.States[0].ExpectedSteps; got != "0" {
		t.Errorf("absorbed start: expected steps = %s, want 0", got)
	}
	cl0 := findClass(a, 0)
	if got := a.States[0].Absorption[strconv.Itoa(cl0)]; got != "1" {
		t.Errorf("absorbed start: P(absorb into own class) = %s, want 1", got)
	}
	if got := a.States[1].ExpectedSteps; got != "1" {
		t.Errorf("state 1: expected steps = %s, want 1", got)
	}
}

func TestUnreachableAbsorbingState(t *testing.T) {
	// {0,1} is a non-absorbing closed class; state 2 is absorbing but
	// unreachable from 0 and 1.
	c := mustChain(t, 3, []int{2}, [][3]string{
		{"0", "1", "1"},
		{"1", "0", "1"},
	})
	a := Analyze(c)
	allChecksPass(t, a)

	cl2 := findClass(a, 2)
	if got := a.States[0].Absorption[strconv.Itoa(cl2)]; got != "0" {
		t.Errorf("unreachable absorbing state: P = %s, want 0", got)
	}
	if got := a.States[0].ExpectedSteps; got != "Infinity" {
		t.Errorf("state 0 expected steps = %s, want Infinity (closed class {0,1} is non-absorbing)", got)
	}
	// Reachability evidence: from 0 only class {0,1} is reachable.
	if len(a.States[0].Reachable) != 1 {
		t.Fatalf("state 0 reaches %d closed classes, want 1", len(a.States[0].Reachable))
	}
	ev := a.States[0].Reachable[0]
	if ev.ClassID == cl2 {
		t.Errorf("state 0 must not have a witness path to absorbing state 2, got %v", ev.Path)
	}
	// From state 2 itself, its own class is trivially reachable.
	found := false
	for _, ev := range a.States[2].Reachable {
		if ev.ClassID == cl2 {
			found = true
		}
	}
	if !found {
		t.Errorf("state 2 should reach its own absorbing class")
	}
}

func TestTinyButNonzeroProbabilityPath(t *testing.T) {
	// p = 1/10^40: far below float64 precision, must remain exact.
	p := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(40), nil))
	abs := []bool{false, true, true}
	c := NewChain(3, abs)
	c.P[0][1] = p
	c.P[0][2] = new(big.Rat).Sub(ratOne(), p)
	a := Analyze(c)
	allChecksPass(t, a)

	cl1 := findClass(a, 1)
	cl2 := findClass(a, 2)
	if got := a.States[0].Absorption[strconv.Itoa(cl1)]; got != p.RatString() {
		t.Errorf("tiny path probability = %s, want exact %s", got, p.RatString())
	}
	want2 := new(big.Rat).Sub(ratOne(), p)
	if got := a.States[0].Absorption[strconv.Itoa(cl2)]; got != want2.RatString() {
		t.Errorf("complement probability = %s, want %s", got, want2.RatString())
	}
	if got := a.States[0].ExpectedSteps; got != "1" {
		t.Errorf("expected steps = %s, want 1", got)
	}
	// Witness path to class of state 1 must exist and use the tiny edge.
	var path []int
	for _, ev := range a.States[0].Reachable {
		if ev.ClassID == cl1 {
			path = ev.Path
		}
	}
	if len(path) != 2 || path[0] != 0 || path[1] != 1 {
		t.Errorf("witness path to tiny-probability class = %v, want [0 1]", path)
	}
}

func TestNonAbsorbingClosedClassBlocksFiniteExpectation(t *testing.T) {
	// 0 -> 1 (1/2), 0 -> 2 (1/2); 1 self-loops forever (non-absorbing
	// closed class); 2 -> 3; 3 absorbing. Absorption probabilities are
	// still solved exactly, but expectations across state 1 are infinite.
	c := mustChain(t, 4, []int{3}, [][3]string{
		{"0", "1", "1/2"},
		{"0", "2", "1/2"},
		{"1", "1", "1"},
		{"2", "3", "1"},
	})
	a := Analyze(c)
	allChecksPass(t, a)

	if got := a.States[0].ExpectedSteps; got != "Infinity" {
		t.Errorf("state 0 expected steps = %s, want Infinity", got)
	}
	if got := a.States[1].ExpectedSteps; got != "Infinity" {
		t.Errorf("state 1 expected steps = %s, want Infinity", got)
	}
	if got := a.States[2].ExpectedSteps; got != "1" {
		t.Errorf("state 2 expected steps = %s, want 1", got)
	}
	if got := a.States[3].ExpectedSteps; got != "0" {
		t.Errorf("state 3 expected steps = %s, want 0", got)
	}
	cl1 := findClass(a, 1)
	cl3 := findClass(a, 3)
	if got := a.States[0].Absorption[strconv.Itoa(cl1)]; got != "1/2" {
		t.Errorf("P(end in {1}) = %s, want 1/2", got)
	}
	if got := a.States[0].Absorption[strconv.Itoa(cl3)]; got != "1/2" {
		t.Errorf("P(absorb at 3) = %s, want 1/2", got)
	}
	// Class {1} must be reported as non-absorbing.
	for _, cl := range a.ClosedClasses {
		if cl.ID == cl1 && cl.Absorbing {
			t.Errorf("class {1} wrongly reported absorbing")
		}
	}
}

func TestSymmetricRandomWalk(t *testing.T) {
	// Classic chain: absorbing 0 and 3; 1 <-> 2 with 1/2 each way.
	// Known answers: h_1(3) = 1/3, h_2(3) = 2/3, t_1 = t_2 = 2.
	c := mustChain(t, 4, []int{0, 3}, [][3]string{
		{"1", "0", "1/2"},
		{"1", "2", "1/2"},
		{"2", "1", "1/2"},
		{"2", "3", "1/2"},
	})
	a := Analyze(c)
	allChecksPass(t, a)

	cl3 := findClass(a, 3)
	if got := a.States[1].Absorption[strconv.Itoa(cl3)]; got != "1/3" {
		t.Errorf("h_1(3) = %s, want 1/3", got)
	}
	if got := a.States[2].Absorption[strconv.Itoa(cl3)]; got != "2/3" {
		t.Errorf("h_2(3) = %s, want 2/3", got)
	}
	if got := a.States[1].ExpectedSteps; got != "2" {
		t.Errorf("t_1 = %s, want 2", got)
	}
	if got := a.States[2].ExpectedSteps; got != "2" {
		t.Errorf("t_2 = %s, want 2", got)
	}
}

func TestSingularSystemNotSolved(t *testing.T) {
	// A singular coefficient matrix must be reported, never "solved".
	A := [][]*big.Rat{
		{ratOne(), ratOne()},
		{ratOne(), ratOne()},
	}
	b := []*big.Rat{ratOne(), ratOne()}
	if _, singular := solveLinear(A, b); !singular {
		t.Errorf("singular matrix not detected")
	}
	A[1][1] = ratZero()
	if x, singular := solveLinear(A, b); singular || x[0].Cmp(ratOne()) != 0 || x[1].Sign() != 0 {
		t.Errorf("solveLinear got (%v, %v), want ([1 0], false)", x, singular)
	}
}

func TestParseChainValidation(t *testing.T) {
	good := []byte(`{"states":2,"absorbing":[1],"transitions":[{"from":0,"to":1,"prob":"1/3"},{"from":0,"to":0,"prob":"2/3"}]}`)
	if _, err := ParseChain(good); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
	badRow := []byte(`{"states":2,"absorbing":[1],"transitions":[{"from":0,"to":1,"prob":"1/3"}]}`)
	if _, err := ParseChain(badRow); err == nil {
		t.Errorf("row not summing to 1 accepted")
	}
	badAbs := []byte(`{"states":2,"absorbing":[0],"transitions":[{"from":0,"to":1,"prob":"1"},{"from":1,"to":0,"prob":"1"}]}`)
	if _, err := ParseChain(badAbs); err == nil {
		t.Errorf("absorbing state with outgoing transitions accepted")
	}
}
