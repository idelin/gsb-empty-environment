package fuzzy

import (
	"math/rand"
	"testing"
)

var unit = Costs{Ins: 1, Del: 1, Rep: 1, Trans: 1}

func TestDistanceBasic(t *testing.T) {
	cases := []struct {
		a, b string
		c    Costs
		want int
	}{
		{"", "", unit, 0},
		{"abc", "abc", unit, 0},
		{"", "abc", unit, 3},
		{"abc", "", unit, 3},
		{"kitten", "sitting", unit, 3},
		{"flaw", "lawn", unit, 2},
		{"ab", "ba", unit, 1},                  // one adjacent transposition
		{"ab", "ba", Costs{3, 3, 5, 1}, 1},     // cheap swap beats delete+insert
		{"ab", "ba", Costs{3, 3, 5, 100}, 6},   // dear swap: delete+insert wins
		{"abcd", "badc", Costs{9, 9, 9, 2}, 4}, // two disjoint swaps
	}
	for _, tc := range cases {
		if got := Distance(tc.a, tc.b, tc.c); got != tc.want {
			t.Errorf("Distance(%q, %q, %+v) = %d, want %d", tc.a, tc.b, tc.c, got, tc.want)
		}
	}
}

// A transposition is a single alignment step over two adjacent positions;
// turning "abc" into "cba" cannot be done with one swap and the library
// does not model a cascade of swaps, so with cheap swaps and dear
// replacements the cost is two replacements.
func TestDistanceTranspositionSemantics(t *testing.T) {
	c := Costs{Ins: 100, Del: 100, Rep: 100, Trans: 1}
	if got := Distance("abc", "cba", c); got != 200 {
		t.Fatalf("Distance(abc, cba) = %d, want 200 (two replacements)", got)
	}
	if got := bruteDistance("abc", "cba", c); got != 200 {
		t.Fatalf("bruteDistance(abc, cba) = %d, want 200", got)
	}
}

// Zero-cost insert and delete make the string-level edit graph full of
// zero-cost cycles (insert a character, delete it again). Any word can be
// reached for free by deleting everything and inserting the target.
func TestDistanceZeroCostCycles(t *testing.T) {
	c := Costs{Ins: 0, Del: 0, Rep: 5, Trans: 5}
	for _, pair := range [][2]string{{"abc", "xyz"}, {"", "abcd"}, {"aa", "bb"}} {
		if got := Distance(pair[0], pair[1], c); got != 0 {
			t.Errorf("Distance(%q, %q, %+v) = %d, want 0", pair[0], pair[1], c, got)
		}
		if got := bruteDistance(pair[0], pair[1], c); got != 0 {
			t.Errorf("bruteDistance(%q, %q, %+v) = %d, want 0", pair[0], pair[1], c, got)
		}
	}
	zero := Costs{}
	if got := Distance("anything", "at all", zero); got != 0 {
		t.Errorf("Distance with all-zero costs = %d, want 0", got)
	}
}

// The same word is reachable through many edit paths; the distance must be
// the minimum over all of them.
func TestDistanceMultiplePathsSameWord(t *testing.T) {
	// "ab" -> "ba": two replacements (4), a swap (3), or delete+insert (2).
	c := Costs{Ins: 1, Del: 1, Rep: 2, Trans: 3}
	if got := Distance("ab", "ba", c); got != 2 {
		t.Fatalf("Distance(ab, ba, %+v) = %d, want 2", c, got)
	}
	// Zero-cost replacements make "ab" -> "ba" free even without a swap.
	c = Costs{Ins: 3, Del: 3, Rep: 0, Trans: 7}
	if got := Distance("ab", "ba", c); got != 0 {
		t.Fatalf("Distance(ab, ba, %+v) = %d, want 0", c, got)
	}
	// Insert then delete the same character is a detour that must not
	// lower or raise the cost of a direct match.
	c = Costs{Ins: 1, Del: 1, Rep: 4, Trans: 4}
	if got := Distance("abc", "abc", c); got != 0 {
		t.Fatalf("Distance(abc, abc, %+v) = %d, want 0", c, got)
	}
}

// Cross-check the dynamic programming against an exhaustive Dijkstra
// search of the edit graph on short words. Zero costs are included, so
// zero-cost edges and ties between many edit paths are exercised.
func TestDistanceAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabets := []string{"ab", "abc", "aab"}
	for iter := 0; iter < 4000; iter++ {
		alphabet := []rune(alphabets[rng.Intn(len(alphabets))])
		a := randWord(rng, alphabet, 5)
		b := randWord(rng, alphabet, 5)
		c := randCosts(rng, 3)
		got := Distance(a, b, c)
		want := bruteDistance(a, b, c)
		if got != want {
			t.Fatalf("Distance(%q, %q, %+v) = %d, brute force says %d", a, b, c, got, want)
		}
	}
}

func TestDistanceNegativeCostsPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on negative costs")
		}
	}()
	Distance("a", "b", Costs{Ins: -1})
}
