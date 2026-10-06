package main

import (
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func mustTerms(t *testing.T, ss ...string) []Term {
	t.Helper()
	terms, err := parseTerms(ss)
	if err != nil {
		t.Fatalf("parseTerms: %v", err)
	}
	return terms
}

// Expected values for TestTwelveMixed, confirmed by an independent int64
// depth-first enumeration of all 12! permutations.
const (
	wantTwelveMixedDev  = "1193/277200"
	wantTwelveMixedPerm = "0 1 2 4 5 6 3 7 8 9 10 11"
)

func ratString(r *big.Rat) string { return r.RatString() }

func permEqual(got []int, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A single 1/2 at p=0 must round half-even to 0 (and the first term is
// rounded too, not summed exactly first).
func TestSingleHalfAtP0(t *testing.T) {
	res, err := Solve(mustTerms(t, "1/2"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ratString(res.Final); got != "0" {
		t.Fatalf("final accumulator = %s, want 0", got)
	}
	if got := ratString(res.Deviation); got != "1/2" {
		t.Fatalf("deviation = %s, want 1/2", got)
	}
	if len(res.Steps) != 1 || ratString(res.Steps[0].Pre) != "1/2" || ratString(res.Steps[0].Post) != "0" {
		t.Fatalf("unexpected steps: %+v", res.Steps)
	}
}

// Positive and negative half-even tie boundaries.
func TestHalfEvenBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		p    int
		want string // final accumulator
	}{
		{"1/2", 0, "0"},      // 0.5 -> 0 (even)
		{"3/2", 0, "2"},      // 1.5 -> 2 (even)
		{"5/2", 0, "2"},      // 2.5 -> 2 (even)
		{"7/2", 0, "4"},      // 3.5 -> 4 (even)
		{"-1/2", 0, "0"},     // -0.5 -> 0 (even)
		{"-3/2", 0, "-2"},    // -1.5 -> -2
		{"-5/2", 0, "-2"},    // -2.5 -> -2
		{"-7/2", 0, "-4"},    // -3.5 -> -4
		{"1/20", 1, "0"},     // 0.05 -> 0.0
		{"3/20", 1, "1/5"},   // 0.15 -> 0.2
		{"1/4", 1, "1/5"},    // 0.25 -> 0.2
		{"7/20", 1, "2/5"},   // 0.35 -> 0.4
		{"9/20", 1, "2/5"},   // 0.45 -> 0.4
		{"-3/20", 1, "-1/5"}, // -0.15 -> -0.2
		{"-1/4", 1, "-1/5"},  // -0.25 -> -0.2
		{"-9/20", 1, "-2/5"}, // -0.45 -> -0.4
	}
	for _, c := range cases {
		res, err := Solve(mustTerms(t, c.in), c.p)
		if err != nil {
			t.Fatalf("%s p=%d: %v", c.in, c.p, err)
		}
		if got := ratString(res.Final); got != c.want {
			t.Errorf("%s at p=%d: final = %s, want %s", c.in, c.p, got, c.want)
		}
	}
}

// Ordering genuinely matters here: only permutations starting with a half
// followed by 3/2 reach the exact sum 3 with zero deviation.
func TestOrderingMatters(t *testing.T) {
	res, err := Solve(mustTerms(t, "1/2", "3/2", "1"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ratString(res.Deviation); got != "0" {
		t.Fatalf("deviation = %s, want 0", got)
	}
	if got := ratString(res.Final); got != "3" {
		t.Fatalf("final = %s, want 3", got)
	}
	if !permEqual(res.Perm, 0, 1, 2) {
		t.Fatalf("perm = %v, want [0 1 2] (lexicographic tie-break)", res.Perm)
	}
}

// Duplicate values are still distinguished by index; the lexicographic
// tie-break must pick index order [0 1].
func TestDuplicateInputs(t *testing.T) {
	res, err := Solve(mustTerms(t, "1/2", "1/2"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !permEqual(res.Perm, 0, 1) {
		t.Fatalf("perm = %v, want [0 1]", res.Perm)
	}
	if got := ratString(res.Final); got != "0" {
		t.Fatalf("final = %s, want 0", got)
	}
	if got := ratString(res.Deviation); got != "1" {
		t.Fatalf("deviation = %s, want 1", got)
	}

	res3, err := Solve(mustTerms(t, "1/2", "1/2", "1/2"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !permEqual(res3.Perm, 0, 1, 2) {
		t.Fatalf("perm = %v, want [0 1 2]", res3.Perm)
	}
	if got := ratString(res3.Deviation); got != "3/2" {
		t.Fatalf("deviation = %s, want 3/2", got)
	}
}

// Twelve copies of 1/2 at p=0: every step sees exactly 1/2 and rounds to 0,
// so the final accumulator is 0 and the deviation equals the exact sum 6.
func TestTwelveIdenticalHalves(t *testing.T) {
	args := make([]string, 12)
	for i := range args {
		args[i] = "1/2"
	}
	res, err := Solve(mustTerms(t, args...), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ratString(res.Final); got != "0" {
		t.Fatalf("final = %s, want 0", got)
	}
	if got := ratString(res.Deviation); got != "6" {
		t.Fatalf("deviation = %s, want 6", got)
	}
	for i, idx := range res.Perm {
		if idx != i {
			t.Fatalf("perm = %v, want identity", res.Perm)
		}
	}
}

// A mixed 12-input instance: check internal consistency (replaying the
// permutation reproduces the claimed final value and deviation) and
// regression-check the optimum found by the subset DP.
func TestTwelveMixed(t *testing.T) {
	terms := mustTerms(t,
		"1/2", "-3/4", "5/6", "7/8", "-9/10", "11/12",
		"13/14", "-15/16", "17/18", "19/20", "-21/22", "23/24",
	)
	res, err := Solve(terms, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Replay consistency: the recorded steps must reproduce Final.
	acc := new(big.Rat)
	for _, s := range res.Steps {
		pre := new(big.Rat).Add(acc, s.Input)
		if pre.Cmp(s.Pre) != 0 {
			t.Fatalf("step pre-value mismatch: got %s, replayed %s", s.Pre, pre)
		}
		if got := roundRatHalfEven(pre, 2); got.Cmp(s.Post) != 0 {
			t.Fatalf("step post-value mismatch: got %s, want %s", got, s.Post)
		}
		acc = s.Post
	}
	if acc.Cmp(res.Final) != 0 {
		t.Fatalf("replayed final %s != reported %s", acc, res.Final)
	}
	dev := new(big.Rat).Sub(res.Final, res.Exact)
	dev.Abs(dev)
	if dev.Cmp(res.Deviation) != 0 {
		t.Fatalf("deviation %s inconsistent with final/exact", res.Deviation)
	}
	// Regression values (validated by the DP; see randomized cross-check below).
	if got := ratString(res.Deviation); got != wantTwelveMixedDev {
		t.Fatalf("deviation = %s, want %s", got, wantTwelveMixedDev)
	}
	if got := permString(res.Perm); got != wantTwelveMixedPerm {
		t.Fatalf("perm = %s, want %s", got, wantTwelveMixedPerm)
	}
	// The optimum must beat (or tie) the identity permutation.
	idFinal, _ := replay(terms, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, 2)
	idDev := new(big.Rat).Sub(idFinal, res.Exact)
	idDev.Abs(idDev)
	if res.Deviation.Cmp(idDev) > 0 {
		t.Fatalf("optimum deviation %s worse than identity %s", res.Deviation, idDev)
	}
}

func permString(perm []int) string {
	parts := make([]string, len(perm))
	for i, v := range perm {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, " ")
}

// Randomized cross-check: for n <= 8 the subset DP must agree with the
// independent exhaustive permutation search on every instance.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for trial := 0; trial < 100; trial++ {
		n := 1 + rng.Intn(8)
		args := make([]string, n)
		for i := range args {
			num := rng.Intn(81) - 40
			den := 1 + rng.Intn(16)
			args[i] = big.NewInt(int64(num)).String() + "/" + big.NewInt(int64(den)).String()
		}
		p := rng.Intn(4)
		terms := mustTerms(t, args...)
		dp, err := Solve(terms, p)
		if err != nil {
			t.Fatal(err)
		}
		bf, err := BruteForce(terms, p)
		if err != nil {
			t.Fatal(err)
		}
		if dp.Deviation.Cmp(bf.Deviation) != 0 || !permEqualInts(dp.Perm, bf.Perm) {
			t.Fatalf("mismatch on %v p=%d: dp dev=%s perm=%v, bf dev=%s perm=%v",
				args, p, dp.Deviation, dp.Perm, bf.Deviation, bf.Perm)
		}
	}
}

func permEqualInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Invalid inputs must be rejected.
func TestParseValidation(t *testing.T) {
	for _, bad := range []string{"1/0", "1/-2", "a", "1/2/3", ""} {
		if _, err := parseTerm(bad); err == nil {
			t.Errorf("parseTerm(%q) unexpectedly succeeded", bad)
		}
	}
	if _, err := Solve(mustTerms(t, "1/2"), -1); err == nil {
		t.Error("Solve with p=-1 unexpectedly succeeded")
	}
	thirteen := make([]string, 13)
	for i := range thirteen {
		thirteen[i] = "1"
	}
	if _, err := Solve(mustTerms(t, thirteen...), 0); err == nil {
		t.Error("Solve with 13 inputs unexpectedly succeeded")
	}
}
