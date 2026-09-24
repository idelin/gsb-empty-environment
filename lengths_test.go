package llex

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// bruteLengths enumerates every legal length vector (1 <= l_i <= L,
// Kraft sum <= 1) and returns the one with minimum weighted cost, breaking
// ties by lexicographic order of the vector. This is the independent
// reference for the first verification layer.
func bruteLengths(freqs []uint64, L int) ([]int, bool) {
	n := len(freqs)
	if n == 0 {
		return []int{}, true
	}
	if n == 1 {
		return []int{1}, true
	}
	if L < 20 && n > 1<<uint(L) {
		return nil, false
	}
	var best []int
	var bestCost *big.Int
	cur := make([]int, n)
	capKraft := 1 << uint(L)
	var rec func(i, kraft int)
	rec = func(i, kraft int) {
		if i == n {
			cost := new(big.Int)
			for j := range cur {
				c := new(big.Int).Mul(new(big.Int).SetUint64(freqs[j]), big.NewInt(int64(cur[j])))
				cost.Add(cost, c)
			}
			if bestCost == nil || cost.Cmp(bestCost) < 0 ||
				(cost.Cmp(bestCost) == 0 && lexLess(cur, best)) {
				bestCost = cost
				best = append([]int(nil), cur...)
			}
			return
		}
		for l := 1; l <= L; l++ {
			k := kraft + 1<<uint(L-l)
			if k > capKraft {
				continue
			}
			cur[i] = l
			rec(i+1, k)
		}
	}
	rec(0, 0)
	if best == nil {
		return nil, false
	}
	return best, true
}

func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func makeSymbols(freqs []uint64) []Symbol {
	syms := make([]Symbol, len(freqs))
	for i, f := range freqs {
		syms[i] = Symbol{ID: fmt.Sprintf("s%02d", i), Freq: f}
	}
	return syms
}

// First verification layer: the solver must match exhaustive enumeration
// of legal length vectors on small alphabets, in cost and in tie-break.
func TestSolveLengthsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20260924))
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(5) // 1..5 symbols
		L := 1 + rng.Intn(5) // L in 1..5
		freqs := make([]uint64, n)
		switch trial % 5 {
		case 0: // equal frequencies
			for i := range freqs {
				freqs[i] = uint64(1 + rng.Intn(1000))
			}
			for i := range freqs {
				freqs[i] = freqs[0]
			}
		case 1: // small random, zeros included
			for i := range freqs {
				freqs[i] = uint64(rng.Intn(6))
			}
		case 2: // mostly zeros
			for i := range freqs {
				if rng.Intn(3) != 0 {
					freqs[i] = 0
				} else {
					freqs[i] = uint64(1 + rng.Intn(50))
				}
			}
		case 3: // huge frequencies, exact integer arithmetic required
			for i := range freqs {
				freqs[i] = uint64(rng.Intn(4)) << 60
			}
		default: // skewed
			for i := range freqs {
				freqs[i] = uint64(1) << uint(rng.Intn(20))
			}
		}
		tab, err := SolveLengths(makeSymbols(freqs), L)
		want, ok := bruteLengths(freqs, L)
		if !ok {
			var inf *InfeasibleError
			if err == nil || !errors.As(err, &inf) {
				t.Fatalf("trial %d: n=%d L=%d freqs=%v: expected infeasible, got %v", trial, n, L, freqs, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: n=%d L=%d freqs=%v: unexpected error %v", trial, n, L, freqs, err)
		}
		if len(tab.Lengths) != n {
			t.Fatalf("trial %d: got %d lengths for %d symbols", trial, len(tab.Lengths), n)
		}
		for i := range want {
			if tab.Lengths[i] != want[i] {
				t.Fatalf("trial %d: n=%d L=%d freqs=%v: solver %v, brute force %v",
					trial, n, L, freqs, tab.Lengths, want)
			}
		}
	}
}

