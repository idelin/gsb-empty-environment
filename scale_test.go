package lpc

import (
	"math/big"
	"testing"
)

// O(n*L) smoke test: thousands of symbols at a realistic limit must be
// quick and produce a Kraft-complete code respecting the bound.
func TestLargeAlphabet(t *testing.T) {
	const n = 4096
	const L = 15
	weights := make([]uint64, n)
	for i := range weights {
		weights[i] = uint64(i + 1)
	}
	rep, err := Build(weights, L)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rep.Code.SymbolCount() != n {
		t.Fatalf("symbol count = %d", rep.Code.SymbolCount())
	}
	if rep.Code.Kraft.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("Kraft = %v", rep.Code.Kraft)
	}
	for s, l := range rep.Code.Lengths {
		if l < 1 || l > L {
			t.Fatalf("symbol %d length %d out of range", s, l)
		}
	}
	assertPrefixFree(t, rep.Code)

	data, err := rep.Code.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	c2, err := UnmarshalCode(data)
	if err != nil {
		t.Fatalf("UnmarshalCode: %v", err)
	}
	for i := range rep.Code.Lengths {
		if c2.Lengths[i] != rep.Code.Lengths[i] {
			t.Fatalf("length table mismatch at %d", i)
		}
	}
}
