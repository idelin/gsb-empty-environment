package prefixcode

import (
	"math/rand"
	"testing"
)

// bruteForceOptimal enumerates every legal code-length assignment for the
// alphabet (all lengths in [1, maxLen], Kraft sum exactly 1) and returns
// the minimum possible cost. Only feasible for small alphabets.
func bruteForceOptimal(freqs []uint64, maxLen int) (uint64, bool) {
	full := 1 << uint(maxLen)
	best := ^uint64(0)
	n := len(freqs)
	var rec func(i, kraft int, cost uint64)
	rec = func(i, kraft int, cost uint64) {
		if cost >= best {
			return
		}
		if i == n {
			if kraft == full {
				best = cost
			}
			return
		}
		for l := 1; l <= maxLen; l++ {
			k := kraft + (1 << uint(maxLen-l))
			if k > full {
				continue
			}
			rec(i+1, k, cost+freqs[i]*uint64(l))
		}
	}
	rec(0, 0, 0)
	if best == ^uint64(0) {
		return 0, false
	}
	return best, true
}

func TestOptimalSmallAlphabet(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for n := 2; n <= 6; n++ {
		for trial := 0; trial < 150; trial++ {
			freqs := make([]uint64, n)
			for i := range freqs {
				freqs[i] = uint64(rng.Intn(30) + 1)
				if rng.Intn(3) == 0 {
					freqs[i] = freqs[rng.Intn(i+1)] // force ties
				}
			}
			maxLen := n // unconstrained optimum
			switch trial % 3 {
			case 1:
				if n > 3 {
					maxLen = n - 1 // mildly constrained
				}
			case 2:
				for maxLen > 1 && 1<<uint(maxLen-1) >= n {
					maxLen-- // tightest feasible maxLen
				}
			}
			lengths, err := BuildLengths(freqs, maxLen)
			if err != nil {
				t.Fatalf("n=%d maxLen=%d freqs=%v: %v", n, maxLen, freqs, err)
			}
			got := Cost(freqs, lengths)
			want, ok := bruteForceOptimal(freqs, maxLen)
			if !ok {
				t.Fatalf("n=%d maxLen=%d: no legal assignment exists?", n, maxLen)
			}
			if got != want {
				t.Errorf("n=%d maxLen=%d freqs=%v: cost %d, optimal %d (lengths %v)",
					n, maxLen, freqs, got, want, lengths)
			}
		}
	}
}

func TestOptimalityAgainstEntropyBound(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	for trial := 0; trial < 100; trial++ {
		var freqs [256]uint64
		var total uint64
		n := 2 + rng.Intn(100)
		for i := 0; i < n; i++ {
			f := uint64(rng.Intn(1000) + 1)
			freqs[rng.Intn(256)] += f
			total += f
		}
		lengths, err := BuildLengths(freqs[:], 24)
		if err != nil {
			t.Fatal(err)
		}
		cost := Cost(freqs[:], lengths)
		bound := Entropy(freqs[:]) * float64(total)
		if float64(cost) < bound-1e-9 {
			t.Fatalf("cost %d below entropy bound %.2f: impossible", cost, bound)
		}
		// Huffman codes are within 1 bit/symbol of entropy.
		if float64(cost) > bound+float64(total) {
			t.Fatalf("cost %d exceeds entropy + 1 bit/symbol (%.2f)", cost, bound+float64(total))
		}
	}
}