func TestEqualFrequencies(t *testing.T) {
	tab, err := SolveLengths(makeSymbols([]uint64{7, 7, 7, 7}), 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range tab.Lengths {
		if l != 2 {
			t.Fatalf("symbol %d: got length %d, want 2 (%v)", i, l, tab.Lengths)
		}
	}
}

func TestZeroFrequencySymbolsGetCodes(t *testing.T) {
	tab, err := SolveLengths(makeSymbols([]uint64{1, 1, 0, 0}), 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2, 3, 3}
	for i := range want {
		if tab.Lengths[i] != want[i] {
			t.Fatalf("got %v, want %v (zero-frequency symbols must still be coded)", tab.Lengths, want)
		}
	}
}

func TestPow2Boundary(t *testing.T) {
	// n = 2^L is feasible: every codeword has length exactly L.
	freqs := make([]uint64, 8)
	for i := range freqs {
		freqs[i] = 1
	}
	tab, err := SolveLengths(makeSymbols(freqs), 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range tab.Lengths {
		if l != 3 {
			t.Fatalf("symbol %d: got %d, want 3", i, l)
		}
	}
	// n = 2^L + 1 is infeasible, with Kraft-sum evidence.
	_, err = SolveLengths(makeSymbols(make([]uint64, 9)), 3)
	var inf *InfeasibleError
	if !errors.As(err, &inf) {
		t.Fatalf("expected InfeasibleError, got %v", err)
	}
	if !strings.Contains(err.Error(), "9") || !strings.Contains(err.Error(), "8") {
		t.Fatalf("error lacks evidence: %v", err)
	}
}

func TestHugeFrequenciesExact(t *testing.T) {
	// Frequencies near 2^63: any 64-bit-overflowing arithmetic would
	// corrupt the result. Verified against big-integer brute force.
	freqs := []uint64{1<<62 + 12345, 1<<62 - 1, 5}
	tab, err := SolveLengths(makeSymbols(freqs), 4)
	if err != nil {
		t.Fatal(err)
	}
	want, ok := bruteLengths(freqs, 4)
	if !ok {
		t.Fatal("brute force says infeasible")
	}
	for i := range want {
		if tab.Lengths[i] != want[i] {
			t.Fatalf("got %v, want %v", tab.Lengths, want)
		}
	}
	// Cross-check the exact cost with big integers.
	cost := new(big.Int)
	for i, l := range tab.Lengths {
		cost.Add(cost, new(big.Int).Mul(new(big.Int).SetUint64(freqs[i]), big.NewInt(int64(l))))
	}
	if !cost.IsUint64() || cost.Uint64() != freqs[0]*uint64(want[0])+freqs[1]*uint64(want[1])+freqs[2]*uint64(want[2]) {
		t.Fatalf("cost mismatch for %v", tab.Lengths)
	}
}

func TestSingleSymbolLengthOne(t *testing.T) {
	tab, err := SolveLengths([]Symbol{{ID: "only", Freq: 0}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Lengths) != 1 || tab.Lengths[0] != 1 {
		t.Fatalf("single symbol must get length 1, got %v", tab.Lengths)
	}
}

func TestEmptyAlphabetEmptyTable(t *testing.T) {
	tab, err := SolveLengths(nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.IDs) != 0 || len(tab.Lengths) != 0 {
		t.Fatalf("empty alphabet must yield empty table, got %+v", tab)
	}
}

func TestLargeLDoesNotEnumerateLeaves(t *testing.T) {
	// L = 10^9: enumerating 2^L leaves is impossible; the solver is
	// O(n * min(L, n)) and returns instantly.
	tab, err := SolveLengths(makeSymbols([]uint64{3, 1, 1, 2}), 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := bruteLengths([]uint64{3, 1, 1, 2}, 3) // effective L is n-1 = 3
	for i := range want {
		if tab.Lengths[i] != want[i] {
			t.Fatalf("got %v, want %v", tab.Lengths, want)
		}
	}
}

func TestTieBreakLexicographic(t *testing.T) {
	// Both [1,2,3,3] and [1,3,3,2] (ID order) have cost 18 for these
	// frequencies; the lexicographically smaller one must win.
	tab, err := SolveLengths(makeSymbols([]uint64{10, 1, 1, 1}), 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2, 3, 3}
	for i := range want {
		if tab.Lengths[i] != want[i] {
			t.Fatalf("got %v, want lexicographically smallest optimum %v", tab.Lengths, want)
		}
	}
}

func TestDuplicateIDsRejected(t *testing.T) {
	_, err := SolveLengths([]Symbol{{ID: "a", Freq: 1}, {ID: "a", Freq: 2}}, 4)
	if err == nil {
		t.Fatal("duplicate IDs must be rejected")
	}
}
